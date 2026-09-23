package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// xTool 测试用最小工具。
type xTool struct {
	name string
	fn   func(ctx context.Context, args map[string]any) (any, error)
}

func (t *xTool) Name() string                                             { return t.name }
func (t *xTool) Description() string                                      { return "crosscheck tool " + t.name }
func (t *xTool) Schema() map[string]any                                   { return map[string]any{"type": "object"} }
func (t *xTool) Permission() types.Permission                             { return types.PermissionReadOnly }
func (t *xTool) Execute(c context.Context, a map[string]any) (any, error) { return t.fn(c, a) }

// xReporter 能报告"跑完了但没做成"或"跑成了但结果为空"的工具。
//
// 这两种性质**只有工具自己知道**（原因在 payload 里，执行器看不见），所以
// 判空/判失败都走 OutcomeReporter，而不是让执行器去猜返回值的形状。
type xReporter struct {
	xTool
	oc   types.StepOutcome
	note string
}

func (t *xReporter) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	return t.oc, t.note
}

// 计数只有一份实现——这条断言是它的正面防线。
//
// 执行器汇总（Result.Succeeded/Failed/Skipped/Empty/Carried）与回放侧的 countSteps
// 是两处代码算同一件事。一旦判据分叉，"记录里说成功 3 步、对比时算出成功 2 步"
// 这种自相矛盾迟早会出现，而读的人只会更不信这张表。
//
// 所以这里不构造理想化的 StepResult，而是**真跑一次执行器**，再拿它的汇总数
// 与 countSteps 的返回值逐项对照。最容易分叉的一项是"跑完了但业务上失败"
// （Outcome=failed 而 Status=succeeded）——按 Status 算会把它洗成成功。
func TestCountSteps_AgreesWithExecutorSummary(t *testing.T) {
	reg := registry.New()
	reg.MustRegister(&xTool{name: "ok", fn: func(ctx context.Context, args map[string]any) (any, error) {
		return map[string]any{"v": 1}, nil
	}})
	reg.MustRegister(&xReporter{
		xTool: xTool{name: "empty", fn: func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"items": []any{}}, nil
		}},
		oc: types.OutcomeEmpty,
	})
	reg.MustRegister(&xReporter{
		xTool: xTool{name: "reporter", fn: func(ctx context.Context, args map[string]any) (any, error) {
			return map[string]any{"exit": 1}, nil
		}},
		oc: types.OutcomeFailed, note: "退出码 1",
	})
	reg.MustRegister(&xTool{name: "boom", fn: func(ctx context.Context, args map[string]any) (any, error) {
		return nil, errors.New("炸了")
	}})

	e := &agent.Executor{
		Reg: reg, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: agent.NopNotifier{},
		// s1 沿用上次结果：验证沿用计数两边一致。
		Carried: map[string]types.StepResult{
			"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		},
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "ok"},
		{ID: "s2", Tool: "empty"},
		{ID: "s3", Tool: "reporter"},
		{ID: "s4", Tool: "boom"},
		{ID: "s5", Tool: "ok", DependsOn: []string{"s4"}}, // 依赖失败 → 跳过
	}}
	res := e.Execute(context.Background(), plan, "t-x", string(types.ModeAuto), false)

	s, f, sk, empty, carried := countSteps(res.Steps)
	if s != res.Succeeded {
		t.Errorf("成功数分叉：countSteps=%d，执行器=%d", s, res.Succeeded)
	}
	if f != res.Failed {
		t.Errorf("失败数分叉：countSteps=%d，执行器=%d（Outcome=failed 是这里最容易漏的一种）", f, res.Failed)
	}
	if sk != res.Skipped {
		t.Errorf("跳过数分叉：countSteps=%d，执行器=%d", sk, res.Skipped)
	}
	if empty != res.Empty {
		t.Errorf("空结果数分叉：countSteps=%d，执行器=%d", empty, res.Empty)
	}
	if carried != res.Carried {
		t.Errorf("沿用数分叉：countSteps=%d，执行器=%d", carried, res.Carried)
	}

	// 顺带钉住绝对值：这个用例覆盖了五类结果，任何一类被漏掉都会在这里露出来。
	if res.Carried != 1 || res.Empty != 1 || res.Skipped != 1 || res.Failed != 2 || res.Succeeded != 2 {
		t.Errorf("用例本身应覆盖 沿用1/空1/跳过1/失败2/成功2，实际 沿用%d/空%d/跳过%d/失败%d/成功%d",
			res.Carried, res.Empty, res.Skipped, res.Failed, res.Succeeded)
	}
}
