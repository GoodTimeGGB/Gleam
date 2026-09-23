package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadOverlay_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.LLM.Model = "glm-5.3-air"
	cfg.LLM.Temperature = 0.7
	cfg.Agent.StepRetries = 3
	cfg.Agent.ContextCompress = false
	cfg.Safety.Mode = "plan_first"
	cfg.Safety.ApprovalTimeoutSecs = 120
	cfg.Memory.ShortTermCap = 30
	cfg.Persona.Style = "gentle"
	cfg.Persona.Name = "小光"

	path := filepath.Join(dir, OverlayFile)
	if err := cfg.SaveOverlay(path); err != nil {
		t.Fatal(err)
	}

	fresh := Default()
	if err := LoadOverlay(fresh, path); err != nil {
		t.Fatal(err)
	}
	if fresh.LLM.Model != "glm-5.3-air" || fresh.LLM.Temperature != 0.7 {
		t.Errorf("llm = %s %.2f", fresh.LLM.Model, fresh.LLM.Temperature)
	}
	if fresh.Agent.StepRetries != 3 || fresh.Agent.ContextCompress {
		t.Errorf("agent = %+v", fresh.Agent)
	}
	if fresh.Safety.Mode != "plan_first" || fresh.Safety.ApprovalTimeoutSecs != 120 {
		t.Errorf("safety = %+v", fresh.Safety)
	}
	if fresh.Memory.ShortTermCap != 30 {
		t.Errorf("memory = %+v", fresh.Memory)
	}
	if fresh.Persona.Style != "gentle" || fresh.Persona.Name != "小光" {
		t.Errorf("persona = %+v", fresh.Persona)
	}
}

// TestSaveLoadOverlay_ModelTiersRoundtrip 模型档位表要能存能读：
// 它是个开放的映射，写丢一个键就等于某个场景悄悄退回主模型。
func TestSaveLoadOverlay_ModelTiersRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.LLM.Tiers = map[string]string{
		"coding":    "deepseek-coder",
		"office":    "glm-5.3-flash",
		"reasoning": "deepseek-reasoner",
	}
	path := filepath.Join(dir, OverlayFile)
	if err := cfg.SaveOverlay(path); err != nil {
		t.Fatal(err)
	}
	fresh := Default()
	if err := LoadOverlay(fresh, path); err != nil {
		t.Fatal(err)
	}
	if len(fresh.LLM.Tiers) != 3 {
		t.Fatalf("档位表应原样读回 3 项，实际 %v", fresh.LLM.Tiers)
	}
	for k, want := range cfg.LLM.Tiers {
		if got := fresh.LLM.Tiers[k]; got != want {
			t.Errorf("档位 %s 应为 %q，实际 %q", k, want, got)
		}
	}
}

// TestApply_ModelTiersClearedByEmptyMap 清空档位表要真的生效（空表 = 所有场景都用主模型）。
func TestApply_ModelTiersClearedByEmptyMap(t *testing.T) {
	cfg := Default()
	cfg.LLM.Tiers = map[string]string{"coding": "x"}
	cfg.Apply(map[string]any{"llm": map[string]any{"tiers": map[string]any{}}})
	if len(cfg.LLM.Tiers) != 0 {
		t.Errorf("空档位表应清空配置，实际 %v", cfg.LLM.Tiers)
	}
}

// TestApply_ModelTiersIgnoresBlank 档位名或模型为空白时丢弃，避免造出"指向空模型的档位"。
func TestApply_ModelTiersIgnoresBlank(t *testing.T) {
	cfg := Default()
	cfg.Apply(map[string]any{"llm": map[string]any{"tiers": map[string]any{
		"coding": "  deepseek-coder  ",
		"  ":     "ghost",
		"empty":  "   ",
	}}})
	if len(cfg.LLM.Tiers) != 1 {
		t.Fatalf("只应保留 1 个有效档位，实际 %v", cfg.LLM.Tiers)
	}
	if got := cfg.LLM.Tiers["coding"]; got != "deepseek-coder" {
		t.Errorf("档位值应去掉首尾空白，实际 %q", got)
	}
}

func TestLoadOverlay_MissingFileOK(t *testing.T) {
	cfg := Default()
	if err := LoadOverlay(cfg, filepath.Join(t.TempDir(), "absent.yaml")); err != nil {
		t.Fatalf("文件不存在应为 nil: %v", err)
	}
}

func TestLoadOverlay_CorruptFileFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, OverlayFile)
	if err := os.WriteFile(path, []byte(":\t: broken [yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := LoadOverlay(cfg, path); err != nil {
		t.Fatalf("损坏覆盖层不应报错: %v", err)
	}
	if cfg.Agent.MaxReplans != 2 {
		t.Error("损坏时应保持默认值")
	}
}

func TestApply_PartialPatch(t *testing.T) {
	cfg := Default()
	cfg.Apply(map[string]any{
		"persona": map[string]any{"style": "rigorous"},
		"agent":   map[string]any{"step_retries": int64(5)},
	})
	if cfg.Persona.Style != "rigorous" {
		t.Errorf("style = %s", cfg.Persona.Style)
	}
	if cfg.Agent.StepRetries != 5 {
		t.Errorf("step_retries = %d", cfg.Agent.StepRetries)
	}
	// 与 Default() 比对而不是写死数字：这条测的是"未提及的字段保持默认"，
	// 默认值本身该改就改，写死 12 只会让调默认值的人来改测试（且改错了也没人发现）。
	if want := Default().Agent.MaxSteps; cfg.Agent.MaxSteps != want {
		t.Errorf("未提及字段应保持默认: max_steps = %d，期望 %d", cfg.Agent.MaxSteps, want)
	}
}
