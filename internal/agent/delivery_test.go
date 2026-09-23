package agent

import (
	"context"
	"testing"

	"gleam/internal/harness/growth"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// 交付侧口径（P5）：产物是不是**一次就合格**。
//
// 与 StepResult.Retried 的区别在层级：那个是步骤级的（这一步重试了两次），
// 回答不了"整个目标返工了几轮"。重试三次才凑出来的结果与一次做对的结果，
// 在成功率上完全一样——成功率这个数字看不见差别，成本与可信度上却完全是两回事。

// 一次做对：第一轮尝试就走完流程并通过完成判定。
func TestRunGoal_FirstPassOnFirstAttempt(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", res)
	}
	if !res.FirstPass {
		t.Error("第一轮就通过，FirstPass 应为 true")
	}
	if res.Reworks != 0 {
		t.Errorf("Reworks = %d，一次做对应为 0", res.Reworks)
	}
}

// 返工一次才通过：FirstPass=false、Reworks=1。
//
// 这是本批口径的核心断言。原先只有步骤级的 Retried，
// "整个目标重做了一遍"这件事在数据里根本不存在——
// 于是"一次就合格的比例"没有任何办法回答。
func TestRunGoal_ReworkCountedWhenReplanNeeded(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		// 第一轮：用了必定失败的工具，反思判 replan
		planScript(`{"steps":[{"id":"s1","description":"调用会失败的工具","tool":"fail","args":{}}]}`),
		reflectScript(40, "replan", "步骤失败"),
		// 第二轮：改对了
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "跑一次"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", res)
	}
	if res.FirstPass {
		t.Error("返工一次才成，FirstPass 应为 false（成功 ≠ 一次就合格）")
	}
	if res.Reworks != 1 {
		t.Errorf("Reworks = %d，应为 1", res.Reworks)
	}
}

// 规划校验失败导致的重规划**不算返工**：那一步什么都没执行，谈不上"重做"。
//
// 但 FirstPass 仍然落成 false——"首次尝试"包含了"计划第一次就可用"这件事，
// 端到端的一次性指标不该把规划阶段的失败藏起来。
// 两个字段因此回答的是两个不同的问题：Reworks 看交付成本，FirstPass 看端到端一次通过。
func TestRunGoal_PlanValidationRetryIsNotRework(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"用不存在的工具","tool":"no_such_tool","args":{}}]}`),
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "跑一次"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", res)
	}
	if res.Reworks != 0 {
		t.Errorf("Reworks = %d，应为 0：计划校验失败时没有任何执行，谈不上返工", res.Reworks)
	}
	if res.FirstPass {
		t.Error("FirstPass 应为 false：第一次尝试连计划都没通过，不是一次就合格")
	}
}

// 第一轮走到完成判定、但最终只是"部分完成"时不算一次就合格。
//
// 只看"第几轮"不够：完成判定通过之后 applyAcceptanceVerdict 仍可能把状态
// 校正成部分完成（有步骤失败、产物核对不过）。那不是一次就合格。
func TestRunGoal_PartialIsNotFirstPass(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[
			{"id":"s1","description":"会失败的一步","tool":"fail","args":{}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "自认完成"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "跑两步"})
	if res.Status != types.GoalPartial {
		t.Fatalf("有步骤失败时应落成部分完成: %+v", res)
	}
	if res.FirstPass {
		t.Error("部分完成不是一次就合格，FirstPass 应为 false")
	}
}

// stamp 只在真正进过工作循环时写。
//
// 对话模式没有"轮"的概念，两个字段由对话管线按自己的核对结论给；
// 无条件写会把那边已经给出的结论覆盖成零值——"核对通过的对话"于是被统计成
// "没通过首次验收"，比率被自己的记账方式压下去。
func TestGoalProgress_StampSkipsChatPath(t *testing.T) {
	res := &types.GoalResult{Status: types.GoalSuccess, FirstPass: true, Reworks: 3}
	(&goalProgress{}).stamp(res) // attempts == 0：没进过循环
	if !res.FirstPass || res.Reworks != 3 {
		t.Errorf("没进过循环时不应改写交付口径: FirstPass=%v Reworks=%d", res.FirstPass, res.Reworks)
	}
}

// 进过循环、第一轮就走到完成判定，但最终不是成功 → 不是一次就合格。
func TestGoalProgress_StampRequiresSuccess(t *testing.T) {
	res := &types.GoalResult{Status: types.GoalPartial}
	(&goalProgress{attempts: 1, firstRound: true}).stamp(res)
	if res.FirstPass {
		t.Error("最终不是成功，FirstPass 应为 false")
	}
	if res.Reworks != 0 {
		t.Errorf("Reworks = %d，应为 0", res.Reworks)
	}
}

// 对话模式不留交付侧口径：没有"产物"可核，也没有"轮"。
func TestRunGoal_ChatModeLeavesDeliveryUnset(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "chat", Texts: []string{"你好呀，有什么可以帮你的？"}},
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "你好", TaskMode: types.TaskChat})
	if res.FirstPass || res.Reworks != 0 {
		t.Errorf("对话模式不该带交付侧口径: FirstPass=%v Reworks=%d", res.FirstPass, res.Reworks)
	}
}

// ---------- 接线断言 ----------
//
// 口径算对了不等于**记下来了**。`goalProgress.stamp` 正确、`growth.Stats` 汇总正确，
// 但中间"写进成长记录"少接一根线，统计的分母就永远是空的——而所有单元测试照样全绿。
// 这是 P4-2b（VerifyLLM 接没接进主循环）栽过的同一个坑，所以这里必须端到端跑一遍，
// 去读真正落盘的那条记录，而不是只看返回的结构体。

// 工作模式：返工一次才成的任务，记录里必须是 Delivery=true / FirstPass=false / Reworks=1。
func TestRunGoal_GrowthEntryCarriesDelivery(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"会失败的一步","tool":"fail","args":{}}]}`),
		reflectScript(40, "replan", "步骤失败"),
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	g, err := growth.Open(f.a.Cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Growth = g

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "跑一次"})
	if res.Reworks != 1 {
		t.Fatalf("前置条件不成立：结果里 Reworks = %d，应为 1", res.Reworks)
	}

	var got *growth.Entry
	for _, e := range g.All() {
		if e.Type == "task_completed" {
			c := e
			got = &c
		}
	}
	if got == nil {
		t.Fatal("没有落下 task_completed 记录")
	}
	if !got.Delivery {
		t.Error("工作模式的任务必须标 Delivery，否则交付口径的分母永远是空的")
	}
	if got.Reworks != res.Reworks {
		t.Errorf("记录里的 Reworks = %d，结果里是 %d——两者必须一致", got.Reworks, res.Reworks)
	}
	if got.FirstPass != res.FirstPass {
		t.Errorf("记录里的 FirstPass = %v，结果里是 %v", got.FirstPass, res.FirstPass)
	}
	if got.FirstPass {
		t.Error("返工一次才成的任务不该记成一次通过")
	}
}

// 一次通过的任务，记录里也必须是 FirstPass=true。
//
// 只把"返工"那一半接对不算接对：这个字段接错方向的后果不是"数字偏了"，
// 而是"首次通过率永远是 0"——一个永远为零的指标比没有指标更糟，它会让人以为
// 系统一次都没做对过。
func TestRunGoal_GrowthEntryFirstPass(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	g, err := growth.Open(f.a.Cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Growth = g

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼"})
	if !res.FirstPass {
		t.Fatalf("前置条件不成立：结果里 FirstPass 应为 true，实际 %+v", res)
	}

	entries := g.All()
	if len(entries) == 0 {
		t.Fatal("没有落下记录")
	}
	last := entries[len(entries)-1]
	if !last.Delivery {
		t.Error("工作模式的任务应标 Delivery")
	}
	if !last.FirstPass {
		t.Error("一次通过的任务，成长记录里也该是 FirstPass=true——否则通过率永远是 0")
	}
	if last.Reworks != 0 {
		t.Errorf("记录里 Reworks = %d，应为 0", last.Reworks)
	}
}

// 对话模式：不标 Delivery。这条线接反了，对话任务就会以 FirstPass=false 混进分母，
// 把比率随使用习惯压下去——而分母被污染之后，这个指标就再也回不来了。
func TestRunGoal_ChatEntryHasNoDeliveryScope(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "chat", Texts: []string{"你好呀"}},
	})
	g, err := growth.Open(f.a.Cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.a.Growth = g

	f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "你好", TaskMode: types.TaskChat})

	entries := g.All()
	if len(entries) == 0 {
		t.Fatal("没有落下记录")
	}
	if last := entries[len(entries)-1]; last.Delivery {
		t.Error("对话模式不该标 Delivery——它会把对话任务混进交付口径的分母")
	}
}
