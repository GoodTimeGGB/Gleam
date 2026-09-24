package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gleam/internal/config"
	"gleam/internal/harness/scheduler"
	"gleam/internal/harness/skill"
	"gleam/internal/llm"
	"gleam/internal/market"
	"gleam/internal/nlcron"
	"gleam/internal/tools/mcp"
	"gleam/pkg/types"
)

// mcpNameRe MCP 服务器名合法性（用于工具名 mcp.<name>.<tool>）。
var mcpNameRe = regexp.MustCompile(`^[a-zA-Z0-9_\-]{1,32}$`)

// 本文件实现 internal/webui 所需的引擎门面方法：
// 工具直调（带审批）、记忆、技能、调度的统一入口。

// LLMName 模型标识。
func (a *Agent) LLMName() string {
	if a.LLM == nil {
		return "unavailable"
	}
	return a.LLM.Name()
}

// ToolNames 工具名列表。
func (a *Agent) ToolNames() []string { return a.Reg.Names() }

// ToolList 工具详情列表（含 schema、有效权限与覆盖标记）。
func (a *Agent) ToolList() []map[string]any {
	list := make([]map[string]any, 0)
	for _, t := range a.Reg.List() {
		effective := a.Gate.EffectivePermission(t)
		_, overridden := a.Gate.OverrideOf(t.Name())
		list = append(list, map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"permission":  effective.String(),
			"overridden":  overridden,
			"schema":      t.Schema(),
		})
	}
	return list
}

// ToolPermissionSet 设置工具权限覆盖（"readonly"/"user_approved"/"full_access"/"default"）。
// "default" 清除覆盖恢复内置；其余持久化到覆盖层并热生效。
func (a *Agent) ToolPermissionSet(name, perm string) (map[string]any, error) {
	if _, ok := a.Reg.Get(name); !ok {
		return nil, fmt.Errorf("工具 %q 不存在", name)
	}
	switch perm {
	case "default":
		a.Gate.ClearToolPermission(name)
	case "readonly", "user_approved", "full_access":
		a.Gate.SetToolPermission(name, permFromStr(perm))
	default:
		return nil, fmt.Errorf("非法权限 %q（readonly | user_approved | full_access | default）", perm)
	}
	a.Cfg.Safety.ToolPermissions = map[string]string{}
	for _, t := range a.Reg.List() {
		if p, ok := a.Gate.OverrideOf(t.Name()); ok {
			a.Cfg.Safety.ToolPermissions[t.Name()] = p.String()
		}
	}
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return nil, fmt.Errorf("已生效但持久化失败: %w", err)
	}
	return map[string]any{
		"name":       name,
		"permission": a.PermissionOf(name).String(),
		"overridden": perm != "default",
		"tools":      a.ToolList(),
	}, nil
}

// DirectToolCall 直接调用工具；需要审批时经 approve 回调裁决。
func (a *Agent) DirectToolCall(ctx context.Context, name string, args map[string]any,
	approve func(types.ApprovalRequest) types.ApprovalResponse) (map[string]any, error) {
	tool, ok := a.Reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("工具 %q 不存在", name)
	}
	dec := a.Gate.Evaluate(tool, args)
	if dec.NeedApproval {
		resp := approve(types.ApprovalRequest{
			Plan:   []string{fmt.Sprintf("直接调用工具 %s", tool.Name())},
			Risk:   dec.Risk,
			Reason: dec.Reason,
		})
		if !resp.Approved {
			return nil, fmt.Errorf("操作被拒绝: %s", resp.Note)
		}
	}
	out, err := tool.Execute(ctx, args)
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": tool.Name(), "output": out}, nil
}

// MemorySearch 长期记忆检索。
func (a *Agent) MemorySearch(query string, k int) []types.MemoryHitView {
	if a.Mem == nil {
		return nil
	}
	hits := a.Mem.Relevant(query, k)
	out := make([]types.MemoryHitView, 0, len(hits))
	for _, h := range hits {
		out = append(out, types.MemoryHitView{ID: h.ID, Content: h.Content, Tags: h.Tags, Score: h.Score})
	}
	return out
}

// MemoryDelete 软删除一条长期记忆（检索立即跳过，条目保留可改判）。
func (a *Agent) MemoryDelete(id string) bool {
	if a.Mem == nil || a.Mem.Long == nil {
		return false
	}
	return a.Mem.Long.SoftDelete(id)
}

// MemorySave 写入长期记忆。
func (a *Agent) MemorySave(content string, tags []string) (string, error) {
	if a.Mem == nil {
		return "", errors.New("记忆系统未启用")
	}
	return a.Mem.Remember(content, tags)
}

// SkillList 技能列表。
func (a *Agent) SkillList() []skill.Skill { return a.Skills.List() }

// SkillSave 保存技能。
func (a *Agent) SkillSave(sk skill.Skill) (int, error) { return a.Skills.Save(sk) }

// SkillRun 运行技能。
func (a *Agent) SkillRun(ctx context.Context, name string, params map[string]string) (map[string]any, error) {
	return a.RunSkill(ctx, name, params)
}

// SkillDelete 删除技能。
func (a *Agent) SkillDelete(name string) error { return a.Skills.Delete(name) }

// ScheduleList 定时任务列表。
func (a *Agent) ScheduleList() []types.ScheduleJobView {
	if a.Sched == nil {
		return nil
	}
	jobs := a.Sched.ListJobs()
	out := make([]types.ScheduleJobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobToScheduleView(j))
	}
	return out
}

// ScheduleCreate 用 cron 或固定间隔创建定时任务（兼容旧入口）。
func (a *Agent) ScheduleCreate(name, cron string, intervalSec int, goal, mode string) (types.ScheduleJobView, error) {
	return a.ScheduleCreateNatural(name, goal, mode, "", cron, intervalSec)
}

// ScheduleCreateNatural 创建定时任务：when 为自然语言时间描述，cron/intervalSec 为显式方式（优先级更高）。
func (a *Agent) ScheduleCreateNatural(name, goal, mode, when, cron string, intervalSec int) (types.ScheduleJobView, error) {
	return a.ScheduleCreateWithNotify(name, goal, mode, when, cron, intervalSec, "")
}

// ScheduleCreateWithNotify 创建定时任务并设置通知策略（notify 为空则用默认）。
//
// **先校验策略再创建**：策略非法时直接返回，不留下一个"任务建好了但策略没设上"的
// 半成品——那种状态最难排查，因为任务看起来一切正常，只是跑完不通知。
func (a *Agent) ScheduleCreateWithNotify(name, goal, mode, when, cron string, intervalSec int, notify string) (types.ScheduleJobView, error) {
	if a.Sched == nil {
		return types.ScheduleJobView{}, fmt.Errorf("调度器未启用")
	}
	policy, err := scheduler.NormalizeNotify(notify)
	if err != nil {
		return types.ScheduleJobView{}, err
	}
	cron, intervalSec, summary, err := nlcron.Resolve(cron, intervalSec, when)
	if err != nil {
		return types.ScheduleJobView{}, err
	}
	j, err := a.Sched.AddJobDetailed(name, cron, intervalSec, goal, mode, strings.TrimSpace(when), summary)
	if err != nil {
		return types.ScheduleJobView{}, err
	}
	if strings.TrimSpace(notify) != "" {
		if err := a.Sched.SetNotify(j.Name, policy); err != nil {
			return types.ScheduleJobView{}, err
		}
	}
	return a.scheduleView(j.Name), nil
}

// ScheduleSetNotify 修改已有任务的通知策略（空串恢复默认）。
func (a *Agent) ScheduleSetNotify(name, policy string) (types.ScheduleJobView, error) {
	if a.Sched == nil {
		return types.ScheduleJobView{}, fmt.Errorf("调度器未启用")
	}
	if err := a.Sched.SetNotify(name, policy); err != nil {
		return types.ScheduleJobView{}, err
	}
	return a.scheduleView(name), nil
}

// ScheduleSetEnabled 启停已有定时任务（暂停/恢复）。
func (a *Agent) ScheduleSetEnabled(name string, enabled bool) (types.ScheduleJobView, error) {
	if a.Sched == nil {
		return types.ScheduleJobView{}, fmt.Errorf("调度器未启用")
	}
	if err := a.Sched.SetEnabled(name, enabled); err != nil {
		return types.ScheduleJobView{}, err
	}
	return a.scheduleView(name), nil
}

// ScheduleDelete 删除定时任务。
func (a *Agent) ScheduleDelete(name string) (bool, error) {
	if a.Sched == nil {
		return false, fmt.Errorf("调度器未启用")
	}
	return a.Sched.DeleteJob(name)
}

// ScheduleTrigger HTTP 回调触发：立即执行指定定时任务。
func (a *Agent) ScheduleTrigger(name string) (bool, error) {
	if a.Sched == nil {
		return false, fmt.Errorf("调度器未启用")
	}
	return a.Sched.TriggerNow(name)
}

// ---------- 设置与上下文（设置页） ----------

// settingsStyles 设置页允许的协作风格。
var settingsStyles = map[string]bool{"rigorous": true, "gentle": true, "efficient": true}

// settingsModes 设置页允许的安全模式。
var settingsModes = map[string]bool{"auto": true, "plan_first": true, "interactive": true}

// SettingsView 返回设置页所需的完整视图（api_key 掩码，不回传明文）。
func (a *Agent) SettingsView() map[string]any {
	cfg := a.Cfg
	apiKeySet := cfg.LLM.APIKey != ""
	return map[string]any{
		"persona": map[string]any{"name": cfg.Persona.Name, "style": cfg.Persona.Style},
		"safety": map[string]any{
			"mode":                     cfg.Safety.Mode,
			"approval_timeout_seconds": cfg.Safety.ApprovalTimeoutSecs,
			"ai_review":                cfg.Safety.AIReview,
		},
		"agent": map[string]any{
			"max_replans":               cfg.Agent.MaxReplans,
			"max_steps":                 cfg.Agent.MaxSteps,
			"step_timeout_seconds":      cfg.Agent.StepTimeoutSecs,
			"step_retries":              cfg.Agent.StepRetries,
			"done_threshold":            cfg.Agent.DoneThreshold,
			"max_concurrency":           cfg.Agent.MaxConcurrency,
			"context_compress":          cfg.Agent.ContextCompress,
			"skill_auto_optimize":       cfg.Agent.SkillAutoOptimize,
			"geo_enabled":               cfg.Agent.GEOEnabled,
			"dedupe_calls":              cfg.Agent.DedupeCalls,
			"max_llm_calls_per_task":    cfg.Agent.MaxLLMCallsPerTask,
			"max_tokens_per_task":       cfg.Agent.MaxTokensPerTask,
			"max_task_duration_seconds": cfg.Agent.MaxTaskDurationSecs,
			"stuck_threshold":           cfg.Agent.StuckThreshold,
			"max_output_runes":          cfg.Agent.MaxOutputRunes,
			"max_tool_schemas":          cfg.Agent.MaxToolSchemas,
			"chat_acceptance":           cfg.Agent.ChatAcceptance,
		},
		"memory": map[string]any{
			"short_term_capacity": cfg.Memory.ShortTermCap,
			"vector_dim":          cfg.Memory.VectorDim,
			"max_items":           cfg.Memory.MaxItems,
		},
		"llm": map[string]any{
			"provider":        cfg.LLM.Provider,
			"protocol":        cfg.LLM.Protocol,
			"provider_id":     cfg.LLM.ProviderID,
			"plan":            cfg.LLM.Plan,
			"model":           cfg.LLM.Model,
			"fast_model":      cfg.LLM.FastModel,
			"tiers":           tiersView(cfg.LLM.Tiers),
			"base_url":        cfg.LLM.BaseURL,
			"temperature":     cfg.LLM.Temperature,
			"max_tokens":      cfg.LLM.MaxTokens,
			"timeout_seconds": cfg.LLM.TimeoutSecs,
			"api_key_set":     apiKeySet,
		},
		"scheduler": map[string]any{"enabled": cfg.Scheduler.Enabled},
		"workspace": cfg.Workspace,
		"data_dir":  cfg.DataDir,
	}
}

// tiersView 把模型档位表转成前端可直接消费的形式：档位名 → 模型 ID。
// 空表也返回空 map 而不是 nil，前端才好判断"没配"与"没这个字段"。
func tiersView(tiers map[string]string) map[string]any {
	out := map[string]any{}
	for name, model := range tiers {
		out[name] = model
	}
	return out
}

// ApplySettings 校验并把设置补丁应用到运行时（模型/风格/安全模式等即改即用），
// 持久化覆盖层后返回新视图。api_key 仅在非空时更新。
func (a *Agent) ApplySettings(patch map[string]any) (map[string]any, error) {
	sanitized := map[string]any{}
	sub := func(section string) map[string]any {
		m, ok := sanitized[section].(map[string]any)
		if !ok {
			m = map[string]any{}
			sanitized[section] = m
		}
		return m
	}
	asInt := func(v any) (int, bool) {
		switch n := v.(type) {
		case float64:
			return int(n), true
		case int:
			return n, true
		case int64:
			return int(n), true
		case string:
			var i int
			if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err == nil {
				return i, true
			}
		}
		return 0, false
	}
	// 越界字段收集器：posInt 曾因只有下限，done_threshold=150 / max_replans=999
	// 被"保存成功"静默接受并写进 overlay。现在越界整次拒绝、明确报错。
	var outOfRange []string
	posInt := func(section, key string, v any, min, max int) {
		i, ok := asInt(v)
		if !ok {
			return // 非数字维持旧语义：忽略该字段
		}
		if i < min || i > max {
			outOfRange = append(outOfRange, fmt.Sprintf("%s=%d（允许 %d–%d）", key, i, min, max))
			return
		}
		sub(section)[key] = i
	}
	// zeroOr：0 表示"关闭/不限"，开启时必须在 [min,max] 内才有意义
	zeroOr := func(section, key string, v any, min, max int) {
		if i, ok := asInt(v); ok && i == 0 {
			sub(section)[key] = 0
			return
		}
		posInt(section, key, v, min, max)
	}

	if pm, ok := patch["persona"].(map[string]any); ok {
		if v, ok := pm["name"].(string); ok && strings.TrimSpace(v) != "" {
			sub("persona")["name"] = strings.TrimSpace(v)
		}
		if v, ok := pm["style"].(string); ok && settingsStyles[v] {
			sub("persona")["style"] = v
		}
	}
	if sm, ok := patch["safety"].(map[string]any); ok {
		if v, ok := sm["mode"].(string); ok && settingsModes[v] {
			sub("safety")["mode"] = v
		}
		if v, ok := sm["approval_timeout_seconds"]; ok {
			posInt("safety", "approval_timeout_seconds", v, 5, 3600)
		}
		if b, ok := sm["ai_review"].(bool); ok {
			sub("safety")["ai_review"] = b
		}
	}
	if am, ok := patch["agent"].(map[string]any); ok {
		// 上限是"配置还能叫配置"的边界：done_threshold>100 永不达标、max_replans 数百
		// 等于烧钱开关。max_steps=0 合法（回退 DefaultMaxSteps，见 config.go）。
		for _, f := range []struct {
			key      string
			min, max int
		}{
			{"max_replans", 0, 20},
			{"max_steps", 0, 512},
			{"step_timeout_seconds", 0, 3600},
			{"step_retries", 0, 10},
			{"done_threshold", 0, 100},
			{"max_concurrency", 0, 16},
		} {
			if v, ok := am[f.key]; ok {
				posInt("agent", f.key, v, f.min, f.max)
			}
		}
		if b, ok := am["context_compress"].(bool); ok {
			sub("agent")["context_compress"] = b
		}
		if b, ok := am["skill_auto_optimize"].(bool); ok {
			sub("agent")["skill_auto_optimize"] = b
		}
		if b, ok := am["geo_enabled"].(bool); ok {
			sub("agent")["geo_enabled"] = b
		}
		if b, ok := am["dedupe_calls"].(bool); ok {
			sub("agent")["dedupe_calls"] = b
		}
		if i, ok := am["max_llm_calls_per_task"]; ok {
			zeroOr("agent", "max_llm_calls_per_task", i, 1, 10000)
		}
		if i, ok := am["max_tokens_per_task"]; ok {
			zeroOr("agent", "max_tokens_per_task", i, 1000, 100000000)
		}
		if i, ok := am["max_task_duration_seconds"]; ok {
			zeroOr("agent", "max_task_duration_seconds", i, 10, 86400)
		}
		// 防打转阈值：0 关闭，开启时至少 2 轮才有意义
		if i, ok := am["stuck_threshold"]; ok {
			zeroOr("agent", "stuck_threshold", i, 2, 100)
		}
		// 工具输出预算：0 不限，开启时给个下限避免设成无意义的极小值
		if i, ok := am["max_output_runes"]; ok {
			zeroOr("agent", "max_output_runes", i, 500, 1000000)
		}
		// 能力菜单阈值：0 不限（始终全量），开启时至少要够放下常用工具
		if i, ok := am["max_tool_schemas"]; ok {
			zeroOr("agent", "max_tool_schemas", i, 5, 1000)
		}
		// 对话模式自检：布尔开关，无需范围校验
		if b, ok := am["chat_acceptance"].(bool); ok {
			sub("agent")["chat_acceptance"] = b
		}
	}
	if mm, ok := patch["memory"].(map[string]any); ok {
		if v, ok := mm["short_term_capacity"]; ok {
			posInt("memory", "short_term_capacity", v, 2, 1000)
		}
		if v, ok := mm["vector_dim"]; ok {
			posInt("memory", "vector_dim", v, 32, 8192)
		}
		if v, ok := mm["max_items"]; ok {
			posInt("memory", "max_items", v, 10, 100000)
		}
	}
	if lm, ok := patch["llm"].(map[string]any); ok {
		if v, ok := lm["protocol"].(string); ok && llm.ValidProtocol(v) {
			sub("llm")["protocol"] = v
		} else if v, ok := lm["protocol"].(string); ok && strings.TrimSpace(v) == "" {
			sub("llm")["protocol"] = ""
		}
		if v, ok := lm["provider_id"].(string); ok {
			v = strings.TrimSpace(v)
			if v == "" || llm.FindProvider(v) != nil {
				sub("llm")["provider_id"] = v
			}
		}
		if v, ok := lm["plan"].(string); ok {
			v = strings.TrimSpace(v)
			switch v {
			case "", llm.PlanToken, llm.PlanCoding, llm.PlanAgent:
				sub("llm")["plan"] = v
			}
		}
		// base_url 允许显式清空以回落厂商预设
		if v, ok := lm["base_url"].(string); ok {
			sub("llm")["base_url"] = strings.TrimSpace(v)
		}
		if v, ok := lm["model"].(string); ok && strings.TrimSpace(v) != "" {
			sub("llm")["model"] = strings.TrimSpace(v)
		}
		// 辅助模型允许清空（清空即回退到主模型）
		if v, ok := lm["fast_model"].(string); ok {
			sub("llm")["fast_model"] = strings.TrimSpace(v)
		}
		// 模型档位表允许整表替换（也允许清空：清空即所有场景都用主模型）
		if v, ok := lm["tiers"].(map[string]any); ok {
			tiers := map[string]any{}
			for name, model := range v {
				name = strings.TrimSpace(name)
				s, _ := model.(string)
				s = strings.TrimSpace(s)
				if name != "" && s != "" {
					tiers[name] = s
				}
			}
			sub("llm")["tiers"] = tiers
		}
		// 密钥两档语义：api_key 非空=更新；clear_api_key=true=显式清除。
		// 空串不算清除（前端留空表示"不修改"），必须走显式标志。
		if cv, ok := lm["clear_api_key"].(bool); ok && cv {
			sub("llm")["api_key"] = ""
			sub("llm")["clear_api_key"] = true
		} else if v, ok := lm["api_key"].(string); ok && strings.TrimSpace(v) != "" {
			sub("llm")["api_key"] = strings.TrimSpace(v)
		}
		if v, ok := lm["temperature"].(float64); ok && v > 0 && v < 2 {
			sub("llm")["temperature"] = v
		}
		if v, ok := lm["max_tokens"]; ok {
			posInt("llm", "max_tokens", v, 64, 1000000)
		}
		if v, ok := lm["timeout_seconds"]; ok {
			posInt("llm", "timeout_seconds", v, 3, 600)
		}
	}
	if v, ok := patch["scheduler"].(map[string]any); ok {
		if b, ok := v["enabled"].(bool); ok {
			sub("scheduler")["enabled"] = b
		}
	}
	if len(outOfRange) > 0 {
		// 整次拒绝：静默丢掉越界字段会让用户以为"保存了但没生效"，
		// 静默接受更糟——非法值直接进 overlay 影响每次重启。
		return a.SettingsView(), fmt.Errorf("参数越界：%s", strings.Join(outOfRange, "；"))
	}
	if len(sanitized) == 0 {
		return a.SettingsView(), fmt.Errorf("没有可应用的有效设置")
	}

	a.Cfg.Apply(sanitized)

	// 运行时热生效：LLM 客户端按协议工厂重建（mock 保持不变），安全门控就地更新
	if a.Cfg.LLM.Provider != "mock" {
		if _, hasLLM := sanitized["llm"]; hasLLM {
			baseURL, model, protocol := llm.ResolvePreset(a.Cfg.LLM.ProviderID, a.Cfg.LLM.Plan, a.Cfg.LLM.BaseURL, a.Cfg.LLM.Model)
			if protocol == "" {
				protocol = a.Cfg.LLM.Protocol
			}
			if protocol == "" {
				protocol = llm.ProtocolOpenAIChat
			}
			a.Cfg.LLM.BaseURL, a.Cfg.LLM.Model, a.Cfg.LLM.Protocol = baseURL, model, protocol
			a.LLM = llm.New(protocol, baseURL, a.Cfg.LLM.APIKey, model,
				a.Cfg.LLM.Temperature, a.Cfg.LLM.MaxTokens, a.Cfg.LLM.TimeoutSecs)
			a.RebuildFastClient()
			// 档位映射可能变了（换了 base_url / 改了 tiers），缓存的客户端必须作废
			a.RebuildTierClients()
		}
	}
	if a.Gate != nil {
		if m, ok := sanitized["safety"].(map[string]any); ok {
			if v, ok := m["mode"].(string); ok {
				a.Gate.SetMode(v)
			}
			if v, ok := m["approval_timeout_seconds"].(int); ok {
				a.Gate.SetApprovalTimeout(time.Duration(v) * time.Second)
			}
		}
	}

	// 持久化密钥到本地独立凭证文件（0600 + DPAPI），重启免填；与 settings.yaml 分离
	if a.Creds != nil {
		if lm, ok := sanitized["llm"].(map[string]any); ok {
			if clear, _ := lm["clear_api_key"].(bool); clear {
				if err := a.Creds.SetLLMAPIKey(""); err != nil {
					return a.SettingsView(), fmt.Errorf("密钥已清除但本地凭证文件更新失败: %w", err)
				}
			} else if k, ok := lm["api_key"].(string); ok && k != "" {
				if err := a.Creds.SetLLMAPIKey(k); err != nil {
					return a.SettingsView(), fmt.Errorf("密钥已生效但保存到本地失败: %w", err)
				}
			}
		}
	}

	// 持久化覆盖层（重启后仍生效）
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return a.SettingsView(), fmt.Errorf("设置已生效但保存失败: %w", err)
	}
	return a.SettingsView(), nil
}

// llmFormTarget 把表单覆盖值与当前生效配置解析成完整接入目标（探测与拉模型列表共用）。
// override 可带 provider_id/plan/base_url/model/protocol/api_key；注意 Cfg 里的 base_url
// 已含预设回落结果，所以"显式空串"与"缺省"必须区分：缺省取生效值，显式空取显式空。
func (a *Agent) llmFormTarget(override map[string]any) (base, model, protocol, key string) {
	str := func(k string) string {
		if v, ok := override[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	pid := firstNonEmpty(str("provider_id"), a.Cfg.LLM.ProviderID)
	plan := firstNonEmpty(str("plan"), a.Cfg.LLM.Plan)
	base = str("base_url")
	if _, ok := override["base_url"].(string); !ok {
		base = a.Cfg.LLM.BaseURL
	}
	model = firstNonEmpty(str("model"), a.Cfg.LLM.Model)
	protocol = firstNonEmpty(str("protocol"), a.Cfg.LLM.Protocol)
	key = firstNonEmpty(str("api_key"), a.Cfg.LLM.APIKey)
	base, model, resolved := llm.ResolvePreset(pid, plan, base, model)
	if resolved != "" {
		protocol = resolved
	}
	if !llm.ValidProtocol(protocol) {
		protocol = llm.ProtocolOpenAIChat
	}
	return
}

// probeHasExplicitTarget 表单里带了非空 base_url：用户正在预验证一套尚未生效的接入
// （典型：还停在 mock 就填了真实网关想先测一把）。此时 mock 短路必须让位，
// 否则"测试连接/拉取模型"在最需要它的时刻恰好失灵（2026-09-23 QA 报告 M4）。
func probeHasExplicitTarget(override map[string]any) bool {
	v, ok := override["base_url"].(string)
	return ok && strings.TrimSpace(v) != ""
}

// TestLLMConnection 连通性探测：用当前生效配置（或表单覆盖值）发一次最小请求，
// 把失败分类成人能看懂的结论。设置页"测试连接"用——配错不必等任务失败才暴露。
// override 可带 protocol/provider_id/plan/base_url/model/api_key；api_key 留空表示用已保存的。
func (a *Agent) TestLLMConnection(override map[string]any) map[string]any {
	if a.Cfg.LLM.Provider == "mock" && !probeHasExplicitTarget(override) {
		return map[string]any{"ok": true, "kind": "mock", "message": "当前为离线 Mock 模型，未发起真实网络调用"}
	}
	base, model, protocol, key := a.llmFormTarget(override)
	if strings.TrimSpace(base) == "" || strings.TrimSpace(model) == "" {
		return map[string]any{"ok": false, "kind": "config", "message": "base_url 或模型名为空：选择厂商预设或直接填写"}
	}
	client := llm.New(protocol, base, key, model, 0.1, 16, 15)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	reply, err := client.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "ping，请只回复 ok"}},
	})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{
			"ok": false, "kind": classifyLLMError(ctx, err),
			"http_status": llm.StatusCode(err), "latency_ms": latency,
			"message": err.Error(),
		}
	}
	preview := reply
	if len([]rune(preview)) > 40 {
		preview = string([]rune(preview)[:40]) + "…"
	}
	return map[string]any{
		"ok": true, "kind": "ok", "latency_ms": latency,
		"model": model, "base_url": base, "protocol": protocol,
		"message": "连接成功", "reply_preview": preview,
	}
}

// ListLLMModels 在线拉取厂商可用模型列表（表单覆盖值，不必先保存）。
// 尽力而为：不少 Coding/Agent 套餐网关不实现 /models，失败按连接诊断同一套 kind 分类，
// 前端拉不到就回到手输。
func (a *Agent) ListLLMModels(override map[string]any) map[string]any {
	if a.Cfg.LLM.Provider == "mock" && !probeHasExplicitTarget(override) {
		return map[string]any{"ok": true, "kind": "mock", "models": []llm.ModelInfo{}, "message": "离线 Mock 模型没有在线模型列表"}
	}
	base, _, protocol, key := a.llmFormTarget(override)
	if strings.TrimSpace(base) == "" {
		return map[string]any{"ok": false, "kind": "config", "message": "base_url 为空：选择厂商预设或直接填写 API 地址"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	models, err := llm.ListModels(ctx, protocol, base, key, 15*time.Second)
	if err != nil {
		return map[string]any{
			"ok": false, "kind": classifyLLMError(ctx, err),
			"http_status": llm.StatusCode(err), "message": err.Error(),
		}
	}
	return map[string]any{
		"ok": true, "kind": "ok", "count": len(models), "models": models,
		"base_url": base, "protocol": protocol, "message": "模型列表获取成功",
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// classifyLLMError 把调用错误归到诊断类别（前端按类别给中文提示与修复建议）。
func classifyLLMError(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	switch code := llm.StatusCode(err); {
	case code == 401 || code == 403:
		return "auth"
	case code == 404:
		return "not_found"
	case code == 429:
		return "rate_limited"
	case code >= 500:
		return "provider"
	case code > 0:
		return "api"
	default:
		return "network"
	}
}

// ContextView 上下文状态视图（含 token 估算，供设置页展示压缩收益）。
func (a *Agent) ContextView() map[string]any {
	if a.Mem == nil {
		return map[string]any{"enabled": false}
	}
	st := a.Mem.Stats()
	return map[string]any{
		"enabled":          a.Cfg.Agent.ContextCompress,
		"short_turns":      st.ShortTurns,
		"short_cap":        st.ShortCap,
		"overflow":         st.Overflow,
		"summary":          st.Summary,
		"summary_chars":    st.SummaryRunes,
		"est_tokens_saved": st.SavedTokens, // 累计压缩节省（原始溢出与摘要的估算差）
	}
}

// ResetConversation 开始新对话：清空短期对话与滚动摘要上下文（长期记忆保留）。
func (a *Agent) ResetConversation() {
	if a.Mem != nil {
		a.Mem.ResetConversation()
	}
}

// ---------- 云端账号 ----------

// AuthSession 返回当前云端登录状态。
func (a *Agent) AuthSession() map[string]any {
	if a.Auth == nil {
		return map[string]any{"signed_in": false, "configured": false}
	}
	return a.Auth.Session()
}

// AuthConfigure 保存 Supabase 连接配置（可稍后填写，框架先行）。
func (a *Agent) AuthConfigure(supabaseURL, anonKey string) error {
	if a.Auth == nil {
		return errors.New("云端账号模块未启用")
	}
	return a.Auth.Configure(supabaseURL, anonKey)
}

// AuthSignUp 邮箱注册。
func (a *Agent) AuthSignUp(email, password string) error {
	if a.Auth == nil {
		return errors.New("云端账号模块未启用")
	}
	return a.Auth.SignUp(email, password)
}

// AuthSignIn 邮箱登录。
func (a *Agent) AuthSignIn(email, password string) error {
	if a.Auth == nil {
		return errors.New("云端账号模块未启用")
	}
	return a.Auth.SignIn(email, password)
}

// AuthSignOut 登出（仅清本地会话）。
func (a *Agent) AuthSignOut() error {
	if a.Auth == nil {
		return nil
	}
	return a.Auth.SignOut()
}

// AuthOAuth 启动 GitHub/Google 登录，返回需浏览器打开的授权地址。
func (a *Agent) AuthOAuth(provider string) (string, error) {
	if a.Auth == nil {
		return "", errors.New("云端账号模块未启用")
	}
	return a.Auth.StartOAuth(provider)
}

// ---------- 本地数据 ----------

// DataDir 返回本地数据目录（供"我的"展示数据存放在哪）。
func (a *Agent) DataDir() string { return a.Cfg.DataDir }

// CredentialsFile 返回凭证文件路径（展示用）。
func (a *Agent) CredentialsFile() string {
	if a.Creds == nil {
		return filepath.Join(a.Cfg.DataDir, "credentials.json")
	}
	return a.Creds.Path()
}

func (a *Agent) scheduleView(name string) types.ScheduleJobView {
	for _, j := range a.Sched.ListJobs() {
		if j.Name == name {
			return jobToScheduleView(j)
		}
	}
	return types.ScheduleJobView{}
}

func jobToScheduleView(j scheduler.Job) types.ScheduleJobView {
	v := types.ScheduleJobView{
		Name: j.Name, Cron: j.Cron, IntervalSec: j.IntervalSec, Goal: j.Goal,
		Mode: j.Mode, Enabled: j.Enabled, WhenText: j.WhenText, ScheduleText: j.ScheduleText,
	}
	// 印**生效值**而不是原始字段：空串在存储层表示"用默认"，但界面要回答的是
	// "这个任务跑完会不会通知我"——印一个空值等于让人自己去猜默认是什么。
	if policy, err := scheduler.NormalizeNotify(j.Notify); err == nil {
		v.Notify = policy
	} else {
		v.Notify = j.Notify // 脏值如实印出来，别悄悄改成默认
	}
	if j.NextRun != nil {
		v.NextRun = j.NextRun.Format(time.RFC3339)
	}
	if j.LastRun != nil {
		v.LastRun = j.LastRun.Format(time.RFC3339)
	}
	return v
}

// ---------- 厂商预设 / MCP 与技能市场 ----------

// ProvidersView 厂商官方接入预设目录（设置页下拉数据源）。
func (a *Agent) ProvidersView() []llm.ProviderPreset { return llm.Providers }

// MarketMCP 搜索 MCP 市场目录（q 为空返回全部），附带已安装标记。
func (a *Agent) MarketMCP(q string) []map[string]any {
	presets := market.SearchMCP(q)
	out := make([]map[string]any, 0, len(presets))
	for _, p := range presets {
		installed := false
		for _, s := range a.Cfg.MCP {
			if s.Name == p.ID {
				installed = true
				break
			}
		}
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "desc": p.Desc, "command": p.Command,
			"params": p.Params, "trust": p.Trust, "tags": p.Tags, "installed": installed,
		})
	}
	return out
}

// MarketSkills 搜索技能模板目录，附带已安装标记。
func (a *Agent) MarketSkills(q string) []map[string]any {
	presets := market.SearchSkill(q)
	out := make([]map[string]any, 0, len(presets))
	for _, s := range presets {
		_, installed := a.Skills.Get(s.Name)
		out = append(out, map[string]any{
			"name": s.Name, "description": s.Description,
			"params": s.Params, "steps": s.Steps, "tags": s.Tags, "installed": installed,
		})
	}
	return out
}

// SkillInstallPreset 一键安装技能模板（已存在则升级版本）。
func (a *Agent) SkillInstallPreset(name string) (int, error) {
	p, err := market.FindSkill(name)
	if err != nil {
		return 0, err
	}
	return a.Skills.Save(skill.Skill{
		Name: p.Name, Description: p.Description, Params: p.Params, Steps: p.Steps,
	})
}

// MCPList 已配置的 MCP 服务器视图（含连接状态与已注册工具数）。
func (a *Agent) MCPList() []map[string]any {
	out := make([]map[string]any, 0, len(a.Cfg.MCP))
	for _, s := range a.Cfg.MCP {
		_, connected := a.MCP.Get(s.Name)
		tools := 0
		prefix := mcpToolPrefix(s.Name)
		for _, n := range a.Reg.Names() {
			if strings.HasPrefix(n, prefix) {
				tools++
			}
		}
		out = append(out, map[string]any{
			"name": s.Name, "command": s.Command, "args": s.Args,
			"trust": s.Trust, "enabled": s.Enabled,
			"connected": connected, "tools": tools,
		})
	}
	return out
}

// MCPInstallPreset 安装市场预设：参数替换 → 持久化 → 热连接。
func (a *Agent) MCPInstallPreset(id string, params map[string]string, trust string) (map[string]any, error) {
	p, err := market.FindMCP(id)
	if err != nil {
		return nil, err
	}
	command, args, err := market.BuildCommand(p, params)
	if err != nil {
		return nil, err
	}
	if trust == "" {
		trust = p.Trust
	}
	return a.mcpInstall(p.ID, p.Name, command, args, trust)
}

// MCPInstallCustom 自定义安装 MCP 服务器（stdio）。
func (a *Agent) MCPInstallCustom(name, command string, args []string, trust string) (map[string]any, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command 不能为空")
	}
	display := strings.TrimSpace(name)
	if display == "" {
		base := filepath.Base(command)
		if i := strings.IndexByte(base, '.'); i > 0 {
			base = base[:i]
		}
		display = mcpNameRe.ReplaceAllString(base, "-")
		if display == "" || !mcpNameRe.MatchString(display) {
			display = "custom"
		}
	}
	return a.mcpInstall(display, display, command, args, trust)
}

// mcpInstall 公共安装路径：校验 → 持久化 → 热连接（尽力而为，失败仅告警）。
func (a *Agent) mcpInstall(name, display, command string, args []string, trust string) (map[string]any, error) {
	if !mcpNameRe.MatchString(name) {
		return nil, fmt.Errorf("名称 %q 非法（仅限字母数字下划线连字符，≤32 字符）", name)
	}
	for _, s := range a.Cfg.MCP {
		if s.Name == name {
			return nil, fmt.Errorf("MCP 服务器 %q 已安装，请先卸载", name)
		}
	}
	if trust != "readonly" && trust != "user_approved" && trust != "full_access" {
		trust = "user_approved"
	}
	srv := config.MCPServerConfig{Name: name, Command: command, Args: args, Trust: trust, Enabled: true}
	a.Cfg.MCP = append(a.Cfg.MCP, srv)
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return nil, fmt.Errorf("已加入配置但持久化失败: %w", err)
	}
	tools, err := a.mcpConnect(srv)
	res := map[string]any{
		"name": name, "display": display, "installed": true,
		"connected": err == nil, "tools": tools, "mcp": a.MCPList(),
	}
	if err != nil {
		res["warning"] = fmt.Sprintf("已保存配置，但连接失败（首次 npx/uvx 需联网下载，可稍后在工具页查看）: %v", err)
	}
	return res, nil
}

// mcpConnect 连接单个 MCP 服务器并把工具热注册进注册表。
func (a *Agent) mcpConnect(srv config.MCPServerConfig) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mcp.Start(ctx, mcp.ServerConfig{
		Name: srv.Name, Command: srv.Command, Args: srv.Args, Trust: srv.Trust, Enabled: true,
	}, func(f string, xs ...any) { fmt.Fprintf(os.Stderr, "[mcp] "+f+"\n", xs...) })
	if err != nil {
		return 0, err
	}
	perm := mcp.PermissionFromTrust(srv.Trust)
	for _, def := range client.Tools {
		a.Reg.Replace(mcp.NewTool(client, def, perm))
	}
	a.MCP.Add(client)
	return len(client.Tools), nil
}

// MCPRemove 卸载：删配置 + 注销工具 + 关闭连接。
func (a *Agent) MCPRemove(name string) (map[string]any, error) {
	idx := -1
	for i, s := range a.Cfg.MCP {
		if s.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("MCP 服务器 %q 未安装", name)
	}
	a.Cfg.MCP = append(a.Cfg.MCP[:idx], a.Cfg.MCP[idx+1:]...)
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return nil, fmt.Errorf("已移除但持久化失败: %w", err)
	}
	removed := 0
	prefix := mcpToolPrefix(name)
	for _, n := range a.Reg.Names() {
		if strings.HasPrefix(n, prefix) {
			if a.Reg.Unregister(n) {
				removed++
			}
		}
	}
	a.MCP.Remove(name)
	return map[string]any{"name": name, "removed_tools": removed, "mcp": a.MCPList()}, nil
}

// MCPRetry 对已安装但未连接的 MCP 服务器重试连接（首装 npx/uvx 下载超时常scenario）。
func (a *Agent) MCPRetry(name string) (map[string]any, error) {
	var srv *config.MCPServerConfig
	for i := range a.Cfg.MCP {
		if a.Cfg.MCP[i].Name == name {
			srv = &a.Cfg.MCP[i]
			break
		}
	}
	if srv == nil {
		return nil, fmt.Errorf("MCP 服务器 %q 未安装", name)
	}
	a.MCP.Remove(name) // 关闭旧连接（若有）
	tools, err := a.mcpConnect(*srv)
	res := map[string]any{
		"name": name, "connected": err == nil, "tools": tools, "mcp": a.MCPList(),
	}
	if err != nil {
		res["warning"] = fmt.Sprintf("连接仍失败: %v", err)
	}
	return res, nil
}

// permFromStr 权限字符串解析。
func permFromStr(s string) types.Permission {
	switch s {
	case "readonly":
		return types.PermissionReadOnly
	case "full_access":
		return types.PermissionFullAccess
	default:
		return types.PermissionUserApproved
	}
}

// PermissionOf 查询工具的有效权限（含覆盖）。
func (a *Agent) PermissionOf(name string) types.Permission {
	if t, ok := a.Reg.Get(name); ok {
		return a.Gate.EffectivePermission(t)
	}
	return types.PermissionUserApproved
}

// ---------- 工作区（任务文件夹） ----------

// WorkspaceView 当前工作区视图（含最近列表）。
func (a *Agent) WorkspaceView() map[string]any {
	return map[string]any{
		"workspace": a.Cfg.Workspace,
		"recents":   a.Cfg.WorkspaceRecents,
	}
}

// WorkspaceBrowse 浏览文件系统供选择工作区：
// path 为空时返回根列表（Windows 盘符 / Unix 根与主目录），否则列出该目录下的子文件夹。
func (a *Agent) WorkspaceBrowse(path string) (map[string]any, error) {
	path = strings.TrimSpace(path)
	type dirEntry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	}
	if path == "" {
		roots := []dirEntry{}
		if runtime.GOOS == "windows" {
			for c := 'C'; c <= 'Z'; c++ {
				drive := string(c) + `:\`
				if info, err := os.Stat(drive); err == nil && info.IsDir() {
					roots = append(roots, dirEntry{Name: string(c) + " 盘", Path: drive, IsDir: true})
				}
			}
		} else {
			roots = append(roots, dirEntry{Name: "/（系统根）", Path: "/", IsDir: true})
			if home, err := os.UserHomeDir(); err == nil {
				roots = append(roots, dirEntry{Name: "主目录 " + home, Path: home, IsDir: true})
			}
		}
		return map[string]any{"parent": "", "dirs": roots}, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("路径非法: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("目录不存在: %s", abs)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s 不是文件夹", abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	dirs := make([]dirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, dirEntry{Name: e.Name(), Path: filepath.Join(abs, e.Name()), IsDir: true})
		if len(dirs) >= 200 {
			break // 目录过多时截断，防大目录卡顿
		}
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		parent = ""
	}
	return map[string]any{"parent": parent, "current": abs, "dirs": dirs}, nil
}

// WorkspaceSet 设定任务工作区（校验 → 文件工具/门控热切换 → 持久化 → 记最近）。
func (a *Agent) WorkspaceSet(path string) (map[string]any, error) {
	if err := a.applyWorkspace(path); err != nil {
		return nil, err
	}
	return a.WorkspaceView(), nil
}

// applyWorkspace 校验并应用工作区：文件工具边界与门控热更新、持久化覆盖层、记入最近列表。
// 供工作区切换与空间激活/打开对话联动复用。
func (a *Agent) applyWorkspace(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("路径非法: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("目录不存在: %s", abs)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s 不是文件夹", abs)
	}

	a.Cfg.Workspace = abs
	if a.FileTools != nil {
		a.FileTools.Roots = []string{abs} // 文件工具边界热切换（工具持指针，立即生效）
	}
	if a.Gate != nil {
		a.Gate.SetWorkspaceRoots([]string{abs})
	}
	// 最近列表：去重置顶，最多保留 8 个
	recents := []string{abs}
	for _, r := range a.Cfg.WorkspaceRecents {
		if r != abs {
			recents = append(recents, r)
		}
	}
	if len(recents) > 8 {
		recents = recents[:8]
	}
	a.Cfg.WorkspaceRecents = recents

	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return fmt.Errorf("已切换但持久化失败: %w", err)
	}
	return nil
}
