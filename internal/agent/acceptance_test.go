package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gleam/internal/llm"
	"gleam/pkg/types"
)

// failingLLM 反思器调用必定失败——用来复现"反思器故障回退"这条路径。
// 走的是真实链路（Evaluate → heuristicReflection），而不是直接调兜底函数。
type failingLLM struct{}

var errReflectorDown = errors.New("模拟反思器不可用")

func (failingLLM) Chat(context.Context, llm.ChatRequest) (string, error) {
	return "", errReflectorDown
}

func (failingLLM) ChatStream(context.Context, llm.ChatRequest, func(string)) (string, error) {
	return "", errReflectorDown
}

func (failingLLM) Name() string { return "failing" }

// ---------- 验收标准：干活的不能兼任验收 ----------

// reflectChecksScript 反思阶段返回带 checks 的原始 JSON（reflectScript 只覆盖 score/verdict/reason）。
func reflectChecksScript(jsonText string) llm.Scripted {
	return llm.Scripted{Kind: "reflect", Texts: []string{jsonText}}
}

func TestAcceptance_Normalized(t *testing.T) {
	got := normalizeAcceptance([]string{
		"  回复里给出不少于 3 条建议  ",
		"回复里给出不少于 3 条建议", // 重复
		"",               // 空
		"第二条",
		"第三条",
		"第四条",
		"第五条", // 超过上限 4 条
	})
	if len(got) != 4 {
		t.Fatalf("去重去空后最多保留 4 条，实际 %d：%v", len(got), got)
	}
	if got[0] != "回复里给出不少于 3 条建议" {
		t.Errorf("应去掉首尾空白，实际 %q", got[0])
	}
}

func TestAcceptance_EmptyWhenAbsent(t *testing.T) {
	if got := normalizeAcceptance(nil); len(got) != 0 {
		t.Errorf("没有验收标准时应为空，实际 %v", got)
	}
}

// TestAcceptance_PlannerParsesIt 规划器应解析出验收标准。
func TestAcceptance_PlannerParsesIt(t *testing.T) {
	p := &Planner{Reg: menuRegistry(2), MaxSteps: 5}
	plan, err := p.validate([]byte(`{"steps":[{"id":"s1","tool":"reply","args":{"text":"hi"}}],
		"acceptance":["回复里包含 3 条建议","语气保持简洁"]}`), "给点建议")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Acceptance) != 2 {
		t.Fatalf("应保留 2 条验收标准，实际 %v", plan.Acceptance)
	}
}

// TestAcceptance_OptionalForBackwardCompat 老格式（没有 acceptance）不能报错。
func TestAcceptance_OptionalForBackwardCompat(t *testing.T) {
	p := &Planner{Reg: menuRegistry(2), MaxSteps: 5}
	plan, err := p.validate([]byte(`{"steps":[{"id":"s1","tool":"reply","args":{"text":"hi"}}]}`), "x")
	if err != nil {
		t.Fatalf("缺少 acceptance 不应导致校验失败: %v", err)
	}
	if len(plan.Acceptance) != 0 {
		t.Errorf("应为空，实际 %v", plan.Acceptance)
	}
}

// TestAcceptance_ScoreFromChecks 分数由逐条判定算出，而不是采信模型的自评总分。
func TestAcceptance_ScoreFromChecks(t *testing.T) {
	cases := []struct {
		name    string
		checks  []types.CheckResult
		wantSco int
	}{
		{"全部通过", []types.CheckResult{{Passed: true}, {Passed: true}}, 100},
		{"对半", []types.CheckResult{{Passed: true}, {Passed: false}}, 50},
		{"三缺一", []types.CheckResult{{Passed: true}, {Passed: true}, {Passed: false}}, 66},
		{"全不过", []types.CheckResult{{Passed: false}, {Passed: false}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoreFromChecks(tc.checks); got != tc.wantSco {
				t.Errorf("score = %d, want %d", got, tc.wantSco)
			}
		})
	}
}

// TestAcceptance_MissingCheckCountsAsFailed 漏判的条目按未通过计——不能靠少判蒙混过关。
func TestAcceptance_MissingCheckCountsAsFailed(t *testing.T) {
	acc := []string{"标准A", "标准B", "标准C"}
	got := alignChecks([]types.CheckResult{{Criterion: "标准A", Passed: true}}, acc)
	if len(got) != 3 {
		t.Fatalf("应补齐到 3 条，实际 %d", len(got))
	}
	if !got[0].Passed {
		t.Error("标准A 判定通过")
	}
	if got[1].Passed || got[2].Passed {
		t.Error("未判定的标准应按未通过计")
	}
	if scoreFromChecks(got) != 33 {
		t.Errorf("1/3 通过应为 33 分，实际 %d", scoreFromChecks(got))
	}
}

// TestAcceptance_ExtraChecksDropped 模型多判的条目应丢弃，只保留定下的标准。
func TestAcceptance_ExtraChecksDropped(t *testing.T) {
	got := alignChecks([]types.CheckResult{
		{Criterion: "标准A", Passed: true},
		{Criterion: "我临时加的", Passed: true},
	}, []string{"标准A"})
	if len(got) != 1 || got[0].Criterion != "标准A" {
		t.Errorf("应只保留定下的标准，实际 %+v", got)
	}
}

// TestAcceptance_ReflectorIgnoresSelfScore 有验收标准时，模型自评的高分不作数。
func TestAcceptance_ReflectorIgnoresSelfScore(t *testing.T) {
	m := llm.NewMock()
	// 模型自信打 95 分，但三条标准只过了两条
	m.Enqueue("reflect", `{"score":95,"verdict":"done","reason":"我觉得很好",
		"checks":[{"criterion":"标准A","passed":true},{"criterion":"标准B","passed":true},{"criterion":"标准C","passed":false,"note":"缺第 3 条建议"}]}`)
	r := &Reflector{LLM: m, DoneThreshold: 80, Acceptance: []string{"标准A", "标准B", "标准C"}}
	refl := r.Evaluate(context.Background(), "给建议", mkExec(1, 0, 0), 1)
	if refl.Score != 66 {
		t.Errorf("分数应由判定结果算出（2/3=66），实际 %d", refl.Score)
	}
	if len(refl.Checks) != 3 {
		t.Fatalf("应保留 3 条判定，实际 %d", len(refl.Checks))
	}
	if refl.Checks[2].Note == "" {
		t.Error("未通过的原因要具体到能照着改")
	}
}

// TestAcceptance_NoCriteriaKeepsModelScore 没有验收标准时保持原行为（不破坏既有链路）。
func TestAcceptance_NoCriteriaKeepsModelScore(t *testing.T) {
	m := llm.NewMock()
	m.Enqueue("reflect", `{"score":88,"verdict":"done","reason":"ok"}`)
	r := &Reflector{LLM: m, DoneThreshold: 80}
	refl := r.Evaluate(context.Background(), "g", mkExec(1, 0, 0), 1)
	if refl.Score != 88 {
		t.Errorf("无验收标准时应沿用模型评分，实际 %d", refl.Score)
	}
	if len(refl.Checks) != 0 {
		t.Errorf("无验收标准时不应产生判定，实际 %+v", refl.Checks)
	}
}

// TestAcceptance_DigestListsCriteria 验收标准要出现在反思器看到的材料里。
func TestAcceptance_DigestListsCriteria(t *testing.T) {
	digest := buildDigest("目标", mkExec(1, 0, 0), 1, []string{"第一条", "第二条"}, nil)
	if !strings.Contains(digest, "验收标准") || !strings.Contains(digest, "第一条") || !strings.Contains(digest, "第二条") {
		t.Errorf("digest 应列出验收标准：\n%s", digest)
	}
}

// TestAcceptance_ReachesGoalResult 验收标准与判定结果要能一路带到任务结果里（前端要用）。
func TestAcceptance_ReachesGoalResult(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"建议一二三"}}],
			"acceptance":["回复里给出不少于 3 条建议"]}`),
		reflectChecksScript(`{"checks":[{"criterion":"回复里给出不少于 3 条建议","passed":true}],
			"verdict":"done","reason":"ok"}`),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "给我三条建议", Mode: "auto"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("全部通过应判成功，实际 %s（%s）", res.Status, res.Error)
	}
	if len(res.Acceptance) != 1 {
		t.Fatalf("验收标准应带到结果里，实际 %v", res.Acceptance)
	}
	if len(res.Checks) != 1 {
		t.Fatalf("判定结果应带到结果里，实际 %v", res.Checks)
	}
	if res.Checks[0].Criterion != "回复里给出不少于 3 条建议" {
		t.Errorf("判定应对齐到原标准，实际 %q", res.Checks[0].Criterion)
	}
}

// TestAcceptance_DoneRequiresAllPassed 模型自己说 done，但验收没全过，就不能算完成。
// 这是"干活的不能兼任验收"的落点：verdict 和 checks 出自同一个模型，但 checks 更结构化，
// 两者矛盾时以 checks 为准。
func TestAcceptance_DoneRequiresAllPassed(t *testing.T) {
	m := llm.NewMock()
	m.Enqueue("reflect", `{"verdict":"done","reason":"我觉得没问题",
		"checks":[{"criterion":"A","passed":true},{"criterion":"B","passed":false,"note":"缺 B"}]}`)
	r := &Reflector{LLM: m, DoneThreshold: 80, Acceptance: []string{"A", "B"}}
	refl := r.Evaluate(context.Background(), "g", mkExec(1, 0, 0), 1)
	if refl.Verdict == "done" {
		t.Fatalf("有标准未通过时不能判 done，实际 %q（分数 %d）", refl.Verdict, refl.Score)
	}
	if refl.Reason == "" {
		t.Error("降级后要留下原因，否则用户不知道为什么要重试")
	}
}

// TestAcceptance_DoneIndependentOfThreshold 完成阈值调低也不能让"半通过"变成完成。
// 阈值是用户可调的松紧带，验收是硬线——两者不能互相抵消。
func TestAcceptance_DoneIndependentOfThreshold(t *testing.T) {
	m := llm.NewMock()
	m.Enqueue("reflect", `{"verdict":"done","reason":"ok",
		"checks":[{"criterion":"A","passed":true},{"criterion":"B","passed":false}]}`)
	// 阈值 30，而分数是 50：按旧逻辑 50>=30 就会被当成完成
	r := &Reflector{LLM: m, DoneThreshold: 30, Acceptance: []string{"A", "B"}}
	refl := r.Evaluate(context.Background(), "g", mkExec(1, 0, 0), 1)
	if refl.Verdict == "done" {
		t.Fatalf("阈值调低不应让未全过的验收算完成，实际 %q", refl.Verdict)
	}
	if acceptanceSatisfied([]string{"A", "B"}, refl.Checks) {
		t.Error("半通过不应被判定为验收达标")
	}
}

// TestAcceptance_AllPassedKeepsDone 全部通过时一切照旧。
func TestAcceptance_AllPassedKeepsDone(t *testing.T) {
	m := llm.NewMock()
	m.Enqueue("reflect", `{"verdict":"done","reason":"ok",
		"checks":[{"criterion":"A","passed":true},{"criterion":"B","passed":true}]}`)
	r := &Reflector{LLM: m, DoneThreshold: 80, Acceptance: []string{"A", "B"}}
	refl := r.Evaluate(context.Background(), "g", mkExec(1, 0, 0), 1)
	if refl.Verdict != "done" || refl.Score != 100 {
		t.Fatalf("全部通过应保持 done/100，实际 %q/%d", refl.Verdict, refl.Score)
	}
	if !acceptanceSatisfied([]string{"A", "B"}, refl.Checks) {
		t.Error("全部通过应判定为验收达标")
	}
}

// TestAcceptance_MissingChecksBlocksCompletion 有验收标准却没判定 → 不能算通过。
//
// 这条曾经是反的（原名 TestAcceptance_SatisfiedFailsOpen，理由是"宁可放过，不要卡死"）。
// 它造成的洞：反思器一故障，heuristicReflection 给出 done/85，这里再放行，
// 一个定义了 4 条验收标准的任务就在一条都没核对的情况下以"已完成"收尾。
// 判定缺失不是"没问题"，是"没核对过"——按资料判据应进失败处理。
//
// "不要卡死"的诉求由另一条保证：applyAcceptanceVerdict 把状态落成"部分完成"而非"失败"，
// 用户看得到差的是核对这一步，任务也不会被卡在循环里（见下面两个测试）。
func TestAcceptance_MissingChecksBlocksCompletion(t *testing.T) {
	if !acceptanceSatisfied(nil, nil) {
		t.Error("没有验收标准时不应阻断完成")
	}
	if acceptanceSatisfied([]string{"A"}, nil) {
		t.Error("有验收标准却没有判定，不能算通过（判定缺失 ≠ 没问题）")
	}
	if acceptanceSatisfied([]string{"A"}, []types.CheckResult{{Criterion: "A", Passed: false}}) {
		t.Error("有未通过项时应判定为未达标")
	}
}

// TestAcceptance_HeuristicNeverDoneWithChecks 有验收标准时，本地兜底不许判 done。
// 兜底只看得到"步骤跑没跑成"，看不到"承诺的标准有没有被核对"。
func TestAcceptance_HeuristicNeverDoneWithChecks(t *testing.T) {
	exec := mkExec(2, 0, 0) // 全部成功——旧逻辑正是在这里给 done/85
	refl := heuristicReflection(exec, "（反思器调用失败）", true)
	if refl.Verdict == "done" {
		t.Fatalf("有验收标准时兜底不能判 done，实际 %q", refl.Verdict)
	}
	if refl.Score != 0 {
		t.Errorf("没核对过就没有完成度可言，分数应为 0，实际 %d", refl.Score)
	}
	if !strings.Contains(refl.Reason, "未能核对") {
		t.Errorf("原因里要写明验收没能核对，实际 %q", refl.Reason)
	}
	// 没有验收标准时维持原行为：不干预，避免把没有验收的任务也拖进重试。
	refl = heuristicReflection(exec, "", false)
	if refl.Verdict != "done" || refl.Score != 85 {
		t.Fatalf("没有验收标准时兜底应保持 done/85，实际 %q/%d", refl.Verdict, refl.Score)
	}
}

// TestAcceptance_ReflectorFailureNeverReportsSuccess 反思器整条链路故障时，
// 带验收标准的任务不能以"已完成"收尾——这是 P0-1 的端到端回归。
//
// 旧行为：heuristicReflection 给 done/85，acceptanceSatisfied 因 checks 为空返回 true，
// 于是 4 条验收标准一条都没核对，任务却报 success/85。
func TestAcceptance_ReflectorFailureNeverReportsSuccess(t *testing.T) {
	acceptance := []string{"产出里含 4 个字段", "文件名符合规范", "数值可对账", "末尾有结论"}
	exec := mkExec(3, 0, 0) // 三步全部执行成功——最容易误判成"完成"的形态

	// 走真实链路：Evaluate 内部三次回退分支都会落到 heuristicReflection
	r := &Reflector{LLM: failingLLM{}, Acceptance: acceptance}
	refl := r.Evaluate(context.Background(), "生成季度报表", exec, 1)

	if refl.Verdict == "done" {
		t.Fatalf("反思器不可用时不能判 done，实际 %q（分数 %d）", refl.Verdict, refl.Score)
	}
	if refl.Score == 85 {
		t.Errorf("反思器不可用时不该给出 85 分（那是「全部成功」的旧口径），实际 %d", refl.Score)
	}
	if len(refl.Checks) != 0 {
		t.Errorf("反思器不可用时应无判定，实际 %+v", refl.Checks)
	}
	// 第一道闸：循环门禁
	if acceptanceSatisfied(acceptance, refl.Checks) {
		t.Error("有验收标准却没判定，循环门禁不能放行")
	}
	// 第二道闸：对外状态
	res := &types.GoalResult{
		Status: types.GoalSuccess, Score: refl.Score,
		Acceptance: acceptance, Checks: refl.Checks,
	}
	applyAcceptanceVerdict(res, acceptance)
	if res.Status == types.GoalSuccess {
		t.Errorf("验收未核对却报告成功：%+v", res)
	}
	if res.Status != types.GoalPartial {
		t.Errorf("步骤其实都跑成功了，应落「部分完成」而不是 %s", res.Status)
	}
	if !strings.Contains(res.Error, "未能核对") {
		t.Errorf("结果里要说明验收没能核对，实际 %q", res.Error)
	}
}

// TestAcceptance_UncheckedStatusIsPartial 有标准却没判定 → 状态落"部分完成"并写明原因，
// 既不是"已完成"（没核对过），也不是"失败"（步骤其实都跑成功了）。
func TestAcceptance_UncheckedStatusIsPartial(t *testing.T) {
	res := &types.GoalResult{Status: types.GoalSuccess, Acceptance: []string{"A", "B"}}
	applyAcceptanceVerdict(res, []string{"A", "B"})
	if res.Status != types.GoalPartial {
		t.Fatalf("验收未核对应判部分完成，实际 %s", res.Status)
	}
	if !strings.Contains(res.Error, "未能核对") {
		t.Errorf("要说明差的是核对这一步，实际 %q", res.Error)
	}
	// 没有验收标准时依旧什么都不做（向后兼容）
	res = &types.GoalResult{Status: types.GoalSuccess}
	applyAcceptanceVerdict(res, nil)
	if res.Status != types.GoalSuccess {
		t.Errorf("没有验收标准时不应改状态，实际 %s", res.Status)
	}
}

// TestAcceptance_StatusCorrectedToPartial 步骤都跑成功但验收只过了一半 → 判"部分完成"，
// 而不是"已完成"。写完了 ≠ 写对了。
func TestAcceptance_StatusCorrectedToPartial(t *testing.T) {
	res := &types.GoalResult{Status: types.GoalSuccess, Checks: []types.CheckResult{
		{Criterion: "给出 3 条建议", Passed: true},
		{Criterion: "每条写明负责人", Passed: false},
	}}
	applyAcceptanceVerdict(res, []string{"给出 3 条建议", "每条写明负责人"})
	if res.Status != types.GoalPartial {
		t.Fatalf("应判部分完成，实际 %s", res.Status)
	}
	if !strings.Contains(res.Error, "每条写明负责人") {
		t.Errorf("要说明哪条没达标，实际 %q", res.Error)
	}
	if strings.Contains(res.Error, "给出 3 条建议") {
		t.Errorf("已通过的条目不该出现在未达标列表里：%q", res.Error)
	}
}

// TestAcceptance_StatusCorrectedToFailed 一条都没过 → 判失败。
func TestAcceptance_StatusCorrectedToFailed(t *testing.T) {
	res := &types.GoalResult{Status: types.GoalSuccess, Checks: []types.CheckResult{
		{Criterion: "A", Passed: false},
		{Criterion: "B", Passed: false},
	}}
	applyAcceptanceVerdict(res, []string{"A", "B"})
	if res.Status != types.GoalFailed {
		t.Fatalf("全不过应判失败，实际 %s", res.Status)
	}
}

// TestAcceptance_StatusUntouched 没有验收标准、已判失败、或已判取消时都不动状态。
func TestAcceptance_StatusUntouched(t *testing.T) {
	// 没有验收标准：原行为不变
	res := &types.GoalResult{Status: types.GoalSuccess, Checks: []types.CheckResult{{Criterion: "A"}}}
	applyAcceptanceVerdict(res, nil)
	if res.Status != types.GoalSuccess {
		t.Errorf("没有验收标准时不应改状态，实际 %s", res.Status)
	}
	// 已经判为失败：不在这里翻案（失败有失败自己的原因要保留）
	res = &types.GoalResult{Status: types.GoalFailed, Error: "工具报错", Checks: []types.CheckResult{
		{Criterion: "A", Passed: true},
	}}
	applyAcceptanceVerdict(res, []string{"A"})
	if res.Status != types.GoalFailed || res.Error != "工具报错" {
		t.Errorf("已判失败的任务不该被改写，实际 %s / %q", res.Status, res.Error)
	}
	// 已取消
	res = &types.GoalResult{Status: types.GoalCancelled, Checks: []types.CheckResult{{Criterion: "A"}}}
	applyAcceptanceVerdict(res, []string{"A"})
	if res.Status != types.GoalCancelled {
		t.Errorf("取消状态不该被改写，实际 %s", res.Status)
	}
}

// TestAcceptance_NotDoneTriggersReplan 验收未全过时不该在第一次尝试就收工，
// 而应把"哪条没做到"作为反馈带回去重规划。
func TestAcceptance_NotDoneTriggersReplan(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"只给了一条建议"}}],
			"acceptance":["回复里给出不少于 3 条建议"]}`),
		// 模型嘴上说 done，判定却说没通过
		reflectChecksScript(`{"verdict":"done","reason":"我尽力了",
			"checks":[{"criterion":"回复里给出不少于 3 条建议","passed":false,"note":"只给了 1 条"}]}`),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "给我三条建议", Mode: "auto"})

	plans := 0
	for _, c := range f.llm.Calls {
		if llm.KindOf(c.System) == "plan" {
			plans++
		}
	}
	if plans < 2 {
		t.Fatalf("验收未通过应触发重规划，实际只规划了 %d 次", plans)
	}
	// 重规划次数用尽后仍要如实反映：不是"已完成"
	if res.Status == types.GoalSuccess {
		t.Errorf("验收始终未通过却报告成功：%+v", res)
	}
}
