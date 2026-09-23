package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/pkg/types"
)

// ---------- 计数：只有一份实现 ----------

// 计数判据必须与执行器汇总**逐字一致**。
//
// 两处各写一遍，迟早会出现"记录里说成功 3 步、对比时算出成功 2 步"这种自相矛盾，
// 而读的人只会更不信这张表。最容易分叉的一处是：**跑完了但业务上失败**
// （shell 非零退出、search 超时）——它 Status=succeeded，但 Outcome=failed。
// 按 Status 算会把它洗成成功，这正是回放报告最不该犯的错。
func TestCountSteps_MatchesExecutorJudgement(t *testing.T) {
	steps := []types.StepResult{
		{StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		{StepID: "s2", Status: types.StepSucceeded, Outcome: types.OutcomeFailed}, // 跑完但没做成
		{StepID: "s3", Status: types.StepFailed, Outcome: types.OutcomeFailed},
		{StepID: "s4", Status: types.StepSkipped},
		{StepID: "s5", Status: types.StepSucceeded, Outcome: types.OutcomeEmpty},
		{StepID: "s6", Status: types.StepSucceeded, Outcome: types.OutcomeOK, Carried: true},
	}
	s, f, sk, e, c := countSteps(steps)
	if s != 3 {
		t.Errorf("成功 = %d，应为 3（s1、s5，以及沿用的 s6——沿用来的成功步骤确实是成功的）；"+
			"s2 跑完但业务失败不算成功", s)
	}
	if f != 2 {
		t.Errorf("失败 = %d，应为 2（s2 与 s3）", f)
	}
	if sk != 1 {
		t.Errorf("跳过 = %d，应为 1", sk)
	}
	if e != 1 {
		t.Errorf("空结果 = %d，应为 1（空结果同时计入成功——它不是错误，但要看得见）", e)
	}
	if c != 1 {
		t.Errorf("沿用 = %d，应为 1", c)
	}
	if s+f+sk != len(steps) {
		t.Errorf("三类之和 %d 应等于总步数 %d", s+f+sk, len(steps))
	}
}

// 沿用与复用是两件不同的事，必须分得开。
//
// 去重（Deduped）是同一次运行内省下的调用：那一步**跑过**，结果被复用。
// 沿用（Carried）是这一步**这次根本没跑**。回放要回答的第一个问题就是
// "这一步到底执行了没有"，而两种"没真跑"的原因完全不同——一个是省调用，
// 一个是省副作用。印成一个词，读的人就无法判断这次有没有产生副作用。
func TestStepLine_DistinguishesCarriedFromDeduped(t *testing.T) {
	carried := stepLine(types.StepResult{StepID: "s1", Tool: "file.write", Status: types.StepSucceeded, Carried: true})
	deduped := stepLine(types.StepResult{StepID: "s1", Tool: "file.read", Status: types.StepSucceeded, Deduped: true})
	retried := stepLine(types.StepResult{StepID: "s1", Tool: "web.fetch", Status: types.StepSucceeded, Retried: true, Attempt: 3})

	if !strings.Contains(carried, "沿用") || !strings.Contains(carried, "未执行") {
		t.Errorf("沿用的步骤必须点明「这次没执行」，实际：%s", carried)
	}
	if strings.Contains(deduped, "沿用") {
		t.Errorf("复用不是沿用（它跑过），不能混为一谈：%s", deduped)
	}
	if !strings.Contains(deduped, "复用") {
		t.Errorf("复用要标出来：%s", deduped)
	}
	if !strings.Contains(retried, "第 3 次尝试") {
		t.Errorf("重试要带上第几次才成：%s", retried)
	}
}

// ---------- 恢复：缺记录就拒绝 ----------

// 恢复的前提是"前面每一步都有当时的结果"。缺一条就必须**拒绝**。
//
// 不能"缺了就当没这回事、顺手跑一遍"：那会把恢复悄悄变成重跑，而重跑有副作用
// （写文件、发请求、下单）。用户以为只是接着跑，实际上把前面做过的又做了一遍。
func TestResume_MissingPriorResultMustBeRefused(t *testing.T) {
	dir := t.TempDir()
	// 三步任务：只有 s1、s3 有结果，s2 缺失（模拟当时没落盘）。
	g := &types.GoalResult{
		TaskID: "t-miss",
		Goal:   "三步任务",
		Status: types.GoalFailed,
		ExecutedPlan: &types.Plan{Steps: []types.Step{
			{ID: "s1", Tool: "counter"},
			{ID: "s2", Tool: "counter", DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "counter", DependsOn: []string{"s2"}},
		}},
		Steps: []types.StepResult{
			{StepID: "s1", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
			{StepID: "s3", Tool: "counter", Status: types.StepFailed, Outcome: types.OutcomeFailed},
		},
	}
	writeTask(t, dir, g)

	src, err := loadRun(dir, "t-miss")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	// 从 s3 恢复要先沿用 s1、s2；s2 没有记录 → 必须拒绝。
	if _, ok := src.resultOf("s2"); ok {
		t.Error("s2 本就没有记录，不该被读出来")
	}
	if _, ok := src.resultOf("s1"); !ok {
		t.Error("s1 有记录，应该能读到")
	}
}

// ---------- 沿用提醒 ----------

// 沿用下来的步骤里有没成功的，必须提醒"这是当时的前提，不是恢复失败"。
//
// 不说的话，用户会看到目标步被跳过，以为 --from 坏了，然后改用 --rerun
// 把副作用又跑一遍——那正好是恢复想避免的事。
func TestCarriedWarning_FlagsFailedCarriedSteps(t *testing.T) {
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "counter"},
		{ID: "s2", Tool: "counter", DependsOn: []string{"s1"}},
		{ID: "s3", Tool: "counter", DependsOn: []string{"s2"}},
	}}

	// 全成功 → 不该提醒（乱提醒会让真的提醒被忽略）
	ok := carriedWarning(map[string]types.StepResult{
		"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		"s2": {StepID: "s2", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
	}, plan, "s3")
	if ok != "" {
		t.Errorf("前两步都成功时不该提醒，实际：%s", ok)
	}

	// s2 当时失败 → 必须提醒，并说清后果与出路
	warn := carriedWarning(map[string]types.StepResult{
		"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		"s2": {StepID: "s2", Status: types.StepFailed, Outcome: types.OutcomeFailed},
	}, plan, "s3")
	for _, want := range []string{"s2", "当时的前提", "不是恢复失败", "把起点提前"} {
		if !strings.Contains(warn, want) {
			t.Errorf("提醒缺少 %q，实际：%s", want, warn)
		}
	}

	// "跑完了但业务失败"也算没成功——只认 Status 会漏掉这一种
	warn2 := carriedWarning(map[string]types.StepResult{
		"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeFailed},
	}, plan, "s3")
	if warn2 == "" {
		t.Error("Outcome=failed 的沿用步骤也算没成功，必须提醒")
	}
}

// 只提醒**目标步之前**的沿用步骤。
//
// 目标步及之后本来就这次真跑，它们当时成功与否与这次无关；把它们也列出来，
// 提醒就变成了一堆噪音，真正的那个前提反而看不见。
func TestCarriedWarning_OnlyLooksBeforeTarget(t *testing.T) {
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "counter"},
		{ID: "s2", Tool: "counter", DependsOn: []string{"s1"}},
	}}
	warn := carriedWarning(map[string]types.StepResult{
		"s1": {StepID: "s1", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		"s2": {StepID: "s2", Status: types.StepFailed, Outcome: types.OutcomeFailed},
	}, plan, "s2")
	if warn != "" {
		t.Errorf("s2 是目标步，不该被当成「沿用下来的失败」，实际：%s", warn)
	}
}

// ---------- 比较 ----------

// 比较要能看出三类差异：整体状态、逐步结果、以及"这次有几步根本没跑"。
//
// 尤其最后一项：不印沿用步数，"这次成功 3 步"会被读成"这次跑了 3 步"。
func TestRenderDiff_ShowsCarriedAndStepChanges(t *testing.T) {
	a := runView{
		Ref: "t1", Label: "任务", Status: types.GoalFailed,
		Steps: []types.StepResult{
			{StepID: "s1", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
			{StepID: "s2", Tool: "web.fetch", Status: types.StepFailed, Outcome: types.OutcomeFailed, Error: "当时超时"},
		},
	}
	a.count()
	b := runView{
		Ref: "r1", Label: "回放",
		Steps: []types.StepResult{
			{StepID: "s1", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK, Carried: true},
			{StepID: "s2", Tool: "shell.run", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		},
	}
	b.count()

	out := renderDiff(a, b)
	for _, want := range []string{
		"对比", "沿用", "s1", "s2",
		"换工具（web.fetch → shell.run）", // 走法变了，这是分叉最该看出来的
	} {
		if !strings.Contains(out, want) {
			t.Errorf("对比缺少 %q，实际：\n%s", want, out)
		}
	}
	if !strings.Contains(out, "[沿用]") {
		t.Errorf("沿用的步骤要标出来，否则分不清哪几步是这次的：\n%s", out)
	}
}

// 两边步数不同时，多出来的步骤要说清"哪边没有"，而不是悄悄跳过。
func TestRenderDiff_ReportsStepsMissingOnEitherSide(t *testing.T) {
	a := runView{Ref: "a", Label: "任务", Steps: []types.StepResult{
		{StepID: "s1", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		{StepID: "s2", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
	}}
	a.count()
	b := runView{Ref: "b", Label: "回放", Steps: []types.StepResult{
		{StepID: "s2", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
		{StepID: "s3", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK},
	}}
	b.count()

	out := renderDiff(a, b)
	if !strings.Contains(out, "s1") || !strings.Contains(out, "B 里不存在") {
		t.Errorf("A 有而 B 没有的步骤要指明，实际：\n%s", out)
	}
	if !strings.Contains(out, "s3") || !strings.Contains(out, "A 里不存在") {
		t.Errorf("B 里新增的步骤也要指明，实际：\n%s", out)
	}
}

// ---------- 存档位置 ----------

// 回放记录必须落 replays/，**不能进 tasks/**。
//
// tasks/ 是任务列表与质量统计（完成率、用户重试率、首次验收通过率）的分母来源。
// 把回放混进去，指标就被复盘动作本身污染了：用户重放一次历史任务，
// 统计里就多出一个"用户提交过的任务"。
func TestSaveReplay_GoesToReplaysNotTasks(t *testing.T) {
	dir := t.TempDir()
	rec := &types.ReplayRecord{
		ReplayID: "r-1", Kind: types.ReplayResume, SourceTaskID: "t-1", FromStep: "s2",
		CreatedAt: time.Now(), Steps: []types.StepResult{{StepID: "s2", Status: types.StepSucceeded}},
	}
	path, err := saveReplay(dir, rec)
	if err != nil {
		t.Fatalf("存档失败: %v", err)
	}
	if filepath.Base(filepath.Dir(path)) != "replays" {
		t.Errorf("回放应落在 replays/，实际 %s", path)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "r-1.json")); !os.IsNotExist(err) {
		t.Error("回放记录不能出现在 tasks/ 里——那会污染质量统计的分母")
	}
	// 落盘的记录要能原样读回来（含沿用计数与配置快照）。
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	var back types.ReplayRecord
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if back.Kind != types.ReplayResume || back.FromStep != "s2" || back.SourceTaskID != "t-1" {
		t.Errorf("往返后关键字段丢了: %+v", back)
	}
}

// ---------- 读取入口 ----------

// 两个目录里同名时**拒绝并提示指明**，不能随便挑一个。
//
// 挑错了会让用户以为在看任务，其实看的是某次回放——而两者回答的问题不同
// （任务有交付判定与提示词分段，回放只有执行结果）。
func TestLoadRun_AmbiguousRefIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "replays"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "tasks", "x.json"), &types.GoalResult{TaskID: "x", Goal: "任务侧"})
	writeJSON(t, filepath.Join(dir, "replays", "x.json"), &types.ReplayRecord{ReplayID: "x", SourceTaskID: "t0"})

	if _, err := loadRun(dir, "x"); err == nil {
		t.Fatal("同名时应拒绝，而不是随便挑一个")
	} else if !strings.Contains(err.Error(), "指明") {
		t.Errorf("拒绝时要说清怎么解决（加前缀），实际：%v", err)
	}

	// 显式前缀是出口。
	task, err := loadRun(dir, "task:x")
	if err != nil {
		t.Fatalf("task: 前缀应能读到: %v", err)
	}
	if task.Task == nil || task.Task.Goal != "任务侧" {
		t.Errorf("task: 应读任务记录，实际 %+v", task.Task)
	}
	rep, err := loadRun(dir, "replay:x")
	if err != nil {
		t.Fatalf("replay: 前缀应能读到: %v", err)
	}
	if rep.Replay == nil || rep.Replay.SourceTaskID != "t0" {
		t.Errorf("replay: 应读回放记录，实际 %+v", rep.Replay)
	}
}

// 只跑了半截的运行（没有终态记录、只有运行日志）也要能读，并标成 RunLogOnly。
//
// 这是运行中落盘那一层存在的理由：进程在运行中退出，那次运行没有终态记录，
// 运行日志是唯一凭据。读不出来，这一层就白做了。
func TestLoadRun_FallsBackToRunLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(types.StepResult{StepID: "s1", Tool: "counter", Status: types.StepSucceeded, Outcome: types.OutcomeOK})
	if err := os.WriteFile(filepath.Join(dir, "runs", "half.jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := loadRun(dir, "half")
	if err != nil {
		t.Fatalf("应从运行日志里读出来: %v", err)
	}
	if !l.RunLogOnly || len(l.LogSteps) != 1 {
		t.Errorf("应标成半截运行并带 1 条步骤，实际 %+v", l)
	}
	if l.Plan() != nil {
		t.Error("半截运行没有落盘计划，Plan() 应为 nil（有的话会被误当成可重跑）")
	}
	out := renderLoaded(l)
	if !strings.Contains(out, "没有跑完") {
		t.Errorf("渲染要如实说明这次没跑完：\n%s", out)
	}
}

// 什么都没有时，报错要说清找过哪三个地方。
//
// "找不到"本身没有可操作性；说清找过 tasks/、replays/、runs/，用户才知道
// 自己是记错了 ID，还是这条任务既没跑完也没留下日志。
func TestLoadRun_NotFoundMentionsAllLocations(t *testing.T) {
	dir := t.TempDir()
	_, err := loadRun(dir, "ghost")
	if err == nil {
		t.Fatal("应报错")
	}
	for _, want := range []string{"tasks/", "replays/", "runs/"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("报错应提到找过 %s，实际：%v", want, err)
		}
	}
}

// ---------- 小工具 ----------

func writeTask(t *testing.T, dir string, g *types.GoalResult) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "tasks", g.TaskID+".json"), g)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
