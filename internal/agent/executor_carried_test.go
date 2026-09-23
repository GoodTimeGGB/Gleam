package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// countingSink 记录 Sink 收到的每一条步骤结果。
type countingSink struct {
	mu  sync.Mutex
	got []types.StepResult
}

func (s *countingSink) RecordStep(taskID string, r types.StepResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, r)
}

func (s *countingSink) ids() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]int{}
	for _, r := range s.got {
		m[r.StepID]++
	}
	return m
}

// 恢复语义的核心：沿用的步骤**这次根本不执行**。
//
// 这是 `replay --from` 的全部意义所在。不这样做，「从第 3 步重新开始」就退化成了
// 「从头再跑一遍」——而重跑有副作用（写文件、发请求、下单），复盘一次等于把副作用
// 又做一遍。工具被调用几次是这里唯一算数的证据，所以直接数调用次数。
func TestExecute_CarriedStepsAreNotExecuted(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	reg := newTestRegistry()
	reg.MustRegister(&funcTool{name: "tally", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		mu.Lock()
		calls[args["id"].(string)]++
		mu.Unlock()
		return map[string]any{"ok": args["id"]}, nil
	}})

	// s1 沿用上次的成功结果，s2 依赖 s1（依赖必须照常放行），s3 这次真的跑。
	carried := map[string]types.StepResult{
		"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK, Output: map[string]any{"ok": "s1"}},
	}
	e := &Executor{
		Reg: reg, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{},
		Carried: carried,
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "tally", Args: map[string]any{"id": "s1"}},
		{ID: "s2", Tool: "tally", DependsOn: []string{"s1"}, Args: map[string]any{"id": "s2"}},
		{ID: "s3", Tool: "tally", DependsOn: []string{"s2"}, Args: map[string]any{"id": "s3"}},
	}}
	res := e.Execute(context.Background(), plan, "t1", "auto", false)

	if calls["s1"] != 0 {
		t.Errorf("s1 被沿用了却仍然执行了 %d 次——恢复变成了重跑", calls["s1"])
	}
	if calls["s2"] != 1 || calls["s3"] != 1 {
		t.Errorf("s2/s3 应各执行一次，实际 %d/%d", calls["s2"], calls["s3"])
	}
	if res.Carried != 1 {
		t.Errorf("沿用步数 = %d，应为 1——不单独计数的话，「这次成功 3 步」会被读成「这次跑了 3 步」", res.Carried)
	}
	if res.Succeeded != 3 {
		t.Errorf("成功数 = %d，沿用下来的成功步骤也算成功（结果确实是成功的）", res.Succeeded)
	}
	if !res.ByID["s1"].Carried {
		t.Error("沿用的步骤必须被标注，否则读的人分不清哪几步是这次的")
	}
	if res.ByID["s2"].Carried || res.ByID["s3"].Carried {
		t.Error("真跑过的步骤不能标成沿用")
	}
	// 沿用的结果内容要原样带过来，而不是留个空壳。
	if m, ok := res.ByID["s1"].Output.(map[string]any); !ok || m["ok"] != "s1" {
		t.Errorf("沿用的结果内容丢了: %#v", res.ByID["s1"].Output)
	}
}

// 沿用下来的失败步骤，必须原样保持「失败」这个事实。
//
// 如果把沿用一律记成成功，恢复报告会把一次失败的运行洗成成功的——而
// 「当时那一步到底成没成」正是恢复要回答的问题。
func TestExecute_CarriedKeepsOriginalOutcome(t *testing.T) {
	e := &Executor{
		Reg: newTestRegistry(), Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{},
		Carried: map[string]types.StepResult{
			"s1": {StepID: "s1", Status: types.StepFailed, Outcome: types.OutcomeFailed, Error: "当时炸了"},
		},
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "fail"},
		{ID: "s2", Tool: "counter", DependsOn: []string{"s1"}},
	}}
	res := e.Execute(context.Background(), plan, "t1", "auto", false)

	if res.ByID["s1"].Status != types.StepFailed || res.ByID["s1"].Error != "当时炸了" {
		t.Errorf("沿用的失败步骤被改写了: %+v", res.ByID["s1"])
	}
	if res.Failed != 1 {
		t.Errorf("沿用下来的失败仍应计入失败数，实际 %d", res.Failed)
	}
	// 依赖一个「当时失败」的步骤：这次仍然被跳过——这正是当时的前提，不是恢复坏了。
	if res.ByID["s2"].Status != types.StepSkipped {
		t.Errorf("依赖未满足应跳过，实际 %+v", res.ByID["s2"])
	}
	if res.ByID["s2"].Carried {
		t.Error("被跳过的步骤不是沿用——它这次是被判定的，不是沿用的")
	}
}

// 沿用的步骤也要写进 Sink，而且只写一次。
//
// 两条理由：① 运行日志要能回答「这次到底跑了哪几步」，漏掉沿用步会让日志看起来
// 少了几步；② 若沿用步既预置又被执行循环再写一次，日志里就会出现两条同名记录，
// 读的人会以为它跑了两次。
func TestExecute_CarriedStepsReachSinkExactlyOnce(t *testing.T) {
	sink := &countingSink{}
	e := &Executor{
		Reg: newTestRegistry(), Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{},
		Carried: map[string]types.StepResult{
			"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		},
		Sink: sink,
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "counter"},
		{ID: "s2", Tool: "counter"},
	}}
	e.Execute(context.Background(), plan, "t1", "auto", false)

	ids := sink.ids()
	if ids["s1"] != 1 || ids["s2"] != 1 {
		t.Errorf("每步应各落一条日志，实际 %v", ids)
	}
}

// 没有 Carried 时行为必须和以前一模一样：这是「加功能」而不是「改行为」。
//
// 预置循环若写错（例如把未命中的步骤也当成沿用），最典型的表现就是整条计划
// 一步都不跑却报成功——所以这里直接断言工具真被调用了。
func TestExecute_NoCarriedIsUnchanged(t *testing.T) {
	ran := 0
	reg := newTestRegistry()
	reg.MustRegister(&funcTool{name: "tally", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		ran++
		return "ok", nil
	}})
	e := &Executor{Reg: reg, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	plan := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "tally"}, {ID: "s2", Tool: "tally"}}}
	res := e.Execute(context.Background(), plan, "t1", "auto", false)

	if ran != 2 {
		t.Errorf("无 Carried 时每步都应执行，实际执行 %d 次", ran)
	}
	if res.Carried != 0 {
		t.Errorf("沿用步数应为 0，实际 %d", res.Carried)
	}
}

// 预置必须发生在启动 goroutine **之前**。
//
// 顺序错了，依赖沿用步骤的下游会去等一个永远不会关闭的完成信号——
// 这不是「偶尔失败」，是死等（本用例会以超时暴露它）。所以断言的是
// **整体能在时限内返回**，而不是某一步的结果。
func TestExecute_CarriedReleasesDependentsPromptly(t *testing.T) {
	e := &Executor{
		Reg: newTestRegistry(), Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{},
		Carried: map[string]types.StepResult{
			"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
			"s2": {StepID: "s2", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		},
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "counter"},
		{ID: "s2", Tool: "counter", DependsOn: []string{"s1"}},
		{ID: "s3", Tool: "counter", DependsOn: []string{"s2"}},
	}}
	done := make(chan *Result, 1)
	go func() { done <- e.Execute(context.Background(), plan, "t1", "auto", false) }()
	select {
	case res := <-done:
		if res.Carried != 2 || res.Succeeded != 3 {
			t.Errorf("沿用 2 步、总成功 3 步，实际沿用 %d 成功 %d", res.Carried, res.Succeeded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("执行没有返回：沿用步骤的完成信号没有放行下游（预置晚于 goroutine 启动）")
	}
}

// 进度事件里「沿用」不能混进「完成」。
//
// 进度条是给人看「跑到哪了」的，把沿用算成刚跑完会让人以为任务正在推进，
// 而实际上它一步都没动。
func TestExecute_CarriedProgressIsDistinguished(t *testing.T) {
	n := &recordingNotifier{}
	e := &Executor{
		Reg: newTestRegistry(), Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: n,
		Carried: map[string]types.StepResult{
			"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		},
	}
	plan := types.Plan{Steps: []types.Step{{ID: "s1", Tool: "counter"}, {ID: "s2", Tool: "counter"}}}
	e.Execute(context.Background(), plan, "t1", "auto", false)

	joined := ""
	for _, ev := range n.progress {
		joined += ev.Message + "\n"
	}
	if !strings.Contains(joined, "沿用") {
		t.Errorf("进度里应出现「沿用」字样，实际：\n%s", joined)
	}
}
