package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// ---------- 审核模型（执行前快筛） ----------

type stubReviewer struct {
	approved bool
	reason   string
	calls    int
}

func (s *stubReviewer) Review(_, _ string, _ map[string]any) (bool, string) {
	s.calls++
	return s.approved, s.reason
}

// mediumTool 中风险工具（user_approved）：默认会被自动放行，正好是审核模型该盯的场景。
type mediumTool struct{}

func (m *mediumTool) Name() string                 { return "medium.write" }
func (m *mediumTool) Description() string          { return "写一个中等风险的东西" }
func (m *mediumTool) Schema() map[string]any       { return map[string]any{"type": "object"} }
func (m *mediumTool) Permission() types.Permission { return types.PermissionUserApproved }
func (m *mediumTool) Execute(context.Context, map[string]any) (any, error) {
	return "ok", nil
}

func reviewRegistry() *registry.Registry {
	r := registry.New()
	r.MustRegister(&mediumTool{})
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) {
			return map[string]any{"text": args["text"]}, nil
		}})
	return r
}

func reviewPlan() types.Plan {
	return types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "medium.write", Args: map[string]any{"path": "x.txt"}},
	}}
}

// TestReviewer_BlockedActionEscalatesToHuman 审核模型不通过时应升级为人工确认，而不是直接放行。
func TestReviewer_BlockedActionEscalatesToHuman(t *testing.T) {
	rev := &stubReviewer{approved: false, reason: "目标只是整理文档，不该写文件"}
	gate := safety.New("auto", nil, nil, nil, time.Second)
	e := &Executor{
		Reg: reviewRegistry(), Gate: gate, Notifier: NopNotifier{},
		StepTimeout: 2 * time.Second, Reviewer: rev, Goal: "整理一下文档",
	}
	res := e.Execute(context.Background(), reviewPlan(), "t1", string(types.ModeAuto), true)
	if rev.calls != 1 {
		t.Fatalf("应做一次快筛，实际 %d", rev.calls)
	}
	// NopNotifier 不批准 → 步骤应失败（说明确实走了审批通道）
	if res.ByID["s1"].Status != types.StepFailed {
		t.Errorf("被审核模型标记的动作必须经人工确认，实际 %v", res.ByID["s1"].Status)
	}
	audit := gate.RecentAudit(10)
	if len(audit) == 0 || audit[0].Action != "denied" {
		t.Errorf("被拒操作必须留痕，实际 %+v", audit)
	}
}

// TestReviewer_ApprovedActionRuns 审核通过时照常执行，不打扰用户。
func TestReviewer_ApprovedActionRuns(t *testing.T) {
	rev := &stubReviewer{approved: true}
	gate := safety.New("auto", nil, nil, nil, time.Second)
	e := &Executor{
		Reg: reviewRegistry(), Gate: gate, Notifier: NopNotifier{},
		StepTimeout: 2 * time.Second, Reviewer: rev, Goal: "写文件",
	}
	res := e.Execute(context.Background(), reviewPlan(), "t1", string(types.ModeAuto), true)
	if res.ByID["s1"].Status != types.StepSucceeded {
		t.Errorf("审核通过就应正常执行，实际 %v（%s）", res.ByID["s1"].Status, res.ByID["s1"].Error)
	}
}

// TestReviewer_ReadOnlyNotReviewed 只读动作不该被送去审核——白花钱还拖慢速度。
func TestReviewer_ReadOnlyNotReviewed(t *testing.T) {
	rev := &stubReviewer{approved: true}
	e := &Executor{
		Reg: dedupeRegistry(&countingTool{}), Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, StepTimeout: 2 * time.Second, Reviewer: rev,
	}
	plan := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "counter", Args: map[string]any{"k": "v"}}}}
	e.Execute(context.Background(), plan, "t1", string(types.ModeAuto), false)
	if rev.calls != 0 {
		t.Errorf("只读动作不应触发快筛，实际 %d 次", rev.calls)
	}
}

// TestReviewer_LLMVerdict 审核模型的输出决定放行与否。
func TestReviewer_LLMVerdict(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		wantOK  bool
		wantHit string
	}{
		{"通过", `{"ok":true,"reason":"属于目标范围"}`, true, ""},
		{"拦截", `{"ok":false,"reason":"会删除工作区外的文件"}`, false, "工作区外"},
		{"输出不可解析时交人工", "我觉得大概可以吧", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := llm.NewMock()
			m.Enqueue("review", tc.reply)
			r := &llmReviewer{LLM: m}
			ok, why := r.Review("整理文档", "file.delete", map[string]any{"path": "/etc/passwd"})
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v（%s）", ok, tc.wantOK, why)
			}
			if tc.wantHit != "" && !strings.Contains(why, tc.wantHit) {
				t.Errorf("应带出原因，实际 %q", why)
			}
		})
	}
}

// TestReviewer_UnavailableFailsOpen 审核模型挂掉时不能把主流程卡死。
func TestReviewer_UnavailableFailsOpen(t *testing.T) {
	m := llm.NewMock()
	m.FailNext(errReviewDown)
	r := &llmReviewer{LLM: m}
	ok, _ := r.Review("整理文档", "file.write", map[string]any{"path": "a.txt"})
	if !ok {
		t.Error("审核模型不可用时应放行，避免把正常任务卡死")
	}
}

// TestReviewer_NilMeansNoBlocking 未配置审核器时完全不介入。
func TestReviewer_NilMeansNoBlocking(t *testing.T) {
	var r *llmReviewer
	if ok, _ := r.Review("g", "t", nil); !ok {
		t.Error("nil 审核器不应拦截")
	}
}

var errReviewDown = &reviewDownError{}

type reviewDownError struct{}

func (e *reviewDownError) Error() string { return "审核模型不可用" }

// TestAudit_RecordsDeniedAndApproved 门控留痕要能同时查到被拒与被放行的记录。
func TestAudit_RecordsDeniedAndApproved(t *testing.T) {
	gate := safety.New("auto", nil, nil, nil, time.Second)
	gate.Record(safety.AuditEntry{Tool: "shell.exec", Risk: "high", Action: "denied", Reason: "高风险"})
	gate.Record(safety.AuditEntry{Tool: "file.write", Risk: "medium", Action: "approved", Reason: "路径可信"})
	got := gate.RecentAudit(10)
	if len(got) != 2 {
		t.Fatalf("应留 2 条，实际 %d", len(got))
	}
	if got[0].Tool != "file.write" {
		t.Errorf("最新的应在最前，实际 %+v", got[0])
	}
	if got[1].Action != "denied" {
		t.Errorf("被拒记录应保留，实际 %+v", got[1])
	}
}

// TestAudit_Bounded 留痕有上限，长期运行不会无限增长。
func TestAudit_Bounded(t *testing.T) {
	gate := safety.New("auto", nil, nil, nil, time.Second)
	for i := 0; i < 500; i++ {
		gate.Record(safety.AuditEntry{Tool: "t", Action: "denied"})
	}
	if got := len(gate.RecentAudit(1000)); got != 200 {
		t.Errorf("应截断到 200 条，实际 %d", got)
	}
}
