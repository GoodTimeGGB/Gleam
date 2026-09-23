package agent

import (
	"context"
	"encoding/json"
	"testing"

	"gleam/internal/llm"
	"gleam/pkg/types"
)

// 任务绑定配置快照（P2-1）：结果里要留下"这次是用什么跑的"。
//
// 关键在于**取值的时机**。收尾时再取，记下的会是一个从未完整生效过的配置：
// 任务跑到一半用户改了模型，前半段用旧的、后半段用新的，而记录里只剩新的那一套——
// 于是复盘时看到的"当时的配置"，其实是一次从未存在过的组合。

// 任务启动后改配置，落进结果的快照必须还是启动时那一套。
func TestRunGoal_ConfigSnapshotFrozenAtStart(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
	})
	f.a.Cfg.LLM.Tiers = map[string]string{"fast": "启动时的档位"}

	// 计划是脚本化的，所以第一次落到 handler 的调用发生在规划之后——
	// 正好是"任务跑起来了，用户才去改设置"这个时点。
	mutated := false
	f.llm.SetHandler(func(req llm.ChatRequest) string {
		if !mutated {
			mutated = true
			f.a.Cfg.LLM.Model = "改过的模型"
			f.a.Cfg.Agent.MaxConcurrency = 99
			f.a.Cfg.LLM.Tiers["fast"] = "改过的档位"
		}
		return `{"score":95,"verdict":"done","reason":"已回复"}`
	})

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼"})
	if !mutated {
		t.Fatal("前置条件不成立：中途改配置的钩子没被触发")
	}
	// 先确认活配置确实变了，否则下面的断言可能只是"改了个不生效的地方"。
	if f.a.Cfg.LLM.Model != "改过的模型" {
		t.Fatal("活配置没被改到，本测试失去意义")
	}

	if res.ConfigSnapshot == nil {
		t.Fatal("结果里必须带配置快照，否则两条历史结果之间不可比")
	}
	if res.ConfigSnapshot.Model == "改过的模型" {
		t.Error("快照取到了收尾时的模型——必须以任务启动时为准")
	}
	if res.ConfigSnapshot.MaxConcurrency == 99 {
		t.Error("中途改的并发改写了这次任务的记录")
	}
	if got := res.ConfigSnapshot.Tiers["fast"]; got != "启动时的档位" {
		t.Errorf("快照的 Tiers 跟着活配置变了（= %q）——必须是深拷贝", got)
	}
}

// 快照必须真的能落盘再读回来。
//
// 断言"结果结构体里有值"不够：json tag 写错、或者 omitempty 用在了指针上，
// 落盘之后就成了空——而内存里的结构体照样是对的。这是"记了口径却没记下来"的另一种形态：
// 单测全绿，历史文件里什么都没有。
func TestRunGoal_ConfigSnapshotSurvivesRoundTrip(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	f.a.Cfg.LLM.Model = "落盘模型"
	f.a.Cfg.Agent.MaxSteps = 21

	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼"})

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back types.GoalResult
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ConfigSnapshot == nil {
		t.Fatalf("落盘再读回后快照丢了（任务记录里不会有这一段）:\n%s", b)
	}
	if back.ConfigSnapshot.Model != "落盘模型" {
		t.Errorf("读回后的模型 = %q，应为 落盘模型", back.ConfigSnapshot.Model)
	}
	if back.ConfigSnapshot.MaxSteps != 21 {
		t.Errorf("读回后的最大步数 = %d，应为 21", back.ConfigSnapshot.MaxSteps)
	}
}
