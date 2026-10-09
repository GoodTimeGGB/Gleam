package agent

import (
	"context"
	"strings"
	"testing"

	"gleam/internal/config"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// stubLLM 只回一句固定的审核结论，用来验"深度扫描拿到结论之后怎么用"。
type stubLLM struct {
	reply string
	err   error
	seen  *llm.ChatRequest
}

func (s *stubLLM) Chat(_ context.Context, req llm.ChatRequest) (string, error) {
	if s.seen != nil {
		*s.seen = req
	}
	return s.reply, s.err
}
func (s *stubLLM) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (string, error) {
	return s.Chat(ctx, req)
}
func (s *stubLLM) Name() string { return "stub" }

func planWith(steps ...string) *types.Plan {
	p := &types.Plan{Goal: "做点事"}
	for i, s := range steps {
		p.Steps = append(p.Steps, types.Step{ID: string(rune('a' + i)), Tool: s, Description: s})
	}
	return p
}

func TestDeepReviewPlan(t *testing.T) {
	ctx := context.Background()
	plan := planWith("file.read", "file.write")

	// 通过
	a := &Agent{LLM: &stubLLM{reply: `{"ok":true,"reason":"没问题"}`}}
	if ok, _ := a.deepReviewPlan(ctx, "t1", "整理一下", plan); !ok {
		t.Error("ok=true 应判通过")
	}

	// 否决：理由要带回来
	a.LLM = &stubLLM{reply: `{"ok":false,"reason":"计划里在顺手删东西"}`}
	ok, reason := a.deepReviewPlan(ctx, "t1", "整理一下", plan)
	if ok || !strings.Contains(reason, "顺手删东西") {
		t.Errorf("应判否决并带回理由，got ok=%v reason=%q", ok, reason)
	}

	// 输出解析不了：宁可多问一次，也不当通过
	a.LLM = &stubLLM{reply: "我觉得还行"}
	if ok, reason := a.deepReviewPlan(ctx, "t1", "整理一下", plan); ok || reason == "" {
		t.Errorf("解析不了应交人工确认，got ok=%v reason=%q", ok, reason)
	}

	// 模型不可用：不阻断（深度扫描是加一道确认，不是新的失败点）
	a.LLM = &stubLLM{err: context.DeadlineExceeded}
	if ok, _ := a.deepReviewPlan(ctx, "t1", "整理一下", plan); !ok {
		t.Error("模型不可用时不该阻断")
	}

	// 空计划 / 没模型：直接放行，不浪费一次调用
	a.LLM = nil
	if ok, _ := a.deepReviewPlan(ctx, "t1", "整理一下", plan); !ok {
		t.Error("没有模型时应放行")
	}
	a.LLM = &stubLLM{reply: `{"ok":false,"reason":"x"}`}
	if ok, _ := a.deepReviewPlan(ctx, "t1", "整理一下", &types.Plan{}); !ok {
		t.Error("空计划不该判否决")
	}
}

// 审核提示词里必须真的带上每一步——否则"深度扫描"就只是又问了一遍目标。
func TestDeepReviewSendsSteps(t *testing.T) {
	var seen llm.ChatRequest
	a := &Agent{LLM: &stubLLM{reply: `{"ok":true}`, seen: &seen}}
	plan := planWith("file.read", "shell.exec")
	if _, _ = a.deepReviewPlan(context.Background(), "t9", "把日志清一下", plan); true {
	}
	body := seen.Messages[0].Content
	for _, want := range []string{"file.read", "shell.exec", "把日志清一下", "共 2 步"} {
		if !strings.Contains(body, want) {
			t.Errorf("审核请求里没有 %q：\n%s", want, body)
		}
	}
	if seen.TaskID != "t9" {
		t.Errorf("用量应归到任务上，TaskID = %q", seen.TaskID)
	}
}

// 没人可问时放行：否则非交互场景会卡在一个用户看不见的原因上。
func TestConfirmDeepReviewNoNotifier(t *testing.T) {
	a := &Agent{Cfg: config.Default()}
	if !a.confirmDeepReview("t1", "理由") {
		t.Error("没有 Notifier 时应放行")
	}
}
