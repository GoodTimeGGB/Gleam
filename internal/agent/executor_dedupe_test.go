package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// countingTool 记录真实执行次数的只读工具。
type countingTool struct {
	calls int64
	err   error
}

func (c *countingTool) Name() string        { return "counter" }
func (c *countingTool) Description() string { return "counting tool" }
func (c *countingTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (c *countingTool) Permission() types.Permission { return types.PermissionReadOnly }
func (c *countingTool) Execute(_ context.Context, _ map[string]any) (any, error) {
	atomic.AddInt64(&c.calls, 1)
	if c.err != nil {
		return nil, c.err
	}
	return map[string]any{"n": 1}, nil
}

func dedupeRegistry(t *countingTool) *registry.Registry {
	r := registry.New()
	r.MustRegister(t)
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) {
			return map[string]any{"text": args["text"]}, nil
		}})
	return r
}

func dedupePlan() types.Plan {
	return types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "counter", Args: map[string]any{"k": "v"}},
		{ID: "s2", Tool: "counter", Args: map[string]any{"k": "v"}}, // 与 s1 完全相同
		{ID: "s3", Tool: "reply", Args: map[string]any{"text": "ok"}, DependsOn: []string{"s1", "s2"}},
	}}
}

// TestExecutor_DedupesIdenticalReadOnlyCalls 相同的只读调用只真正执行一次。
func TestExecutor_DedupesIdenticalReadOnlyCalls(t *testing.T) {
	tool := &countingTool{}
	e := &Executor{
		Reg: dedupeRegistry(tool), Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, Dedupe: true, StepTimeout: 2 * time.Second,
	}
	res := e.Execute(context.Background(), dedupePlan(), "t1", string(types.ModeAuto), false)
	if res.Succeeded != 3 {
		t.Fatalf("三个步骤都应成功: %+v", res.Steps)
	}
	if got := atomic.LoadInt64(&tool.calls); got != 1 {
		t.Fatalf("相同只读调用应只执行 1 次，实际 %d", got)
	}
	// s1 与 s2 无依赖会并发执行，谁先抢到执行权是不确定的——
	// 所以只能断言"恰好一个真跑、另一个复用"，不能断言具体是哪一个。
	if res.ByID["s1"].Deduped == res.ByID["s2"].Deduped {
		t.Fatalf("应恰好一个步骤被标记为去重，实际 s1=%v s2=%v",
			res.ByID["s1"].Deduped, res.ByID["s2"].Deduped)
	}
}

// TestExecutor_DedupeCountsReported 去重次数应上报给消耗看板。
func TestExecutor_DedupeCountsReported(t *testing.T) {
	tool := &countingTool{}
	var deduped, toolCalls int
	e := &Executor{
		Reg: dedupeRegistry(tool), Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, Dedupe: true, StepTimeout: 2 * time.Second,
		OnUsage: func(_ string, tc, _, dd int) { toolCalls += tc; deduped += dd },
	}
	e.Execute(context.Background(), dedupePlan(), "t1", string(types.ModeAuto), false)
	if deduped != 1 {
		t.Fatalf("应上报 1 次去重，实际 %d", deduped)
	}
	if toolCalls != 2 { // counter 1 次 + reply 1 次
		t.Fatalf("工具调用次数应为 2，实际 %d", toolCalls)
	}
}

// TestExecutor_DedupeDisabled 关闭去重视为关闭优化，两次都真正执行。
func TestExecutor_DedupeDisabled(t *testing.T) {
	tool := &countingTool{}
	e := &Executor{
		Reg: dedupeRegistry(tool), Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, Dedupe: false, StepTimeout: 2 * time.Second,
	}
	e.Execute(context.Background(), dedupePlan(), "t1", string(types.ModeAuto), false)
	if got := atomic.LoadInt64(&tool.calls); got != 2 {
		t.Fatalf("关闭去重时应执行 2 次，实际 %d", got)
	}
}

// TestExecutor_SkipsRepeatedFailedCall 已失败过的相同调用不再重复消耗。
//
// 断言刻意做成**顺序无关**的：s1 与 s2 之间没有依赖，执行器会并发起跑它们
// （MaxConcurrency 为 0 时取默认 8），谁先抢到 single-flight 由调度决定。
// 原先断言"被去重的一定是 s2"，只在 s1 抢到时成立——机器一忙就假红。
// 真正该钉住的契约是：两次完全相同的调用，恰好一次真执行、一次去重跳过。
func TestExecutor_SkipsRepeatedFailedCall(t *testing.T) {
	tool := &countingTool{err: errors.New("boom")}
	e := &Executor{
		Reg: dedupeRegistry(tool), Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, Dedupe: true, StepTimeout: 2 * time.Second,
	}
	res := e.Execute(context.Background(), dedupePlan(), "t1", string(types.ModeAuto), false)
	if got := atomic.LoadInt64(&tool.calls); got != 1 {
		t.Fatalf("失败调用不应重复执行，实际执行 %d 次", got)
	}

	executed, deduped := 0, 0
	for _, id := range []string{"s1", "s2"} {
		r := res.ByID[id]
		switch {
		case r.Deduped:
			if r.Status != types.StepFailed {
				t.Errorf("%s 被去重跳过时应记为失败，实际 %s", id, r.Status)
			}
			if !strings.Contains(r.Error, "已跳过") {
				t.Errorf("%s 的跳过原因应说明是重复的失败调用，实际 %q", id, r.Error)
			}
			deduped++
		case r.Status == types.StepFailed:
			executed++
		}
	}
	if executed != 1 || deduped != 1 {
		t.Fatalf("两次相同调用应恰好一次真执行、一次去重跳过，实际 executed=%d deduped=%d", executed, deduped)
	}
}

// TestExecutor_DedupeKeepsDistinctArgs 参数不同不视为重复。
func TestExecutor_DedupeKeepsDistinctArgs(t *testing.T) {
	tool := &countingTool{}
	e := &Executor{
		Reg: dedupeRegistry(tool), Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, Dedupe: true, StepTimeout: 2 * time.Second,
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "counter", Args: map[string]any{"k": "a"}},
		{ID: "s2", Tool: "counter", Args: map[string]any{"k": "b"}},
	}}
	e.Execute(context.Background(), plan, "t1", string(types.ModeAuto), false)
	if got := atomic.LoadInt64(&tool.calls); got != 2 {
		t.Fatalf("参数不同应各执行一次，实际 %d", got)
	}
}
