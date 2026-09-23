package agent

import (
	"strings"
	"testing"

	"gleam/internal/llm"
	"gleam/pkg/types"
)

func mkExec(succ, fail, skipped int) *Result {
	res := &Result{Total: succ + fail + skipped}
	for i := 0; i < succ; i++ {
		res.Steps = append(res.Steps, types.StepResult{StepID: "s", Tool: "t", Status: types.StepSucceeded})
		res.Succeeded++
	}
	for i := 0; i < fail; i++ {
		res.Steps = append(res.Steps, types.StepResult{StepID: "f", Tool: "t", Status: types.StepFailed, Error: "x"})
		res.Failed++
	}
	for i := 0; i < skipped; i++ {
		res.Steps = append(res.Steps, types.StepResult{StepID: "k", Tool: "t", Status: types.StepSkipped})
		res.Skipped++
	}
	return res
}

func TestReflector_ParseLLMOutput(t *testing.T) {
	r := &Reflector{LLM: mockReturning(`{"score":95,"verdict":"done","reason":"目标达成","suggestion":"可以固化为技能"}`)}
	refl := r.Evaluate(testCtx(), "g", mkExec(2, 0, 0), 1)
	if refl.Score != 95 || refl.Verdict != "done" || refl.Suggestion == "" {
		t.Errorf("reflection = %+v", refl)
	}
}

func TestReflector_FallbackOnBadOutput(t *testing.T) {
	r := &Reflector{LLM: mockReturning("我觉得挺好的！")}
	refl := r.Evaluate(testCtx(), "g", mkExec(3, 0, 0), 1)
	if refl.Verdict != "done" || refl.Score < 80 {
		t.Errorf("全成功应兜底 done: %+v", refl)
	}
	refl = r.Evaluate(testCtx(), "g", mkExec(1, 2, 0), 1)
	if refl.Verdict != "replan" {
		t.Errorf("有失败应兜底 replan: %+v", refl)
	}
}

func TestReflector_Cancelled(t *testing.T) {
	r := &Reflector{LLM: mockReturning(`{"score":100,"verdict":"done"}`)}
	exec := mkExec(1, 0, 0)
	exec.Cancelled = true
	refl := r.Evaluate(testCtx(), "g", exec, 1)
	if refl.Verdict != "failed" || refl.Score != 0 {
		t.Errorf("取消应直接 failed: %+v", refl)
	}
}

func TestReflector_NoLLM(t *testing.T) {
	r := &Reflector{LLM: nil}
	refl := r.Evaluate(testCtx(), "g", mkExec(2, 0, 0), 1)
	if refl.Verdict != "done" {
		t.Errorf("无 LLM 兜底: %+v", refl)
	}
}

func TestReflector_DigestTruncated(t *testing.T) {
	exec := mkExec(1, 1, 0)
	exec.Steps[0].Output = map[string]any{"big": strings.Repeat("x", 5000)}
	digest := buildDigest("目标文本", exec, 2, nil, nil)
	if !strings.Contains(digest, "第 2 次") || !strings.Contains(digest, "目标文本") {
		t.Errorf("digest 缺少关键字段")
	}
	if strings.Contains(digest, strings.Repeat("x", 1000)) {
		t.Error("输出应被截断")
	}
	_ = llm.MarkerPlan
}
