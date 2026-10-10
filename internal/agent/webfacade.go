package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gleam/internal/config"
	"gleam/internal/harness/growth"
	"gleam/internal/harness/safety"
	"gleam/internal/harness/scheduler"
	"gleam/internal/harness/skill"
	"gleam/internal/llm"
	"gleam/internal/market"
	"gleam/internal/nlcron"
	"gleam/internal/tools/mcp"
	"gleam/internal/tools/toolutil"
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
	hits := a.Mem.RelevantScoped(query, k, a.MemoryScope())
	out := make([]types.MemoryHitView, 0, len(hits))
	for _, h := range hits {
		out = append(out, types.MemoryHitView{ID: h.ID, Content: h.Content, Tags: h.Tags, Score: h.Score})
	}
	return out
}

// MemoryCount 长期记忆条目总数（含软删条目，口径与就绪体检里的「长期记忆 N 条」一致）。
// 界面上任何一处"条数"都必须问这里：自己数一遍就会和别处对不上。
func (a *Agent) MemoryCount() int {
	if a.Mem == nil || a.Mem.Long == nil {
		return 0
	}
	return a.Mem.Long.Count()
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
	return a.Mem.RememberScoped(content, "", a.MemoryScope(), tags)
}

// MemoryScope 当前记忆归属的工作区。开了「项目级记忆」才返回工作区路径，否则空串（全局）。
// 读的是**当下**的配置与工作区：切换工作区、开关设置都即时生效，不用重启。
func (a *Agent) MemoryScope() string {
	if a.Cfg == nil || !a.Cfg.Memory.ProjectScope {
		return ""
	}
	return strings.TrimSpace(a.Cfg.Workspace)
}

// SkillList 技能列表。
func (a *Agent) SkillList() []skill.Skill { return a.Skills.List() }

// SkillGet 读取单个技能（含步骤）：外部入口要看内容只能走这里，别直摸 harness。
func (a *Agent) SkillGet(name string) (*skill.Skill, error) { return a.Skills.Get(name) }

// SkillSave 保存技能。首次落库（v1）记一条 skill_created——等级公式里
// TotalSkills 占 20 分，而这个事件此前没有任何生产者：技能那一栏永远是 0，
// 用户攒了一堆技能，等级却一动不动。
func (a *Agent) SkillSave(sk skill.Skill) (int, error) {
	v, err := a.Skills.Save(sk)
	if err == nil && v == 1 {
		a.recordSkillCreated(sk.Name, sk.Description)
	}
	return v, err
}

// recordSkillCreated 记一条技能入库事件（Growth 未启用时静默跳过，与任务日志同一条规则）。
func (a *Agent) recordSkillCreated(name, description string) {
	if a.Growth == nil {
		return
	}
	a.Growth.Record(growth.Entry{Type: "skill_created", SkillName: name, Goal: description})
}

// SkillRun 运行技能。
func (a *Agent) SkillRun(ctx context.Context, name string, params map[string]string) (map[string]any, error) {
	return a.RunSkill(ctx, name, params)
}

// SkillDelete 删除技能。
func (a *Agent) SkillDelete(name string) error { return a.Skills.Delete(name) }

// SkillSetEnabled 启用 / 停用技能：留在库里、保留统计，只是不再进技能清单、不再可运行。
func (a *Agent) SkillSetEnabled(name string, enabled bool) (map[string]any, error) {
	s, err := a.Skills.SetDisabled(name, !enabled)
	if err != nil {
		return nil, err
	}
	list := a.SkillList()
	return map[string]any{
		"name": s.Name, "enabled": !s.Disabled, "version": s.Version,
		"count": len(list), "skills": list,
	}, nil
}

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
	// 多模型时代：api_key_set 看默认模型有没有密钥，不再看旧的全局凭证文件。
	var apiKeySet bool
	var keyHost string
	if def := a.defaultModelEntry(); def != nil {
		apiKeySet = strings.TrimSpace(def.APIKey) != ""
		keyHost = llm.KeyScope(def.BaseURL)
	} else {
		// 没有多模型配置时回退到旧逻辑
		activeKey, kh := a.llmKeyFor(cfg.LLM.BaseURL, "")
		apiKeySet = activeKey != ""
		keyHost = kh
	}
	return map[string]any{
		"persona": map[string]any{"name": cfg.Persona.Name, "style": cfg.Persona.Style},
		"git": map[string]any{
			"branch_prefix":       cfg.Git.BranchPrefix,
			"force_push":          cfg.Git.ForcePush,
			"commit_instructions": cfg.Git.CommitInstructions,
		},
		"worktrees": map[string]any{
			"enabled":             cfg.Worktrees.Enabled,
			"fetch_before_create": cfg.Worktrees.FetchBeforeCreate,
			"auto_delete":         cfg.Worktrees.AutoDelete,
			"max_count":           cfg.Worktrees.MaxCount,
		},
		"network": map[string]any{
			"proxy_mode": cfg.Network.ProxyModeOrDefault(),
			"proxy_url":  cfg.Network.ProxyURL,
		},
		"safety": map[string]any{
			"mode":                     cfg.Safety.Mode,
			"approval_timeout_seconds": cfg.Safety.ApprovalTimeoutSecs,
			"ai_review":                cfg.Safety.AIReview,
			"deep_review":              cfg.Safety.DeepReview,
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
			"project_scope":       cfg.Memory.ProjectScope,
		},
		"llm": map[string]any{
			"provider":         cfg.LLM.Provider,
			"protocol":         cfg.LLM.Protocol,
			"provider_id":      cfg.LLM.ProviderID,
			"plan":             cfg.LLM.Plan,
			"model":            cfg.LLM.Model,
			"fast_model":       cfg.LLM.FastModel,
			"tiers":            tiersView(cfg.LLM.Tiers),
			"base_url":         cfg.LLM.BaseURL,
			"temperature":      cfg.LLM.Temperature,
			"max_tokens":       cfg.LLM.MaxTokens,
			"timeout_seconds":  cfg.LLM.TimeoutSecs,
			"context_window":   cfg.LLM.ContextWindow,
			"api_key_set":      apiKeySet,
			"api_key_host":     keyHost,
			"api_key_host_cur": llm.KeyScope(cfg.LLM.BaseURL),
			"models":           modelsView(cfg.LLM.Models, keyHost),
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

// modelsView 把多模型列表转成前端可消费的形式：密钥掩码，不回传明文。
func modelsView(models []config.ModelEntry, _ string) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, modelEntryView(m))
	}
	return out
}

// modelEntryView 单条模型条目转前端视图（密钥掩码）。
func modelEntryView(m config.ModelEntry) map[string]any {
	keySet := strings.TrimSpace(m.APIKey) != ""
	return map[string]any{
		"id": m.ID, "name": m.Name,
		"provider_id": m.ProviderID, "protocol": m.Protocol,
		"base_url": m.BaseURL, "model": m.Model, "plan": m.Plan,
		"is_default": m.IsDefault, "is_fast": m.IsFast,
		"api_key_set": keySet,
	}
}

// maskAPIKey 密钥掩码：前 4 + **** + 后 2，过短则全遮。
func maskAPIKey(key string) string {
	n := len([]rune(key))
	if n <= 6 {
		return "****"
	}
	runes := []rune(key)
	return string(runes[:4]) + "****" + string(runes[n-2:])
}

// ---------- 多模型 CRUD ----------

// modelEntryFromMap 从前端 JSON 解析一条模型条目（校验必填字段）。
func modelEntryFromMap(m map[string]any) (config.ModelEntry, error) {
	e := config.ModelEntry{}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	e.ID = str("id")
	e.Name = str("name")
	e.ProviderID = str("provider_id")
	e.Protocol = str("protocol")
	e.BaseURL = str("base_url")
	e.Model = str("model")
	e.Plan = str("plan")
	e.APIKey = str("api_key")
	if b, ok := m["is_default"].(bool); ok {
		e.IsDefault = b
	}
	if b, ok := m["is_fast"].(bool); ok {
		e.IsFast = b
	}
	if e.Name == "" {
		return e, fmt.Errorf("显示名不能为空")
	}
	if e.Model == "" {
		return e, fmt.Errorf("模型 ID 不能为空")
	}
	return e, nil
}

// modelIndex 按 ID 查找模型条目在列表中的位置，未找到返回 -1。
func (a *Agent) modelIndex(id string) int {
	for i, m := range a.Cfg.LLM.Models {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// defaultModelEntry 返回标记为默认的模型条目，没有则返回列表第一条。
func (a *Agent) defaultModelEntry() *config.ModelEntry {
	for i := range a.Cfg.LLM.Models {
		if a.Cfg.LLM.Models[i].IsDefault {
			return &a.Cfg.LLM.Models[i]
		}
	}
	if len(a.Cfg.LLM.Models) > 0 {
		return &a.Cfg.LLM.Models[0]
	}
	return nil
}

// ModelsList 返回当前多模型列表（密钥掩码）。
func (a *Agent) ModelsList() []map[string]any {
	return modelsView(a.Cfg.LLM.Models, "")
}

// ModelAdd 添加一条模型配置。ID 为空时自动生成。
func (a *Agent) ModelAdd(raw map[string]any) (map[string]any, error) {
	entry, err := modelEntryFromMap(raw)
	if err != nil {
		return nil, err
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("model-%d", time.Now().UnixNano()%100000)
	}
	if a.modelIndex(entry.ID) >= 0 {
		return nil, fmt.Errorf("模型 ID %q 已存在", entry.ID)
	}
	if entry.IsDefault {
		for i := range a.Cfg.LLM.Models {
			a.Cfg.LLM.Models[i].IsDefault = false
		}
	}
	a.Cfg.LLM.Models = append(a.Cfg.LLM.Models, entry)
	if err := a.saveModelsOverlay(); err != nil {
		return nil, err
	}
	return map[string]any{"model": modelEntryView(entry), "models": a.ModelsList()}, nil
}

// ModelUpdate 修改一条模型配置（按 ID 匹配，字段覆盖）。
func (a *Agent) ModelUpdate(id string, raw map[string]any) (map[string]any, error) {
	idx := a.modelIndex(id)
	if idx < 0 {
		return nil, fmt.Errorf("模型 %q 不存在", id)
	}
	entry, err := modelEntryFromMap(raw)
	if err != nil {
		return nil, err
	}
	entry.ID = id
	// 没带 api_key 字段时保留原密钥（前端编辑对话框留空表示不修改）。
	if _, hasKey := raw["api_key"]; !hasKey {
		entry.APIKey = a.Cfg.LLM.Models[idx].APIKey
	}
	if entry.IsDefault {
		for i := range a.Cfg.LLM.Models {
			a.Cfg.LLM.Models[i].IsDefault = false
		}
	}
	a.Cfg.LLM.Models[idx] = entry
	if err := a.saveModelsOverlay(); err != nil {
		return nil, err
	}
	return map[string]any{"model": modelEntryView(entry), "models": a.ModelsList()}, nil
}

// ModelDelete 删除一条模型配置。
func (a *Agent) ModelDelete(id string) (map[string]any, error) {
	idx := a.modelIndex(id)
	if idx < 0 {
		return nil, fmt.Errorf("模型 %q 不存在", id)
	}
	a.Cfg.LLM.Models = append(a.Cfg.LLM.Models[:idx], a.Cfg.LLM.Models[idx+1:]...)
	if err := a.saveModelsOverlay(); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": id, "models": a.ModelsList()}, nil
}

// ModelSetDefault 把某条模型设为新对话默认。
func (a *Agent) ModelSetDefault(id string) (map[string]any, error) {
	idx := a.modelIndex(id)
	if idx < 0 {
		return nil, fmt.Errorf("模型 %q 不存在", id)
	}
	for i := range a.Cfg.LLM.Models {
		a.Cfg.LLM.Models[i].IsDefault = (i == idx)
	}
	if err := a.saveModelsOverlay(); err != nil {
		return nil, err
	}
	return map[string]any{"model": modelEntryView(a.Cfg.LLM.Models[idx]), "models": a.ModelsList()}, nil
}

// SetActiveModel 切换当前对话使用的模型（运行时热生效，不改配置列表）。
func (a *Agent) SetActiveModel(id string) (map[string]any, error) {
	idx := a.modelIndex(id)
	if idx < 0 {
		return nil, fmt.Errorf("模型 %q 不存在", id)
	}
	entry := a.Cfg.LLM.Models[idx]
	if err := a.applyModelEntry(entry); err != nil {
		return nil, err
	}
	return map[string]any{"active": modelEntryView(entry)}, nil
}

// ActiveModel 返回当前激活的模型条目视图（对话页下拉框高亮用）。
func (a *Agent) ActiveModel() map[string]any {
	for _, m := range a.Cfg.LLM.Models {
		if m.IsDefault {
			return modelEntryView(m)
		}
	}
	return nil
}

// applyModelEntry 用一条 ModelEntry 重建主模型客户端（运行时热切换）。
func (a *Agent) applyModelEntry(entry config.ModelEntry) error {
	if a.Cfg.LLM.Provider == "mock" {
		return nil
	}
	baseURL, model, protocol := llm.ResolveTarget(entry.ProviderID, entry.Plan, entry.BaseURL, entry.Model, entry.Protocol)
	key := strings.TrimSpace(entry.APIKey)
	if key == "" {
		key, _ = a.llmKeyFor(baseURL, "")
	}
	a.LLM = llm.New(protocol, baseURL, key, model,
		a.Cfg.LLM.Temperature, a.Cfg.LLM.MaxTokens, a.Cfg.LLM.TimeoutSecs)
	return nil
}

// saveModelsOverlay 把多模型列表持久化到覆盖层（重启后仍生效）。
func (a *Agent) saveModelsOverlay() error {
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return fmt.Errorf("已生效但保存失败: %w", err)
	}
	return nil
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
	asFloat := func(v any) (float64, bool) {
		switch n := v.(type) {
		case float64:
			return n, true
		case int:
			return float64(n), true
		case int64:
			return float64(n), true
		}
		return 0, false
	}
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
		if b, ok := sm["deep_review"].(bool); ok {
			sub("safety")["deep_review"] = b
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
		if b, ok := mm["project_scope"].(bool); ok {
			sub("memory")["project_scope"] = b
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
		// 温度 0 是合法取值（确定性输出），旧的 v > 0 判断把用户输入的 0 静默丢回默认值。
		// 越界与 posInt 同语义：整次拒绝并说明范围，不静默。
		if v, ok := asFloat(lm["temperature"]); ok {
			if v < 0 || v > 2 {
				outOfRange = append(outOfRange, fmt.Sprintf("temperature=%g（允许 0–2）", v))
			} else {
				sub("llm")["temperature"] = v
			}
		}
		if v, ok := lm["max_tokens"]; ok {
			posInt("llm", "max_tokens", v, 64, 1000000)
		}
		if v, ok := lm["timeout_seconds"]; ok {
			posInt("llm", "timeout_seconds", v, 3, 600)
		}
		// 上下文窗口：0 = 用内置兜底表（默认这条路），手填时才要求落在一个像样的范围里。
		// 下界 4k：比这更小的窗口是配错了，不是一个可用配置。
		if raw, ok := lm["context_window"]; ok {
			zeroOr("llm", "context_window", raw, 4096, 10000000)
		}
	}
	if v, ok := patch["scheduler"].(map[string]any); ok {
		if b, ok := v["enabled"].(bool); ok {
			sub("scheduler")["enabled"] = b
		}
	}
	if v, ok := patch["git"].(map[string]any); ok {
		if s2, ok := v["branch_prefix"].(string); ok {
			sub("git")["branch_prefix"] = strings.TrimSpace(s2)
		}
		if b, ok := v["force_push"].(bool); ok {
			sub("git")["force_push"] = b
		}
		if s2, ok := v["commit_instructions"].(string); ok {
			sub("git")["commit_instructions"] = strings.TrimSpace(s2)
		}
	}
	if v, ok := patch["worktrees"].(map[string]any); ok {
		for _, k := range []string{"enabled", "fetch_before_create", "auto_delete"} {
			if b, ok := v[k].(bool); ok {
				sub("worktrees")[k] = b
			}
		}
		// 上限 0 = 不限制（默认）。给下限 1，是因为"上限 0"和"上限 1"是两件事：
		// 前者是关掉裁剪，后者是"只留一个"，混起来读的人会把关掉当成只剩一个。
		if raw, ok := v["max_count"]; ok {
			zeroOr("worktrees", "max_count", raw, 1, 100)
		}
	}
	if v, ok := patch["network"].(map[string]any); ok {
		if mode, ok := v["proxy_mode"].(string); ok {
			switch mode {
			case "system", "manual", "none":
				sub("network")["proxy_mode"] = mode
			default:
				return a.SettingsView(), fmt.Errorf("代理方式只能是 system / manual / none，收到 %q", mode)
			}
		}
		if raw, ok := v["proxy_url"].(string); ok {
			sub("network")["proxy_url"] = strings.TrimSpace(raw)
		}
		// 手动模式当场校验一次地址：保存成功却在启动时才发现填错了，等于把错误拖到下一次重启。
		// 这里顺带把传输层换掉，之后新建的客户端就用新的；已经有连接的不会切，界面里写明了要重启。
		if m := sub("network"); m["proxy_mode"] == "manual" {
			raw, _ := m["proxy_url"].(string)
			if err := llm.SetProxyMode("manual", raw); err != nil {
				return a.SettingsView(), err
			}
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

	// ---------- 密钥：先定"这次往哪台主机发哪把 key"，再用它重建客户端 ----------
	// 顺序不能反：重建客户端用的就是这个值。原先是先重建、后落盘，于是换了厂商的
	// 那次保存，新客户端里装的还是上一家的密钥（而且界面上显示"已设置"）。
	if lm, ok := sanitized["llm"].(map[string]any); ok {
		formKey, _ := lm["api_key"].(string)
		clearKey, _ := lm["clear_api_key"].(bool)
		bindBase, _, _, _ := a.llmFormTarget(lm)
		if _, err := BindLLMKey(a.Cfg, a.Creds, bindBase, formKey, clearKey); err != nil {
			return a.SettingsView(), err
		}
	}

	// 运行时热生效：LLM 客户端按协议工厂重建（mock 保持不变），安全门控就地更新
	if a.Cfg.LLM.Provider != "mock" {
		if _, hasLLM := sanitized["llm"]; hasLLM {
			baseURL, model, protocol := llm.ResolveTarget(a.Cfg.LLM.ProviderID, a.Cfg.LLM.Plan, a.Cfg.LLM.BaseURL, a.Cfg.LLM.Model, a.Cfg.LLM.Protocol)
			a.Cfg.LLM.BaseURL, a.Cfg.LLM.Model, a.Cfg.LLM.Protocol = baseURL, model, protocol
			// 上面 BindLLMKey 已把"可发往这台主机的密钥"写进 Cfg.LLM.APIKey（拿不到就是空串）
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
	base, model, protocol = llm.ResolveTarget(pid, plan, base, model, protocol)
	// 密钥按"这次要发往的主机"取：表单没填就用该主机的已存密钥，别家的一概不发。
	// 这里以前是 `firstNonEmpty(str("api_key"), a.Cfg.LLM.APIKey)`，等于把上一家厂商的
	// 凭证发给用户正在预验证的新端点。
	key, _ = a.llmKeyFor(base, str("api_key"))
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
// override 可带 protocol/provider_id/plan/base_url/model/api_key/model_id；
// api_key 留空表示用已保存的；model_id 用于编辑时查找现有模型的密钥。
func (a *Agent) TestLLMConnection(override map[string]any) map[string]any {
	if a.Cfg.LLM.Provider == "mock" && !probeHasExplicitTarget(override) {
		return map[string]any{"ok": true, "kind": "mock", "message": "当前为离线 Mock 模型，未发起真实网络调用"}
	}
	// 如果提供了 model_id 且没带 api_key，尝试从现有模型中获取密钥
	if _, hasKey := override["api_key"]; !hasKey {
		if mid, ok := override["model_id"].(string); ok && mid != "" {
			idx := a.modelIndex(mid)
			if idx >= 0 && strings.TrimSpace(a.Cfg.LLM.Models[idx].APIKey) != "" {
				override["api_key"] = a.Cfg.LLM.Models[idx].APIKey
			}
		}
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
	// 如果提供了 model_id 且没带 api_key，尝试从现有模型中获取密钥
	if _, hasKey := override["api_key"]; !hasKey {
		if mid, ok := override["model_id"].(string); ok && mid != "" {
			idx := a.modelIndex(mid)
			if idx >= 0 && strings.TrimSpace(a.Cfg.LLM.Models[idx].APIKey) != "" {
				override["api_key"] = a.Cfg.LLM.Models[idx].APIKey
			}
		}
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
	rd := a.windowReading()
	return map[string]any{
		"enabled":       a.Cfg.Agent.ContextCompress,
		"short_turns":   st.ShortTurns,
		"short_cap":     st.ShortCap,
		"fill_pct":      st.FillPct, // 轮数口径：短期窗口攒了多少轮（上限是配置的轮数容量，与模型无关）
		"overflow":      st.Overflow,
		"summary":       st.Summary,
		"summary_chars": st.SummaryRunes,
		// est_tokens_saved 累计压缩节省（原始溢出与摘要的估算差）
		"est_tokens_saved": st.SavedTokens,
		// token 口径：最近一次请求的输入占模型上下文窗口的几成（见 ctxwindow.go）。
		// 两套口径都给：轮数回答"攒了多少"，窗口回答"下一轮还能塞多少"，不是一回事。
		"prompt_tokens":     rd.LastTokens,
		"prompt_estimated":  rd.Estimated,
		"window_tokens":     rd.Window,
		"window_source":     rd.Source,
		"window_note":       rd.Note,
		"window_pct":        rd.Pct,
		"carry_turns":       rd.CarryTurns,
		"compress_end_pct":  compressAtEndPct,
		"compress_mid_pct":  compressMidPct,
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

// mcpIndex 按名字找已配置的 MCP 条目，未安装返回 -1。
//
// 「名字 → 条目」这件事在装/卸/启停/重连五处都要问，各自写一遍找不到的分支
// 就会漂（比如某处忘了跳过同名，另一处没跳过）。
func (a *Agent) mcpIndex(name string) int {
	for i, s := range a.Cfg.MCP {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// MCPInstalledByArgs 远端目录用的「装过没」判据：看已配置服务器的参数里有没有这个包引用。
//
// 为什么不比整个 argv：装的时候可能按选中的 npm 源加过 `--registry=…`，
// 逐字比较会在"换了源之后"把装好的服务器判成没装。包引用是那个稳定的身份。
func (a *Agent) MCPInstalledByArgs(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, s := range a.Cfg.MCP {
		for _, arg := range s.Args {
			if arg == ref || strings.Contains(arg, ref) {
				return true
			}
		}
	}
	return false
}

// MarketMCP 搜索 MCP 市场目录（q 为空返回全部），附带已安装标记。
func (a *Agent) MarketMCP(q string) []map[string]any {
	presets := market.SearchMCP(q)
	out := make([]map[string]any, 0, len(presets))
	for _, p := range presets {
		out = append(out, map[string]any{
			"id": p.ID, "name": p.Name, "desc": p.Desc, "command": p.Command,
			"params": p.Params, "trust": p.Trust, "tags": p.Tags,
			"installed": a.mcpIndex(p.ID) >= 0,
		})
	}
	return out
}

// MarketSkills 搜索技能模板目录，附带已安装标记。
func (a *Agent) MarketSkills(q string) []map[string]any {
	presets := market.SearchSkill(q)
	out := make([]map[string]any, 0, len(presets))
	for _, s := range presets {
		_, err := a.Skills.Get(s.Name)
		out = append(out, map[string]any{
			"name": s.Name, "description": s.Description, "category": s.Category,
			"params": s.Params, "steps": s.Steps, "tags": s.Tags, "installed": err == nil,
		})
	}
	return out
}

// SkillInstallPreset 一键安装技能模板。force=false 且已存在 → ErrAlreadyInstalled：
// 技能可能已被用户改过（固化、自动优化都写同一个文件），市场版本不该静默盖掉它。
func (a *Agent) SkillInstallPreset(name string, force bool) (int, error) {
	p, err := market.FindSkill(name)
	if err != nil {
		return 0, err
	}
	// Get 的第二个返回值是 error，不能当「装没装」的布尔用（error 序列化成 {} 会恒真）
	_, existed := a.Skills.Get(p.Name)
	if existed == nil && !force {
		return 0, fmt.Errorf("%w：重装会把它恢复成市场里的模板，你改过的步骤会被替换；只想临时别用它，请改用「停用」", ErrAlreadyInstalled)
	}
	// 走 SkillSave 而不是 Skills.Save：入库事件只有一个出口，第二条路忘了记就是统计漏项
	return a.SkillSave(skill.Skill{
		Name: p.Name, Description: p.Description, Params: p.Params, Steps: p.Steps,
	})
}

// MCPList 已配置的 MCP 服务器视图（含连接状态与已注册工具数）。
func (a *Agent) MCPList() []map[string]any {
	out := make([]map[string]any, 0, len(a.Cfg.MCP))
	names := a.Reg.Names()
	for _, s := range a.Cfg.MCP {
		_, connected := a.MCP.Get(s.Name)
		tools := 0
		prefix := mcpToolPrefix(s.Name)
		for _, n := range names {
			if strings.HasPrefix(n, prefix) {
				tools++
			}
		}
		kind := "stdio"
		if strings.TrimSpace(s.URL) != "" {
			kind = "http"
		}
		out = append(out, map[string]any{
			"name": s.Name, "command": s.Command, "args": s.Args,
			"trust": s.Trust, "enabled": s.Enabled,
			"connected": connected, "tools": tools,
			"kind": kind, "url": s.URL,
			// 只报"有几个请求头"，不回吐值：里面是凭据
			"header_count": len(s.Headers),
		})
	}
	return out
}

// MCPInstallPreset 安装市场预设：参数替换 → 持久化 → 热连接。
// force=true 表示「重装」：覆盖同名条目的命令与参数（UI 必须先二次确认）。
func (a *Agent) MCPInstallPreset(id string, params map[string]string, trust string, force bool) (map[string]any, error) {
	p, err := market.FindMCP(id)
	if err != nil {
		return nil, err
	}
	argVals, envVals := market.SplitParams(p.Params, params)
	command, args, err := market.BuildCommand(p, argVals)
	if err != nil {
		return nil, err
	}
	env, err := market.BuildEnv(p, envVals)
	if err != nil {
		return nil, err
	}
	if trust == "" {
		trust = p.Trust
	}
	return a.mcpInstall(mcpSpec{Name: p.ID, Display: p.Name, Command: command, Args: args, Env: env, Trust: trust}, force)
}

// MCPInstallCustom 自定义安装 MCP 服务器（stdio）。
func (a *Agent) MCPInstallCustom(name, command string, args []string, env map[string]string, trust string, force bool) (map[string]any, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("command 不能为空")
	}
	display := strings.TrimSpace(name)
	if display == "" {
		base := filepath.Base(command)
		if i := strings.LastIndexByte(base, '.'); i > 0 {
			base = base[:i] // 去掉 .exe / .cmd，名字更有辨识度，也少一次字符替换
		}
		display = sanitizeMCPName(base)
		if display == "" {
			display = "custom"
		}
	}
	return a.mcpInstall(mcpSpec{Name: display, Display: display, Command: command, Args: args, Env: env, Trust: trust}, force)
}

// MCPInstallRemote 装一台远端（streamable-http）MCP 服务器：不起进程，只记地址与请求头。
//
// 与 stdio 那条路共用 mcpInstall：落盘、热连接、失败回 warning 的行为完全一致——
// 两条路各写一套"安装流程"就会开始漂，而漂的方向是远端那套少一个回滚。
func (a *Agent) MCPInstallRemote(name, url string, headers map[string]string, trust string, force bool) (map[string]any, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("远端地址为空")
	}
	return a.mcpInstall(mcpSpec{Name: name, Display: name, URL: url, Headers: headers, Trust: trust}, force)
}

// mcpNameUnsafe 名称里必须换掉的字符，与 mcpNameRe 互补。
var mcpNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_\-]+`)

// sanitizeMCPName 把任意命令名收敛成合法服务器名——它要做工具名的中段（mcp.<name>.<tool>）。
//
// 为什么不能写成 mcpNameRe.ReplaceAllString(base, "-")：那个正则匹配的是**合法**字符，
// 替换它等于把 "npx" 变成 "---"——两台不同目录下的 npx 会撞成同一个名字，第二台永远装不上。
// 要替换的是**非法**字符。
func sanitizeMCPName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(mcpNameUnsafe.ReplaceAllString(s, "-"), "-")
	if len(s) > 32 { // 按字节截：字符集已是 ASCII
		s = strings.Trim(s[:32], "-")
	}
	return s
}

// ErrAlreadyInstalled 重名未确认覆盖（技能与 MCP 共用）。单独一个哨兵错误，是为了接入层
// 能回 **409** 而不是笼统的 400：前端拿到 409 才知道该弹「已安装，要重装吗」，
// 拿到 400 只能把后端文案原样糊在屏幕上——用户看到的是一句报错，而不是一个选择。
var ErrAlreadyInstalled = errors.New("已经安装过了")

// mcpInstall 公共安装路径：校验 → 落盘 → 热连接（连接失败仅告警，配置照留）。
//
// **为什么先落盘再改内存**：原顺序是「追加到内存 → 存盘」，存盘失败时内存里已经多了
// 一个条目——界面显示"已安装"，重启就消失，而且因为重名检查挡着，用户连重装都做不了，
// 只能重启进程。落盘失败就把切片整个还原，让用户看到"没装上"这个真实结果。
// mcpSpec 一次安装要落进配置的全部事实。
//
// 为什么收成一个结构体：参数已经到第九个（name/display/command/args/env/url/headers/trust/force），
// 再往上加位置参数，调用点就开始靠"第几个"来读——那种代码改一次错一次。
type mcpSpec struct {
	Name    string
	Display string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	Trust   string
}

func (a *Agent) mcpInstall(spec mcpSpec, force bool) (map[string]any, error) {
	name, display, command, args := spec.Name, spec.Display, spec.Command, spec.Args
	if !mcpNameRe.MatchString(name) {
		return nil, fmt.Errorf("名称 %q 非法（仅限字母数字下划线连字符，≤32 字符）", name)
	}
	// 两种形态二选一：本机进程要有命令，远端要有 URL
	if strings.TrimSpace(spec.URL) == "" && strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("启动命令不能为空")
	}
	idx := a.mcpIndex(name)
	if idx >= 0 && !force {
		return nil, fmt.Errorf("%w：重装会替换当前的命令与参数；只想临时别跑它，请改用「停用」", ErrAlreadyInstalled)
	}
	trust := spec.Trust
	if trust != "readonly" && trust != "user_approved" && trust != "full_access" {
		trust = "user_approved"
	}
	srv := config.MCPServerConfig{
		Name: name, Command: command, Args: args, Env: spec.Env,
		URL: spec.URL, Headers: spec.Headers, Trust: trust, Enabled: true,
	}
	next := append([]config.MCPServerConfig{}, a.Cfg.MCP...)
	if idx >= 0 {
		next[idx] = srv
		a.mcpDetach(name) // 旧连接与旧工具先摘掉，避免新旧两套工具同时挂在注册表上
	} else {
		next = append(next, srv)
	}
	prev := a.Cfg.MCP
	a.Cfg.MCP = next
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		a.Cfg.MCP = prev
		return nil, fmt.Errorf("配置未保存（%v），已还原为安装前的状态", err)
	}
	tools, err := a.mcpConnect(srv)
	res := map[string]any{
		"name": name, "display": display, "installed": true, "replaced": idx >= 0,
		"connected": err == nil, "tools": tools, "mcp": a.MCPList(),
	}
	if err != nil {
		// 两种形态的失败原因完全不同，提示不能共用一句
		hint := "首次 npx/uvx 需联网下载，可稍后在工具页查看"
		if strings.TrimSpace(srv.URL) != "" {
			hint = "远端地址、网络或请求头里的凭据要再核一下"
		}
		res["warning"] = fmt.Sprintf("已保存配置，但连接失败（%s）: %v", hint, err)
	}
	return res, nil
}

// mcpDetach 摘掉某个 MCP 服务器的注册工具与进程连接（不动配置）。
//
// 停用一个服务器和卸载一个服务器要摘的是同一批东西，所以只留这一份实现——
// 两处各写一遍迟早会漏一项（漏掉关连接就是留着子进程在后台跑）。
func (a *Agent) mcpDetach(name string) int {
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
	return removed
}

// MCPSetEnabled 启用 / 停用 MCP 服务器：状态落盘，停用立刻摘工具并关进程，启用立刻重连。
//
// 为什么要有"停用"而不是只有"卸载"：调试一个连不上的服务器时，用户要的是先让它别挡路、
// 保留参数回头再看。只有卸载的话，参数就没了（定时任务的暂停/恢复早就证明过这一点）。
func (a *Agent) MCPSetEnabled(name string, enabled bool) (map[string]any, error) {
	idx := a.mcpIndex(name)
	if idx < 0 {
		return nil, fmt.Errorf("MCP 服务器 %q 未安装", name)
	}
	if a.Cfg.MCP[idx].Enabled == enabled {
		return map[string]any{"name": name, "enabled": enabled, "unchanged": true, "mcp": a.MCPList()}, nil
	}
	prev := a.Cfg.MCP[idx].Enabled
	a.Cfg.MCP[idx].Enabled = enabled
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		a.Cfg.MCP[idx].Enabled = prev
		return nil, fmt.Errorf("状态未保存（%v），已还原", err)
	}
	tools := 0
	a.mcpDetach(name)
	warning := ""
	if enabled {
		n, err := a.mcpConnect(a.Cfg.MCP[idx])
		tools = n
		if err != nil {
			warning = fmt.Sprintf("已启用，但连接失败: %v", err)
		}
	}
	res := map[string]any{
		"name": name, "enabled": enabled, "connected": enabled && warning == "",
		"tools": tools, "mcp": a.MCPList(),
	}
	if warning != "" {
		res["warning"] = warning
	}
	return res, nil
}

// mcpConnect 连接单个 MCP 服务器并把工具热注册进注册表。
func (a *Agent) mcpConnect(srv config.MCPServerConfig) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mcp.Start(ctx, mcp.ServerConfig{
		Name: srv.Name, Command: srv.Command, Args: srv.Args, Env: srv.Env,
		URL: srv.URL, Headers: srv.Headers,
		Trust: srv.Trust, Enabled: true,
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
	idx := a.mcpIndex(name)
	if idx < 0 {
		return nil, fmt.Errorf("MCP 服务器 %q 未安装", name)
	}
	prev := append([]config.MCPServerConfig{}, a.Cfg.MCP...)
	a.Cfg.MCP = append(a.Cfg.MCP[:idx], a.Cfg.MCP[idx+1:]...)
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		// 回滚要拿真正的副本：删元素是就地搬移，只留旧切片头会把被搬走的那条读回来。
		a.Cfg.MCP = prev
		return nil, fmt.Errorf("已移除但持久化失败（重启后它会回来）: %w", err)
	}
	removed := a.mcpDetach(name)
	return map[string]any{"name": name, "removed_tools": removed, "mcp": a.MCPList()}, nil
}

// MCPRetry 对已安装但未连接的 MCP 服务器重试连接（首装 npx/uvx 下载超时是常见场景）。
func (a *Agent) MCPRetry(name string) (map[string]any, error) {
	idx := a.mcpIndex(name)
	if idx < 0 {
		return nil, fmt.Errorf("MCP 服务器 %q 未安装", name)
	}
	srv := &a.Cfg.MCP[idx]
	if !srv.Enabled {
		// 停用的服务器不允许"重连"：那会让一个用户已经判定别跑的东西重新挂上工具表。
		return nil, fmt.Errorf("MCP 服务器 %q 已停用，请先启用", name)
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

// WorkspaceView 当前工作区视图（含最近列表与 Git 分支）。
func (a *Agent) WorkspaceView() map[string]any {
	return map[string]any{
		"workspace":  a.Cfg.Workspace,
		"recents":    a.Cfg.WorkspaceRecents,
		"git_branch": gitBranch(a.Cfg.Workspace),
	}
}

// gitBranch 读取工作区当前 Git 分支名，供界面在工作区旁边亮一个分支标签。
// 只读、超时两秒；不是 Git 仓库、没有 git 命令或处于游离 HEAD 时都返回空串——
// 这些都不是错误，静默跳过，不要因为「这里没有仓库」把工作区选择卡住。
func gitBranch(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" { // 游离 HEAD 没有分支名
		return ""
	}
	return branch
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

// WorkspaceClear 回到「不指定工作区」：文件工具与门控都不再绑定根目录，
// 文件操作失去工作区边界（仍受安全门控逐条约束）。最近列表保留，方便再切回来。
func (a *Agent) WorkspaceClear() (map[string]any, error) {
	a.Cfg.Workspace = ""
	if a.FileTools != nil {
		a.FileTools.Roots = nil
	}
	if a.Gate != nil {
		a.Gate.SetWorkspaceRoots(nil)
	}
	if err := a.Cfg.SaveOverlay(filepath.Join(a.Cfg.DataDir, config.OverlayFile)); err != nil {
		return nil, fmt.Errorf("已取消但持久化失败: %w", err)
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

// ---------- 改动清单：对比与还原 ----------

// 本段是批次 F14 的出口：界面上的「本次改动」只有这两条动作——看某条路径的 diff、
// 把它退回到任务开始前。**引擎不主动改文件**，这里所有写盘都由用户点一下触发。

// changeRef 一次「哪次任务 + 哪个路径」的定位结果。
type changeRef struct {
	Path string     // 规范化后的绝对路径（后面所有 OS 调用都用它，不用客户端原串）
	Head preImage   // 该路径在本任务里的**第一条**写前快照，还原以它为准
	Recs []preImage // 该任务的全部快照，成对还原（file.move）要用
}

// workspaceRoots 当前工作区边界。文件工具那份是热切换的活口径，
// 配置里的 Workspace 兜底（工具未注册时，例如还没跑到需要文件工具的那一步）。
func (a *Agent) workspaceRoots() []string {
	if a.FileTools != nil && len(a.FileTools.Roots) > 0 {
		return a.FileTools.Roots
	}
	if strings.TrimSpace(a.Cfg.Workspace) != "" {
		return []string{a.Cfg.Workspace}
	}
	return nil
}

// changeTarget 校验这对参数并把路径定下来。**两道校验缺一不可**：
//
//   - **在不在本任务的清单里**：不在就不许动。少了这一道，"还原"就成了一个
//     "填任意路径即可覆盖任意文件"的接口；
//   - **在不在工作区边界里**：清单记的是任务当时解析出的绝对路径，而用户可能已经把
//     工作区切到别处，也可能当初那一步的口径与现在不同。边界以**现在**的为准。
//     在副本里跑过的任务是个例外：它的清单记的是副本里的路径，所以**该任务自己的副本根**
//     也算允许边界——只加这一个任务的，不是把所有副本一起放开。
//
// 客户端传来的路径串一律重新解析，绝不拿清单里的原文去拼 OS 调用——清单是磁盘上的
// 文件，读它的时候它已经不完全归我们管了。
func (a *Agent) changeTarget(taskID, rawPath string) (changeRef, error) {
	if SafeTaskName(taskID) == "" {
		return changeRef{}, fmt.Errorf("任务标识 %q 不合法", types.Shorten(taskID, 40))
	}
	if strings.TrimSpace(a.Cfg.DataDir) == "" {
		return changeRef{}, fmt.Errorf("未配置数据目录，写前快照无处可存，也就无从还原")
	}
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return changeRef{}, fmt.Errorf("没有指定路径")
	}
	if !filepath.IsAbs(path) {
		// 刻意不按工作区根去猜相对路径：界面上给的就是清单里的绝对路径，
		// 会走到这里的只有手改请求的一种人，那种时候"猜"比"拒"更坏。
		return changeRef{}, fmt.Errorf("请填写清单里的完整路径")
	}
	// 副本已经被删掉的任务：清单里的路径指向一个不存在的地方。这时说"超出工作区范围"
	// 是把人往错的方向指——真实原因是那份副本已经不在了，而这一点用户能从别处补救
	// （任务分支还在，去 Worktrees 那一页或直接用 git 工具）。
	if m := a.worktreeManager(); m != nil {
		if wtDir := m.Dir(taskID); toolutil.Within(wtDir, path) {
			if _, err := os.Stat(wtDir); os.IsNotExist(err) {
				return changeRef{}, fmt.Errorf("这个任务的副本（%s）已经被删掉了：副本里的改动无从还原。任务分支还在，可用 git 工具查看或合并", wtDir)
			}
		}
	}
	roots := a.workspaceRoots()
	if m := a.worktreeManager(); m != nil {
		if meta, ok := m.Get(taskID); ok {
			roots = append(append([]string(nil), roots...), meta.Path)
		}
	}
	if len(roots) == 0 {
		return changeRef{}, fmt.Errorf("尚未设定工作区，无法判断这个路径能不能动")
	}
	target, err := toolutil.ResolveInRoots(path, roots)
	if err != nil {
		return changeRef{}, err
	}
	recs, err := taskPreImages(a.Cfg.DataDir, taskID)
	if err != nil {
		return changeRef{}, err
	}
	if len(recs) == 0 {
		return changeRef{}, fmt.Errorf("这次任务没有留下写前快照（没动文件，或当时未配置数据目录）")
	}
	head, ok := firstPreImageOf(recs, target)
	if !ok {
		return changeRef{}, fmt.Errorf("这个路径不在本次任务的改动清单里，不能动")
	}
	return changeRef{Path: target, Head: head, Recs: recs}, nil
}

// diffReadMax 参与对比的**现在**这份内容的体量上限，与单文件快照上限同值：
// 一边留不住，另一边算下去也没有可比的前后，两边用同一个数才不会错开。
const diffReadMax = snapshotMaxBytes

// TaskDiff 一条路径的「写前 → 现在」行级对比。
func (a *Agent) TaskDiff(taskID, path string) (map[string]any, error) {
	ref, err := a.changeTarget(taskID, path)
	if err != nil {
		return nil, err
	}
	head := ref.Head
	view := map[string]any{
		"task_id":    taskID,
		"path":       ref.Path,
		"reversible": head.reversible(),
	}
	switch {
	case head.IsDir:
		return nil, fmt.Errorf("这是个目录，没有内容可对比")
	case head.Exists && head.File == "":
		return nil, fmt.Errorf("写前内容未留存，无从对比：%s", diffBlame(head))
	}

	var before string
	if head.Exists {
		b, err := preImageContent(a.Cfg.DataDir, taskID, head)
		if err != nil {
			return nil, fmt.Errorf("读写前内容失败: %w", err)
		}
		before = string(b)
	}
	after, afterBytes, note := readForDiff(ref.Path)
	view["before_bytes"] = head.Bytes
	view["after_bytes"] = afterBytes
	if note != "" {
		view["diff"] = DiffResult{Truncated: true, Note: note}
		return view, nil
	}
	view["diff"] = DiffText(before, after)
	return view, nil
}

// readForDiff 读「现在」这一份。第二个返回值是超限或读不到时的说明（对比照给，
// 但只给体量，不给一份看着完整其实缺了半截的 diff）。
func readForDiff(path string) (text string, bytes int64, note string) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, "" // 现在不存在=全删（读不出内容就是空，不是"读失败"）
	}
	if info.IsDir() {
		return "", info.Size(), "现在这里是个目录，没有内容可对比"
	}
	if info.Size() > diffReadMax {
		return "", info.Size(), fmt.Sprintf("现在有 %s，超过对比上限，只给出体量对比", humanSize(info.Size()))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", info.Size(), "现在这份读不出来：" + types.Shorten(err.Error(), 80)
	}
	return string(b), info.Size(), ""
}

// diffBlame 内容没留住时的那句原因（快照里已经写好，缺一条兜底）。
func diffBlame(rec preImage) string {
	if strings.TrimSpace(rec.Reason) != "" {
		return rec.Reason
	}
	return "写前内容未留存"
}

// TaskRevert 把一条路径退回到本任务开始前。**只由用户点击触发**，不做任何自动还原。
//
// `file.move` 按组成对退（见 preImageGroup）：一次移动在清单里是两行，
// 只退其中一行等于把文件留在半路上。
func (a *Agent) TaskRevert(taskID, path string) (map[string]any, error) {
	ref, err := a.changeTarget(taskID, path)
	if err != nil {
		return nil, err
	}
	group := preImageGroup(ref.Recs, ref.Path)
	if len(group) == 0 { // changeTarget 已经确认过路径在清单里，这里只是不让下面空跑
		return nil, fmt.Errorf("这个路径不在本次任务的改动清单里，不能动")
	}

	type revertedRow struct {
		Path string `json:"path"`
		Note string `json:"note"`
	}
	done := make([]revertedRow, 0, len(group))
	donePaths := make([]string, 0, len(group))
	failed := make([]string, 0, len(group))
	for _, rec := range group {
		if !rec.reversible() {
			failed = append(failed, fmt.Sprintf("%s：%s", filepath.Base(rec.Path), diffBlame(rec)))
			continue
		}
		note, err := restorePreImage(a.Cfg.DataDir, taskID, rec)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s：%s", filepath.Base(rec.Path), types.Shorten(err.Error(), 120)))
			continue
		}
		done = append(done, revertedRow{Path: rec.Path, Note: note})
		donePaths = append(donePaths, rec.Path)
	}
	if len(done) == 0 {
		return nil, fmt.Errorf("没有还原成功任何路径：%s", strings.Join(failed, "；"))
	}

	// 归档里那几行要说实话：动了就是动了（Kind 是历史），但盘上已经不是那样了。
	a.markChangesReverted(taskID, donePaths)
	if a.Gate != nil {
		a.Gate.Record(safety.AuditEntry{
			Tool:   "file.revert",
			Risk:   "medium",
			Action: "manual_revert",
			Reason: "用户在改动清单里点了还原",
			// Detail 只记路径与条数，不记内容：审计自己不能变成新的泄露面。
			Detail: fmt.Sprintf("%d 个路径：%s", len(done), strings.Join(donePaths, ", ")),
		})
	}
	return map[string]any{
		"task_id":  taskID,
		"reverted": done,
		"failed":   failed,
		"pairs":    len(group) > 1, // 一次移动成对退掉，界面据此说"两个路径都动了"
	}, nil
}

// markChangesReverted 把归档里对应路径的改动行标成已还原。
//
// 只标不改写 Kind：Kind 回答"这次任务当时做了什么"，那是历史；Reverted 回答
// "盘上现在还是那样吗"。合成一个值就把两件事都说糊了。
// 没有归档（任务没跑完、数据目录没配）时静默返回——界面对应的本来就是内存里那条。
func (a *Agent) markChangesReverted(taskID string, paths []string) {
	g, err := ReadTaskResult(a.Cfg.DataDir, taskID)
	if err != nil || g == nil || len(g.Changes) == 0 {
		return
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[pathKey(p)] = true
	}
	changed := false
	for i := range g.Changes {
		if want[pathKey(g.Changes[i].Path)] && !g.Changes[i].Reverted {
			g.Changes[i].Reverted = true
			if info, err := os.Stat(g.Changes[i].Path); err == nil && !info.IsDir() {
				g.Changes[i].Bytes = info.Size()
			} else {
				g.Changes[i].Bytes = 0
			}
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := SaveTaskResult(a.Cfg.DataDir, g); err != nil {
		fmt.Fprintf(os.Stderr, "[gleam] 改动清单已还原但归档没更新（下次读档案会看不到这一位）：%v\n", err)
	}
}
