package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// ---------- 测试工具 ----------

type funcTool struct {
	name   string
	perm   types.Permission
	schema map[string]any
	// paths 与工具实现同签名（带 ctx）：测试要能验证"路径是按本次任务的边界解析的"。
	paths func(ctx context.Context, args map[string]any) []string
	fn    func(ctx context.Context, args map[string]any) (any, error)
}

func (f *funcTool) Name() string        { return f.name }
func (f *funcTool) Description() string { return "func tool " + f.name }
func (f *funcTool) Schema() map[string]any {
	if f.schema != nil {
		return f.schema
	}
	return map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
}
func (f *funcTool) Permission() types.Permission { return f.perm }
func (f *funcTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return f.fn(ctx, args)
}
func (f *funcTool) Paths(ctx context.Context, args map[string]any) []string {
	if f.paths != nil {
		return f.paths(ctx, args)
	}
	return nil
}

func newTestRegistry() *registry.Registry {
	r := registry.New()
	r.MustRegister(&funcTool{name: "counter", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]any{"n": 42, "text": "hello"}, nil
	}})
	r.MustRegister(&funcTool{name: "fail", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return nil, errors.New("boom")
	}})
	r.MustRegister(&funcTool{name: "slow", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		time.Sleep(2 * time.Second)
		return "done", nil
	}})
	r.MustRegister(&funcTool{name: "echo", perm: types.PermissionReadOnly,
		schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"in": map[string]any{"type": "string"}},
			"required":   []any{"in"},
		},
		fn: func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"got": args["in"]}, nil
		}})
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]any{"text": args["text"]}, nil
	}})
	return r
}

// recordingNotifier 记录事件并按脚本应答审批。
type recordingNotifier struct {
	mu          sync.Mutex
	progress    []types.ProgressEvent
	approvals   []types.ApprovalRequest
	approveAll  bool
	suggestions []string
	skillDrafts []types.SkillDraft
	done        []types.TaskDoneEvent
}

func (n *recordingNotifier) OnProgress(ev types.ProgressEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.progress = append(n.progress, ev)
}
func (n *recordingNotifier) OnApproval(req types.ApprovalRequest) types.ApprovalResponse {
	n.mu.Lock()
	n.approvals = append(n.approvals, req)
	approve := n.approveAll
	n.mu.Unlock()
	return types.ApprovalResponse{Approved: approve}
}
func (n *recordingNotifier) OnSuggestion(taskID, text string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.suggestions = append(n.suggestions, text)
}
func (n *recordingNotifier) OnSuggestSkill(taskID string, draft types.SkillDraft) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.skillDrafts = append(n.skillDrafts, draft)
}
func (n *recordingNotifier) OnTaskDone(ev types.TaskDoneEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.done = append(n.done, ev)
}

// ---------- Execute：DAG 与并发 ----------

func TestExecute_DAGOrderAndResults(t *testing.T) {
	var order []string
	var mu sync.Mutex
	r := registry.New()
	r.MustRegister(&funcTool{name: "first", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
		return map[string]any{"value": "F1"}, nil
	}})
	r.MustRegister(&funcTool{name: "second", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		return "S2", nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "first"},
		{ID: "s2", Tool: "second", DependsOn: []string{"s1"}},
	}}
	res := e.Execute(context.Background(), plan, "t1", "auto", false)
	if res.Succeeded != 2 || res.Failed != 0 {
		t.Fatalf("结果错误: %+v", res)
	}
	if order[0] != "first" || order[1] != "second" {
		t.Errorf("执行顺序 = %v", order)
	}
	if res.ReplyText != "" {
		t.Error("无 reply 步骤")
	}
}

func TestExecute_ParallelConcurrency(t *testing.T) {
	var running, maxRunning atomic.Int64
	r := registry.New()
	r.MustRegister(&funcTool{name: "hold", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		cur := running.Add(1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
		running.Add(-1)
		return "ok", nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), MaxConcurrency: 3, Notifier: NopNotifier{}}
	plan := types.Plan{}
	for i := 0; i < 6; i++ {
		plan.Steps = append(plan.Steps, types.Step{ID: fmt.Sprintf("s%d", i), Tool: "hold"})
	}
	res := e.Execute(context.Background(), plan, "t", "auto", false)
	if res.Succeeded != 6 {
		t.Fatalf("成功数 = %d", res.Succeeded)
	}
	if maxRunning.Load() > 3 {
		t.Errorf("并发峰值 %d 超过上限 3", maxRunning.Load())
	}
	if maxRunning.Load() < 2 {
		t.Errorf("未发生并行: %d", maxRunning.Load())
	}
}

func TestExecute_DepFailureSkips(t *testing.T) {
	e := &Executor{Reg: newTestRegistry(), Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "fail"},
		{ID: "s2", Tool: "counter", DependsOn: []string{"s1"}},
		{ID: "s3", Tool: "counter", DependsOn: []string{"s2"}},
	}}
	res := e.Execute(context.Background(), plan, "t", "auto", false)
	if res.Failed != 1 || res.Skipped != 2 || res.Succeeded != 0 {
		t.Fatalf("结果 = 成功%d 失败%d 跳过%d", res.Succeeded, res.Failed, res.Skipped)
	}
	if !strings.Contains(res.ByID["s2"].Error, "依赖") {
		t.Errorf("跳过原因 = %q", res.ByID["s2"].Error)
	}
}

func TestExecute_RefSubstitution(t *testing.T) {
	var seenIn, seenWhole, seenField any
	r := registry.New()
	r.MustRegister(&funcTool{name: "producer", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]any{"text": "PRODUCED", "count": 7}, nil
	}})
	r.MustRegister(&funcTool{name: "consumer", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		seenIn = args["in"]
		seenWhole = args["whole"]
		seenField = args["field"]
		return nil, nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "producer"},
		{ID: "s2", Tool: "consumer", DependsOn: []string{"s1"}, Args: map[string]any{
			"in":    "前缀 {ref:s1.text} 后缀",
			"whole": "$ref:s1",
			"field": "$ref:s1.count",
		}},
	}}
	res := e.Execute(context.Background(), plan, "t", "auto", false)
	if res.Succeeded != 2 {
		t.Fatalf("成功 = %d, s2 错误: %s", res.Succeeded, res.ByID["s2"].Error)
	}
	if seenIn != "前缀 PRODUCED 后缀" {
		t.Errorf("插值 = %v", seenIn)
	}
	if m, ok := seenWhole.(map[string]any); !ok || m["text"] != "PRODUCED" {
		t.Errorf("整值引用 = %v", seenWhole)
	}
	// 字段引用保留原始类型
	if n, ok := seenField.(int); !ok || n != 7 {
		t.Errorf("字段引用 = %v (%T)", seenField, seenField)
	}
	if res.ExecutedPlan.Steps[1].Args["in"] != "前缀 PRODUCED 后缀" {
		t.Error("ExecutedPlan 应保存替换后参数")
	}
}

func TestExecute_RefErrors(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "noop", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return nil, nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "noop"},
		{ID: "s2", Tool: "noop", DependsOn: []string{"s1"}, Args: map[string]any{"x": "$ref:s1.nosuch"}},
		{ID: "s3", Tool: "noop", DependsOn: []string{"s1"}, Args: map[string]any{"x": "$ref:ghost"}},
	}}
	res := e.Execute(context.Background(), plan, "t", "auto", false)
	if res.Failed != 2 {
		t.Fatalf("失败数 = %d", res.Failed)
	}
	if !strings.Contains(res.ByID["s2"].Error, "不存在字段") || !strings.Contains(res.ByID["s3"].Error, "不存在") {
		t.Errorf("错误信息: %q | %q", res.ByID["s2"].Error, res.ByID["s3"].Error)
	}
}

func TestExecute_StepTimeout(t *testing.T) {
	e := &Executor{Reg: newTestRegistry(), Gate: safety.New("auto", nil, nil, nil, time.Second), StepTimeout: 100 * time.Millisecond, Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "slow"}}}
	res := e.Execute(context.Background(), plan, "t", "auto", false)
	if res.Failed != 1 || !strings.Contains(res.ByID["s1"].Error, "超时") {
		t.Fatalf("应为超时失败: %+v", res.ByID["s1"])
	}
}

func TestExecute_Cancel(t *testing.T) {
	block := make(chan struct{})
	r := registry.New()
	r.MustRegister(&funcTool{name: "block", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	r.MustRegister(&funcTool{name: "never", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		<-block
		return nil, nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	ctx, cancel := context.WithCancel(context.Background())
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "block"},
		{ID: "s2", Tool: "never", DependsOn: []string{"s1"}},
	}}
	done := make(chan *Result, 1)
	go func() { done <- e.Execute(ctx, plan, "t", "auto", false) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	res := <-done
	close(block)
	if !res.Cancelled {
		t.Error("应标记取消")
	}
	if res.ByID["s2"].Status != types.StepSkipped {
		t.Errorf("后续步骤应跳过: %+v", res.ByID["s2"])
	}
}

// ---------- Execute：审批 ----------

func TestExecute_ApprovalFlow(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "write", perm: types.PermissionUserApproved, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return "written", nil
	}})

	// 拒绝
	n := &recordingNotifier{}
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: n}
	plan := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "write"}}}
	res := e.Execute(context.Background(), plan, "t", "auto", false)
	if res.Failed != 1 || !strings.Contains(res.ByID["s1"].Error, "拒绝") {
		t.Fatalf("拒绝场景: %+v", res.ByID["s1"])
	}
	if len(n.approvals) != 1 || n.approvals[0].Risk != "medium" {
		t.Errorf("审批请求 = %+v", n.approvals)
	}

	// 批准
	n2 := &recordingNotifier{approveAll: true}
	e2 := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: n2}
	res2 := e2.Execute(context.Background(), plan, "t", "auto", false)
	if res2.Succeeded != 1 {
		t.Errorf("批准后应成功: %+v", res2.ByID["s1"])
	}
}

func TestExecute_PreApprovedBypassesMedium(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "write", perm: types.PermissionUserApproved, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return "ok", nil
	}})
	n := &recordingNotifier{}
	e := &Executor{Reg: r, Gate: safety.New("plan_first", nil, nil, nil, time.Second), Notifier: n}
	plan := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "write"}}}
	res := e.Execute(context.Background(), plan, "t", "plan_first", true)
	if res.Succeeded != 1 || len(n.approvals) != 0 {
		t.Errorf("预批准应免审: 成功=%d 审批=%d", res.Succeeded, len(n.approvals))
	}
	// 高风险不能豁免
	r.MustRegister(&funcTool{name: "danger", perm: types.PermissionFullAccess, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return "ok", nil
	}})
	plan2 := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "danger"}}}
	res2 := e.Execute(context.Background(), plan2, "t", "plan_first", true)
	if res2.Succeeded != 0 || len(n.approvals) == 0 {
		t.Error("高风险在预批准下仍需审批")
	}
}

func TestExecute_ReplyTextCaptured(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]any{"text": "回答完毕"}, nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	res := e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "reply"}}}, "t", "auto", false)
	if res.ReplyText != "回答完毕" {
		t.Errorf("ReplyText = %q", res.ReplyText)
	}
}

// ---------- 步骤 panic 边界 ----------

// TestExecute_ToolPanicFailsOnlyThatStep 是本批的核心断言：**一次工具 panic 只该让这一步失败**。
//
// 它守的是一个具体的损失：`gleam eval` 跑一批用例时，第 5 条用例的工具 panic 会让整个进程
// 终止——已通过的用例、已经花掉的模型调用、整份报告一起消失，重跑还要再花一次。
// 而 panic 发生在**子 goroutine** 里，Go 的 recover 只在同一个 goroutine 的 defer 里有效，
// 所以外层（eval runner / cmdGoal / webui handler）加 recover 都接不住——
// 断言必须打在执行器这一层，否则测的是别的东西。
func TestExecute_ToolPanicFailsOnlyThatStep(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "boom", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		var m map[string]int
		m["x"] = 1 // 对 nil map 写入 → panic，且消息确定
		return nil, nil
	}})
	r.MustRegister(&funcTool{name: "ok", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]any{"value": "fine"}, nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "boom"},
		{ID: "s2", Tool: "ok"},
	}}
	res := e.Execute(context.Background(), plan, "t-panic", "auto", false)

	// ① 整批跑完：炸的那步失败，另一步照常成功。
	if res.Total != 2 || res.Failed != 1 || res.Succeeded != 1 {
		t.Fatalf("应 1 失败 1 成功，实得 成功%d 失败%d 跳过%d", res.Succeeded, res.Failed, res.Skipped)
	}
	bad := res.ByID["s1"]
	if bad == nil || bad.Status != types.StepFailed {
		t.Fatalf("s1 应为 StepFailed，实得 %+v", bad)
	}
	// ② 不静默：错误文本必须能认出是 panic，且保留 panic 值本身。
	if !strings.Contains(bad.Error, "工具 panic:") {
		t.Errorf("错误文本应含 panic 标记，实得 %q", bad.Error)
	}
	if !strings.Contains(bad.Error, "assignment to entry in nil map") {
		t.Errorf("错误文本应保留 panic 值，实得 %q", bad.Error)
	}
	// ③ 归因单独一类：不是 unknown（有类别）也不是 business（不是业务拒绝）。
	if bad.ErrorKind != types.ErrInternal {
		t.Errorf("归因应为 %q，实得 %q", types.ErrInternal, bad.ErrorKind)
	}
	// ④ 栈要有界：完整栈几十行，灌进 tasks/*.json 会把诊断信息淹掉。
	if n := strings.Count(bad.Error, "\n"); n > 8 {
		t.Errorf("错误文本 %d 行，应压到 8 行内", n)
	}
	// ⑤ 同批另一步完全不受影响。
	if good := res.ByID["s2"]; good == nil || good.Status != types.StepSucceeded {
		t.Fatalf("s2 应成功，实得 %+v", good)
	}
}

// TestExecute_ToolPanicReleasesDownstream 守的是 recover 之后最容易漏的一件事。
//
// goroutine 里 panic 之后，**defer 之外的剩余语句全部被跳过**。所以如果收尾
// （close(state.finished[i]) / 落盘 / 计数 / 进度）没有在 recover 的那段里补全，
// 依赖这一步的下游会**永远等不到放行**——症状是**任务挂住**，而不是报错。
// 挂住比崩掉更难查：没有栈、没有错误、只有一个不动的进度条。
//
// 用显式超时把"挂住"变成一条可读的失败信息，而不是让 go test 的 10 分钟兜底
// 把它变成一句看不出原因的 context deadline exceeded。
func TestExecute_ToolPanicReleasesDownstream(t *testing.T) {
	r := registry.New()
	r.MustRegister(&funcTool{name: "boom", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		panic("工具内部炸了")
	}})
	r.MustRegister(&funcTool{name: "after", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		return "ran", nil
	}})
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "boom"},
		{ID: "s2", Tool: "after", DependsOn: []string{"s1"}},
	}}

	done := make(chan *Result, 1)
	go func() { done <- e.Execute(context.Background(), plan, "t-panic-dep", "auto", false) }()

	select {
	case res := <-done:
		if res.Failed != 1 || res.Skipped != 1 {
			t.Fatalf("应 1 失败 1 跳过，实得 失败%d 跳过%d", res.Failed, res.Skipped)
		}
		if !strings.Contains(res.ByID["s2"].Error, "依赖") {
			t.Errorf("下游应因依赖未满足而跳过，实得 %q", res.ByID["s2"].Error)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("下游步骤没有被放行——panic 路径漏了 close(state.finished[i])，任务挂住了")
	}
}

// TestFailureBreakdown_CountsPanicAsInternal 把「panic 要能被归因看见」钉在归因层。
//
// 上面两条证明 panic 变成了 StepFailed；这一条证明它**进得了归因分布**——
// 否则"步骤红了"与"知道该先修哪一层"之间还差一步，而这一步正是分布存在的理由
// （见 failureBreakdown 的注释：判据是"能不能回答该先修哪层"）。
func TestFailureBreakdown_CountsPanicAsInternal(t *testing.T) {
	got := failureBreakdown([]types.StepResult{
		{StepID: "s1", Status: types.StepFailed, ErrorKind: types.ErrInternal},
		{StepID: "s2", Status: types.StepFailed, ErrorKind: types.ErrBusiness},
		{StepID: "s3", Status: types.StepSucceeded},
	})
	if got[types.ErrInternal] != 1 || got[types.ErrBusiness] != 1 {
		t.Fatalf("归因分布 = %v", got)
	}
	if got[types.ErrUnknown] != 0 {
		t.Errorf("已定类的失败不该落进 unknown：%v", got)
	}
}
