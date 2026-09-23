package main

import (
	"strings"
	"testing"

	"gleam/internal/agent"
	"gleam/internal/harness/growth"
)

// report 造一份指定结论的体检报告。
func report(passed, warned, failed int, items ...agent.ReadinessItem) agent.ReadinessReport {
	return agent.ReadinessReport{
		Total: passed + warned + failed, Passed: passed, Warned: warned, Failed: failed,
		Items: items,
	}
}

// TestStrictErr_OnlyFailsBlock 门禁只卡「不合格」。
//
// 「待改进」是"机制在但没用起来"（没配档位、还没沉淀技能），属于使用进度不是缺陷；
// 拿它拦发布只会逼人加个开关把整个检查废掉，那就白做了。
func TestStrictErr_OnlyFailsBlock(t *testing.T) {
	pass := agent.ReadinessItem{Index: 1, Title: "Runtime 重复建设", Status: agent.ReadyPass}
	warn := agent.ReadinessItem{Index: 3, Title: "模型与能力锁定", Status: agent.ReadyWarn}
	fail := agent.ReadinessItem{Index: 7, Title: "模型调用缓存不可控", Status: agent.ReadyFail}

	cases := []struct {
		name   string
		rep    agent.ReadinessReport
		strict bool
		want   bool // 是否应报错
	}{
		{"不开启 strict 永不报错", report(1, 1, 1, pass, warn, fail), false, false},
		{"全部通过", report(3, 0, 0, pass), true, false},
		{"只有待改进：放行", report(2, 1, 0, pass, warn), true, false},
		{"有不合格：拦住", report(2, 0, 1, pass, fail), true, true},
	}
	for _, c := range cases {
		err := strictErr(c.rep, c.strict)
		if (err != nil) != c.want {
			t.Errorf("%s：strictErr = %v，期望报错=%v", c.name, err, c.want)
		}
	}
}

// TestStrictErr_MessageIsActionable 报错信息要点出是哪几项，否则 CI 日志里只看到"失败了"。
func TestStrictErr_MessageIsActionable(t *testing.T) {
	rep := report(1, 0, 2,
		agent.ReadinessItem{Index: 1, Title: "Runtime 重复建设", Status: agent.ReadyFail},
		agent.ReadinessItem{Index: 7, Title: "模型调用缓存不可控", Status: agent.ReadyFail},
	)
	err := strictErr(rep, true)
	if err == nil {
		t.Fatal("应报错")
	}
	msg := err.Error()
	for _, want := range []string{"2", "1. Runtime 重复建设", "7. 模型调用缓存不可控", "--strict"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错信息应包含 %q，实际：%s", want, msg)
		}
	}
}

// TestUserSignalReport_DeliveryLine 交付侧一次性那两行要报出来，且分母与上面不同。
//
// 这是口径断言，不是文案断言：两个比率的分母不一样（用户信号按"完成 + 中断"，
// 交付口径只按走过工作循环的完成任务），而分母写错在终端上看起来一模一样。
func TestUserSignalReport_DeliveryLine(t *testing.T) {
	out := userSignalReport(growth.Stats{
		TotalTasks: 6, AbortedTasks: 1, RetryCount: 1,
		RetryRate: 1.0 / 7, AbortRate: 1.0 / 7,
		FirstPassCount: 4, FirstPassBase: 6, FirstPassRate: 4.0 / 6,
		ReworksTotal: 3,
	})
	for _, want := range []string{
		"分母 = 完成 6 + 中断 1",
		"首次验收通过率 67%（4/6，仅工作模式）",
		"累计返工 3 轮",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q，实际：\n%s", want, out)
		}
	}
}

// TestUserSignalReport_NoDeliveryBase 分母为零时不报首次通过率：0/0 不是 0%。
//
// "没有样本"与"通过率是零"是两件事，混在一起报，看的人会以为系统一次都没做对过。
func TestUserSignalReport_NoDeliveryBase(t *testing.T) {
	out := userSignalReport(growth.Stats{TotalTasks: 5, AbortRate: 0})
	if strings.Contains(out, "首次验收通过率") {
		t.Errorf("没有记过交付口径的任务时不该报通过率（0/0 不是 0%%），实际：\n%s", out)
	}
	if !strings.Contains(out, "用户重试率") {
		t.Errorf("用户信号应照常报，实际：\n%s", out)
	}
}

// TestUserSignalReport_SmallSampleIsQuiet 样本太少时整段不报：噪音不是信号。
func TestUserSignalReport_SmallSampleIsQuiet(t *testing.T) {
	if out := userSignalReport(growth.Stats{TotalTasks: 2, AbortedTasks: 1}); out != "" {
		t.Errorf("样本不足时不该报比率，实际输出：%q", out)
	}
}
