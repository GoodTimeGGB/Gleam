package eval

import "testing"

// 分层校验：非法 layer 必须在 Validate 拒绝，不能等到运行时静默跑错层。
func TestValidate_LayerInvalid(t *testing.T) {
	bad := []Case{{ID: "x", Goal: "g", WantTools: []string{"file.read"}, Layer: "premium"}}
	if err := Validate(bad); err == nil {
		t.Fatal("非法 layer 应被拒绝")
	}
	for _, l := range []string{"", "smoke", "regression", "edge", "adversarial", "holdout"} {
		ok := []Case{{ID: "x", Goal: "g", WantTools: []string{"file.read"}, Layer: l}}
		if err := Validate(ok); err != nil {
			t.Errorf("layer %q 不应被拒绝: %v", l, err)
		}
	}
}

// 空层回落 regression；FilterLayer 按 有效分层 过滤。
func TestFilterLayer(t *testing.T) {
	cases := []Case{
		{ID: "s1", Goal: "g", Layer: "smoke"},
		{ID: "s2", Goal: "g", Layer: "smoke"},
		{ID: "r1", Goal: "g"}, // 空 = regression
		{ID: "h1", Goal: "g", Layer: "holdout"},
	}
	if got := LayerOf(cases[2]); got != LayerRegression {
		t.Errorf("空层应回落 regression，实际 %s", got)
	}
	smoke := FilterLayer(cases, LayerSmoke)
	if len(smoke) != 2 {
		t.Errorf("smoke = %d 条，应为 2", len(smoke))
	}
	if len(FilterLayer(cases, LayerHoldout)) != 1 {
		t.Error("holdout 应为 1 条")
	}
	if got := FilterLayer(cases, ""); len(got) != 4 {
		t.Errorf("空层 = 全部，实际 %d 条", len(got))
	}
}

// 保留池聚合：单条结果不进调试视图，但聚合必须留在报告里。
func TestTally_HoldoutAggregate(t *testing.T) {
	rep := Report{Depth: DepthSelect, Total: 3}
	h1 := CaseResult{ID: "h1", Holdout: true, Passed: true}
	h2 := CaseResult{ID: "h2", Holdout: true, Passed: false}
	r1 := CaseResult{ID: "r1", Passed: true}
	tally(&rep, Case{ID: "h1", Layer: "holdout"}, &h1)
	tally(&rep, Case{ID: "h2", Layer: "holdout"}, &h2)
	tally(&rep, Case{ID: "r1"}, &r1)
	if rep.HoldoutTotal != 2 || rep.HoldoutPassed != 1 {
		t.Errorf("保留池聚合 = %d/%d，应 1/2", rep.HoldoutPassed, rep.HoldoutTotal)
	}
	// 保留池照常计入总数：它是真实质量，只是不看单条
	if rep.Passed != 2 || rep.Failed != 1 {
		t.Errorf("Passed/Failed = %d/%d，应 2/1", rep.Passed, rep.Failed)
	}
}
