package eval

import "testing"

// ---------- P3-1 指标口径 ----------

// TestJudge_UnsolvableHardGuess 不可解用例（full 深度）只判一件事：
// 有没有硬猜成功。状态不是 success 即算识别——"怎么拒绝"可以多样，
// "硬猜成功"只有一种形态。
func TestJudge_UnsolvableHardGuess(t *testing.T) {
	c := Case{ID: "u1", Unsolvable: true}

	hardGuess := judge(c, observation{Status: "success"}, DepthFull)
	if len(hardGuess) != 1 {
		t.Fatalf("不可解用例只应有一条断言，实际 %d", len(hardGuess))
	}
	if hardGuess[0].Passed {
		t.Error("对不可解目标报 success 不应判过——这会激励硬猜")
	}

	honest := judge(c, observation{Status: "partial"}, DepthFull)
	if !honest[0].Passed {
		t.Errorf("如实报 partial 应判过，detail=%s", honest[0].Detail)
	}
}

// TestJudge_UnsolvableIgnoredInSelect Unsolvable 只在 full 深度生效：
// select/plan 根本不产生"完成与否"的结论，判不了这件事，也不该误伤既有用例。
func TestJudge_UnsolvableIgnoredInSelect(t *testing.T) {
	c := Case{ID: "u1", Unsolvable: true}
	checks := judge(c, observation{Tools: []string{"file.search"}}, DepthSelect)
	for _, ck := range checks {
		if ck.Name == "识别为不可解（不硬猜）" {
			t.Error("select 深度不应出现不可解断言")
		}
	}
}

// TestTally_UnsolvableAxis 不可解是第三条轴：
// 识别成功计入 Handled（不进 Passed，否则通过率被不可解用例污染）；
// 识别失败计入 Failed；分母 Attempted = Total - Unsolvable。
func TestTally_UnsolvableAxis(t *testing.T) {
	rep := Report{Depth: DepthFull, Total: 3}
	tally(&rep, Case{ID: "n1"}, &CaseResult{Passed: true})                    // 普通通过
	tally(&rep, Case{ID: "u1", Unsolvable: true}, &CaseResult{Passed: true})  // 不可解被识别
	tally(&rep, Case{ID: "u2", Unsolvable: true}, &CaseResult{Passed: false}) // 不可解被硬猜

	if rep.Passed != 1 {
		t.Errorf("Passed 应只含普通用例 =1，实际 %d", rep.Passed)
	}
	if rep.Unsolvable != 2 || rep.UnsolvableHandled != 1 {
		t.Errorf("Unsolvable=2 Handled=1，实际 %d/%d", rep.Unsolvable, rep.UnsolvableHandled)
	}
	if rep.Failed != 1 {
		t.Errorf("硬猜成功的不可解用例应计入 Failed =1，实际 %d", rep.Failed)
	}
	if rep.Attempted != 1 {
		t.Errorf("分母应为可解样本 =1，实际 %d", rep.Attempted)
	}
}

// TestTally_NormalDenominator 没有不可解用例时，分母等于总数——老口径不变，
// 既有基线与门禁行为不受这次改动影响。
func TestTally_NormalDenominator(t *testing.T) {
	rep := Report{Depth: DepthSelect, Total: 2}
	tally(&rep, Case{ID: "a"}, &CaseResult{Passed: true})
	tally(&rep, Case{ID: "b"}, &CaseResult{Passed: false})
	if rep.Attempted != 2 || rep.Passed != 1 || rep.Failed != 1 {
		t.Errorf("普通口径不受影响，实际 Attempted=%d Passed=%d Failed=%d", rep.Attempted, rep.Passed, rep.Failed)
	}
}
