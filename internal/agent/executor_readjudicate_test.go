package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// ---------- 派发前重算裁决 ----------
//
// 计划里的 `$ref:` 在门控眼里只是一个**非绝对路径的字符串**，而门控对非绝对路径的
// 口径是"按工作区内放过"（safety.pathTrusted）。于是"要动哪个路径"这件事在被裁决时
// 根本还没有值，真值却可能落在信任范围外——更坏的是审计会写下
// "操作路径均在信任路径内"，替一次没做过的检查作证。
//
// 下面四条钉住重算的四种结果：真值出界要问人（卡片上是真值）、真值在界内照常自动
// 放行（且审核模型只看真值一次）、人批过之后不再补记"自动放行"、审批途中改了安全
// 模式也不能把"人看过"洗成"没人看过"。

// capturingReviewer 记下每次快筛看到的参数，用于断言"审核模型看的是真值，不是占位符"。
type capturingReviewer struct {
	approved bool
	seen     []map[string]any
}

func (c *capturingReviewer) Review(_, _ string, args map[string]any) (bool, string) {
	c.seen = append(c.seen, args)
	return c.approved, "超出目标范围"
}

// refFixture 搭一个「s1 产出一个路径，s2 用 $ref 引用它去写」的场景。
// work 是工作区（门控信任它）；resolved 是 s1 真产出的路径——它在不在 work 里，
// 决定了 s2 该被重新裁决成"要问人"还是"照常放行"。
//
// writer 的 Paths 原样交出参数里的 path，与 internal/tools/file 同形：
// 解析不了的值原样返回，由门控自己判。
func refFixture(work, resolved string, called *bool) (*registry.Registry, *safety.Gate) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "locate", perm: types.PermissionReadOnly,
		fn: func(context.Context, map[string]any) (any, error) {
			return map[string]any{"path": resolved}, nil
		}})
	r.MustRegister(&funcTool{
		name: "writer", perm: types.PermissionUserApproved,
		paths: func(_ context.Context, args map[string]any) []string {
			if p, ok := args["path"].(string); ok && p != "" {
				return []string{p}
			}
			return nil
		},
		fn: func(context.Context, map[string]any) (any, error) {
			*called = true
			return "written", nil
		},
	})
	return r, safety.New("auto", nil, nil, []string{work}, time.Second)
}

func refPlan() types.Plan {
	return types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "locate"},
		{ID: "s2", Tool: "writer", DependsOn: []string{"s1"}, Args: map[string]any{"path": "$ref:s1.path"}},
	}}
}

// auditOf 取某个工具的某类留痕（没有则返回 nil）。
func auditOf(gate *safety.Gate, tool, action string) *safety.AuditEntry {
	entries := gate.RecentAudit(50)
	for i := range entries {
		if entries[i].Tool == tool && entries[i].Action == action {
			return &entries[i]
		}
	}
	return nil
}

func TestExecute_RefToUntrustedPathAsksHuman(t *testing.T) {
	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	var called bool
	reg, gate := refFixture(work, outside, &called)
	n := &recordingNotifier{approveAll: false}
	e := &Executor{Reg: reg, Gate: gate, Notifier: n, StepTimeout: time.Second}
	res := e.Execute(context.Background(), refPlan(), "t", "auto", false)

	if called {
		t.Error("真值落在信任范围外且未获批，工具不该被执行")
	}
	if res.ByID["s2"].Status != types.StepFailed {
		t.Fatalf("被拒应失败，实际 %v（%s）", res.ByID["s2"].Status, res.ByID["s2"].Error)
	}
	if res.ByID["s2"].ErrorKind != types.ErrPermission {
		t.Errorf("拒绝属权限类错误，实际 %v", res.ByID["s2"].ErrorKind)
	}
	// 只看占位符的那次裁决会放行，所以这唯一一次审批请求只能来自重算。
	if len(n.approvals) != 1 {
		t.Fatalf("应恰好 1 次审批请求，实际 %d（%+v）", len(n.approvals), n.approvals)
	}
	if got := n.approvals[0].Reason; !strings.Contains(got, outside) || strings.Contains(got, "$ref:") {
		t.Errorf("审批卡片上的理由必须是真值，实际 %q", got)
	}
	if a := auditOf(gate, "writer", "denied"); a == nil {
		t.Errorf("被拒必须留痕，实际 %+v", gate.RecentAudit(10))
	} else if !strings.Contains(a.Reason, outside) {
		t.Errorf("留痕的理由必须是真值，实际 %q", a.Reason)
	}
}

func TestExecute_RefEscalationApprovedThenRuns(t *testing.T) {
	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	var called bool
	reg, gate := refFixture(work, outside, &called)
	n := &recordingNotifier{approveAll: true}
	e := &Executor{Reg: reg, Gate: gate, Notifier: n, StepTimeout: time.Second}
	res := e.Execute(context.Background(), refPlan(), "t", "auto", false)

	if !called || res.ByID["s2"].Status != types.StepSucceeded {
		t.Fatalf("人批了就该执行，实际 %v（%s）", res.ByID["s2"].Status, res.ByID["s2"].Error)
	}
	if a := auditOf(gate, "writer", "approved"); a == nil || !strings.Contains(a.Reason, outside) {
		t.Errorf("批准留痕应带真值，实际 %+v", gate.RecentAudit(10))
	}
	// 人已经看过这一步，再补一条"自动放行"等于把有人看过的动作记成没人看过。
	if a := auditOf(gate, "writer", "auto"); a != nil {
		t.Errorf("已走人工确认的步骤不该再记自动放行：%+v", a)
	}
}

func TestExecute_RefInsideWorkspaceStillAuto(t *testing.T) {
	work := t.TempDir()
	inside := filepath.Join(work, "out.txt")
	var called bool
	reg, gate := refFixture(work, inside, &called)
	n := &recordingNotifier{}
	rev := &capturingReviewer{approved: true}
	e := &Executor{
		Reg: reg, Gate: gate, Notifier: n, StepTimeout: time.Second,
		Reviewer: rev, Goal: "把结果写到工作区",
	}
	res := e.Execute(context.Background(), refPlan(), "t", "auto", false)

	if !called || res.ByID["s2"].Status != types.StepSucceeded {
		t.Fatalf("真值在工作区内应照常执行，实际 %v（%s）", res.ByID["s2"].Status, res.ByID["s2"].Error)
	}
	if len(n.approvals) != 0 {
		t.Errorf("真值可信就不该打扰用户，实际 %+v", n.approvals)
	}
	a := auditOf(gate, "writer", "auto")
	if a == nil {
		t.Fatalf("自动放行的副作用动作必须留痕，实际 %+v", gate.RecentAudit(10))
	}
	if !strings.Contains(a.Reason, "信任路径内") {
		t.Errorf("留痕理由应出自真值那次裁决，实际 %q", a.Reason)
	}
	// 快筛只看真值一次：占位符那次快筛是花钱筛一个假值。
	if len(rev.seen) != 1 {
		t.Fatalf("快筛应恰好 1 次，实际 %d 次（%v）", len(rev.seen), rev.seen)
	}
	if got := rev.seen[0]["path"]; got != inside {
		t.Errorf("快筛看到的应是真值 %q，实际 %v", inside, got)
	}
}

// modeFlipNotifier 批准的同时把门控切到 auto。运行时改安全模式是真能发生的
// （设置页随时可改，任务在并发跑），所以这一步不能因为模式变了就被记成"自动放行"。
type modeFlipNotifier struct {
	NopNotifier
	gate *safety.Gate
}

func (m *modeFlipNotifier) OnApproval(types.ApprovalRequest) types.ApprovalResponse {
	m.gate.SetMode("auto")
	return types.ApprovalResponse{Approved: true, Note: "顺手把模式改成 auto"}
}

// 重算之后的裁决更宽松时，也不能把"人已经批过"洗成"自动放行"：
// 台账上那一步会同时挂着 approved 与 auto，读的人算不出到底有多少动作没人看过。
func TestExecute_ModeFlippedMidApprovalNotRecordedAsAuto(t *testing.T) {
	work := t.TempDir()
	inside := filepath.Join(work, "out.txt")
	var called bool
	reg, gate := refFixture(work, inside, &called)
	gate.SetMode("plan_first")
	e := &Executor{
		Reg: reg, Gate: gate, Notifier: &modeFlipNotifier{gate: gate}, StepTimeout: time.Second,
	}
	res := e.Execute(context.Background(), refPlan(), "t", "plan_first", false)

	if !called || res.ByID["s2"].Status != types.StepSucceeded {
		t.Fatalf("人已批准，应执行，实际 %v（%s）", res.ByID["s2"].Status, res.ByID["s2"].Error)
	}
	if a := auditOf(gate, "writer", "approved"); a == nil {
		t.Fatalf("人工批准必须留痕，实际 %+v", gate.RecentAudit(10))
	}
	if a := auditOf(gate, "writer", "auto"); a != nil {
		t.Errorf("同一步不该再记一条自动放行：%+v", a)
	}
}
