package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// ---------- 旁白层（自研人格化叙述） ----------

func TestNarrator_StyleVariants(t *testing.T) {
	for _, style := range []string{"rigorous", "gentle", "efficient", ""} {
		n := Narrator{Style: style}
		msgs := []string{n.PlanStart(), n.PlanDone(3), n.ReflectStart(), n.Replan(45), n.Compressed()}
		for i, m := range msgs {
			if strings.TrimSpace(m) == "" {
				t.Errorf("风格 %s 第 %d 条旁白为空", style, i)
			}
		}
		if !strings.Contains(n.PlanDone(3), "3") {
			t.Errorf("风格 %s 旁白未包含步骤数", style)
		}
	}
	// 三种风格在规划完成上应产出不同文案（人格差异可见）
	got := map[string]bool{}
	for _, style := range []string{"rigorous", "gentle", "efficient"} {
		got[Narrator{Style: style}.PlanDone(3)] = true
	}
	if len(got) != 3 {
		t.Errorf("三种风格的旁白应不同: %v", got)
	}
}

// ---------- 执行器丝滑：审批等待不占用并发槽位 ----------

// blockingApprovalNotifier 审批请求到达即通知并阻塞，直到测试放行。
type blockingApprovalNotifier struct {
	approveReq chan struct{}
	release    chan struct{}
}

func (b *blockingApprovalNotifier) OnProgress(types.ProgressEvent) {}
func (b *blockingApprovalNotifier) OnApproval(types.ApprovalRequest) types.ApprovalResponse {
	b.approveReq <- struct{}{}
	<-b.release
	return types.ApprovalResponse{Approved: true}
}
func (b *blockingApprovalNotifier) OnSuggestion(string, string)             {}
func (b *blockingApprovalNotifier) OnSuggestSkill(string, types.SkillDraft) {}
func (b *blockingApprovalNotifier) OnTaskDone(types.TaskDoneEvent)          {}

func TestExecute_ApprovalWaitDoesNotBlockOtherSteps(t *testing.T) {
	r := registry.New()

	// s2 的工具：执行即发出完成信号
	var once sync.Once
	s2Done := make(chan struct{})
	r.MustRegister(&funcTool{name: "signal", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		once.Do(func() { close(s2Done) })
		return map[string]any{"n": 1}, nil
	}})
	r.MustRegister(&funcTool{name: "needs-approval", perm: types.PermissionUserApproved, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return "ok", nil
	}})

	n := &blockingApprovalNotifier{approveReq: make(chan struct{}, 1), release: make(chan struct{})}
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, 5*time.Second),
		MaxConcurrency: 1, Notifier: n} // 并发=1：旧行为下 s2 会被 s1 的审批等待完全卡死

	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "needs-approval", Args: map[string]any{}},
		{ID: "s2", Tool: "signal", Args: map[string]any{}},
	}}
	done := make(chan *Result, 1)
	go func() { done <- e.Execute(context.Background(), plan, "t", "auto", false) }()

	// 等待审批请求出现（说明 s1 正在等待用户裁决）
	select {
	case <-n.approveReq:
	case <-time.After(3 * time.Second):
		t.Fatal("未收到审批请求")
	}

	// 审批仍挂着：并发=1 时 s2 也必须能完成（不被审批等待阻塞）
	select {
	case <-s2Done:
	case <-time.After(3 * time.Second):
		t.Fatal("审批等待期间其他步骤被并发槽位阻塞（丝滑回退）")
	}

	close(n.release)
	res := <-done
	if res.Succeeded != 2 {
		t.Fatalf("应全部成功: %+v", res.Steps)
	}
}
