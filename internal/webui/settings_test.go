package webui

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"gleam/internal/config"
)

// ---------- 设置与上下文 API ----------

func TestWebUI_SettingsGetMasked(t *testing.T) {
	f := newFixture(t, nil)
	s := f.call("GET", "/api/settings", nil)
	if s["persona"] == nil || s["agent"] == nil || s["llm"] == nil {
		t.Fatalf("settings 视图缺 section: %v", s)
	}
	llm := s["llm"].(map[string]any)
	if _, hasKey := llm["api_key"]; hasKey {
		t.Error("不应回传 api_key 明文")
	}
	if llm["api_key_set"] != false {
		t.Errorf("api_key_set = %v", llm["api_key_set"])
	}
}

func TestWebUI_SettingsApplyAndPersist(t *testing.T) {
	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm" // 非 mock 时 ApplySettings 才会热更新 GLM 客户端字段
	updated := f.call("POST", "/api/settings", map[string]any{
		"persona": map[string]any{"style": "gentle", "name": "小光"},
		"agent":   map[string]any{"step_retries": 3, "context_compress": false},
		"safety":  map[string]any{"mode": "plan_first", "approval_timeout_seconds": 60},
	})
	persona := updated["persona"].(map[string]any)
	if persona["style"] != "gentle" || persona["name"] != "小光" {
		t.Errorf("persona = %v", persona)
	}
	agentSec := updated["agent"].(map[string]any)
	if agentSec["step_retries"] != float64(3) || agentSec["context_compress"] != false {
		t.Errorf("agent = %v", agentSec)
	}
	if updated["safety"].(map[string]any)["mode"] != "plan_first" {
		t.Error("safety.mode 未更新")
	}
	// 运行时热生效
	if f.agent.Cfg.Safety.Mode != "plan_first" {
		t.Error("cfg.Safety.Mode 未生效")
	}
	// 覆盖层已持久化，可被新配置加载恢复
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, filepath.Join(f.dataDir, config.OverlayFile)); err != nil {
		t.Fatal(err)
	}
	if fresh.Persona.Style != "gentle" || fresh.Agent.StepRetries != 3 {
		t.Errorf("覆盖层未持久化: %+v %+v", fresh.Persona, fresh.Agent)
	}
}

// TestWebUI_CostGovernanceSettingsRoundTrip 成本治理四件套的设置项应能保存、热生效并持久化。
func TestWebUI_CostGovernanceSettingsRoundTrip(t *testing.T) {
	f := newFixture(t, nil)
	updated := f.call("POST", "/api/settings", map[string]any{
		"agent": map[string]any{
			"dedupe_calls":              false,
			"max_llm_calls_per_task":    7,
			"max_tokens_per_task":       12345,
			"max_task_duration_seconds": 42,
		},
		"llm": map[string]any{"fast_model": "glm-cheap"},
	})
	agentSec := updated["agent"].(map[string]any)
	if agentSec["dedupe_calls"] != false {
		t.Errorf("dedupe_calls = %v", agentSec["dedupe_calls"])
	}
	for k, want := range map[string]float64{
		"max_llm_calls_per_task": 7, "max_tokens_per_task": 12345, "max_task_duration_seconds": 42,
	} {
		if got := agentSec[k].(float64); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if got := updated["llm"].(map[string]any)["fast_model"]; got != "glm-cheap" {
		t.Errorf("fast_model = %v", got)
	}
	// 运行时热生效
	if f.agent.Cfg.Agent.DedupeCalls || f.agent.Cfg.Agent.MaxLLMCallsPerTask != 7 ||
		f.agent.Cfg.Agent.MaxTokensPerTask != 12345 || f.agent.Cfg.Agent.MaxTaskDurationSecs != 42 ||
		f.agent.Cfg.LLM.FastModel != "glm-cheap" {
		t.Errorf("运行时配置未生效: agent=%+v fast=%q", f.agent.Cfg.Agent, f.agent.Cfg.LLM.FastModel)
	}
	// 覆盖层持久化
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, filepath.Join(f.dataDir, config.OverlayFile)); err != nil {
		t.Fatal(err)
	}
	if fresh.Agent.MaxLLMCallsPerTask != 7 || fresh.Agent.MaxTokensPerTask != 12345 ||
		fresh.Agent.MaxTaskDurationSecs != 42 || fresh.LLM.FastModel != "glm-cheap" {
		t.Errorf("覆盖层未持久化: agent=%+v fast=%q", fresh.Agent, fresh.LLM.FastModel)
	}
}

// TestWebUI_BudgetZeroMeansUnlimited 预算填 0 应被接受（0 = 不限），不能因为"非正数"被拒。
func TestWebUI_BudgetZeroMeansUnlimited(t *testing.T) {
	f := newFixture(t, nil)
	updated := f.call("POST", "/api/settings", map[string]any{
		"agent": map[string]any{
			"max_llm_calls_per_task":    0,
			"max_tokens_per_task":       0,
			"max_task_duration_seconds": 0,
		},
	})
	agentSec := updated["agent"].(map[string]any)
	for _, k := range []string{"max_llm_calls_per_task", "max_tokens_per_task", "max_task_duration_seconds"} {
		if got := agentSec[k].(float64); got != 0 {
			t.Errorf("%s = %v, want 0", k, got)
		}
	}
	if f.agent.Cfg.Agent.MaxLLMCallsPerTask != 0 || f.agent.Cfg.Agent.MaxTokensPerTask != 0 ||
		f.agent.Cfg.Agent.MaxTaskDurationSecs != 0 {
		t.Errorf("0 预算未生效: %+v", f.agent.Cfg.Agent)
	}
}

func TestWebUI_SettingsInvalidPatch(t *testing.T) {
	f := newFixture(t, nil)
	// 全部字段非法 → 400
	req := map[string]any{
		"persona": map[string]any{"style": "狂野"},
		"safety":  map[string]any{"mode": "yolo"},
		"agent":   map[string]any{"step_retries": "abc"},
	}
	resp, err := http.Post(f.ts.URL+"/api/settings", "application/json", strings.NewReader(`{"persona":{"style":"狂野"},"safety":{"mode":"yolo"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("非法补丁应 400, got %d", resp.StatusCode)
	}
	_ = req
	// 非法值不应污染现有配置
	if f.agent.Cfg.Persona.Style == "狂野" || f.agent.Cfg.Safety.Mode == "yolo" {
		t.Error("非法值被应用")
	}
}

func TestWebUI_ContextCompressAndClear(t *testing.T) {
	f := newFixture(t, nil)
	// 制造溢出：容量 20，写入 22 轮（每轮足够长，压缩收益为正）
	long := strings.Repeat("需要保留的关键上下文细节。", 12)
	for i := 0; i < 22; i++ {
		f.agent.Mem.AddTurn("user", long)
	}
	ctx := f.call("GET", "/api/context", nil)
	if ctx["enabled"] != true || ctx["overflow"].(float64) < 2 {
		t.Fatalf("context = %v", ctx)
	}
	out := f.call("POST", "/api/context/compress", nil)
	if out["compressed"] != true {
		t.Errorf("compress = %v", out)
	}
	ctxAfter := out["context"].(map[string]any)
	if ctxAfter["overflow"].(float64) != 0 || ctxAfter["summary"].(string) == "" {
		t.Errorf("压缩后 = %v", ctxAfter)
	}
	// token 收益估算应为正（溢出原文远大于摘要）
	if ctxAfter["est_tokens_saved"].(float64) < 0 {
		t.Errorf("est_tokens_saved = %v", ctxAfter["est_tokens_saved"])
	}
	cleared := f.call("POST", "/api/context/clear", nil)
	if cleared["cleared"] != true {
		t.Errorf("clear = %v", cleared)
	}
	if cleared["context"].(map[string]any)["summary"].(string) != "" {
		t.Error("清空后摘要应为空")
	}
}

func TestWebUI_SettingsPageRendered(t *testing.T) {
	f := newFixture(t, nil)
	resp, err := http.Get(f.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	_, _ = io.Copy(&b, resp.Body)
	body := b.String()
	if !strings.Contains(body, "view-settings") || !strings.Contains(body, "记忆与上下文") {
		t.Error("设置视图未内嵌到首页")
	}
}
