package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// outcomeTool 一个"调用没炸、但事情没做成"的工具：
// Execute 返回 nil error，靠 types.OutcomeReporter 声明真实的结果性质。
// 真实世界里的对应物是 shell.exec（非零退出）与 file.search（超时/没搜到）。
type outcomeTool struct {
	name    string
	out     any
	outcome types.StepOutcome
	note    string
}

func (t *outcomeTool) Name() string                 { return t.name }
func (t *outcomeTool) Description() string          { return "outcome tool " + t.name }
func (t *outcomeTool) Permission() types.Permission { return types.PermissionReadOnly }
func (t *outcomeTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
}
func (t *outcomeTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return t.out, nil
}
func (t *outcomeTool) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	return t.outcome, t.note
}

func outcomeExec(t *testing.T, tool types.Tool, steps ...types.Step) *Result {
	t.Helper()
	r := registry.New()
	r.MustRegister(tool)
	e := &Executor{Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second), Notifier: NopNotifier{}}
	return e.Execute(context.Background(), types.Plan{Steps: steps}, "t1", "auto", false)
}

// TestExecute_BusinessFailureCountsAsFailed 这是「完成率失真」的回归断言。
//
// 工具返回 nil error 只说明"调用没炸"，不代表"事情做成了"：
// shell 非零退出、file.search 超时都属于这一类。以前它们被计入 Succeeded，
// statusOfExec 于是把整个任务判成 success——不是统计口径问题，是数据源头不对。
func TestExecute_BusinessFailureCountsAsFailed(t *testing.T) {
	tool := &outcomeTool{
		name: "bizfail", out: map[string]any{"exit_code": 1},
		outcome: types.OutcomeFailed, note: "命令以退出码 1 结束",
	}
	res := outcomeExec(t, tool, types.Step{ID: "s1", Tool: "bizfail"})

	if res.Failed != 1 || res.Succeeded != 0 {
		t.Fatalf("业务失败应计入 Failed，实际 Succeeded=%d Failed=%d", res.Succeeded, res.Failed)
	}
	if got := statusOfExec(res); got != types.GoalFailed {
		t.Errorf("状态应为 failed，实际 %s", got)
	}
	st := res.Steps[0]
	if st.Status != types.StepSucceeded || st.Outcome != types.OutcomeFailed {
		t.Errorf("Status/Outcome 应分别为 succeeded/failed，实际 %s/%s", st.Status, st.Outcome)
	}
	if st.Error == "" {
		t.Error("业务失败的原因必须抬到 Error，否则反思器与用户都看不见")
	}
}

// TestExecute_EmptyIsNotFailure 空结果不是错误（不能因此重试），
// 但必须与"有结果"区分开，否则模型会在"换关键词—还是空—再换"里空转。
func TestExecute_EmptyIsNotFailure(t *testing.T) {
	tool := &outcomeTool{
		name: "empty", out: map[string]any{"count": 0, "files": []string{}},
		outcome: types.OutcomeEmpty,
	}
	res := outcomeExec(t, tool, types.Step{ID: "s1", Tool: "empty"})

	if res.Succeeded != 1 || res.Failed != 0 {
		t.Fatalf("空结果不该算失败，实际 Succeeded=%d Failed=%d", res.Succeeded, res.Failed)
	}
	if res.Empty != 1 {
		t.Errorf("空结果应被单独计数，实际 Empty=%d", res.Empty)
	}
	if got := statusOfExec(res); got != types.GoalSuccess {
		t.Errorf("空结果不是错误，状态应为 success，实际 %s", got)
	}
}

// TestExecute_OutcomeSurvivesDedupe 去重复用的结果必须连结果性质一起还原。
// 只存输出、不存 Outcome 的话，一个"空结果"被复用时会被记成 ok，语义就丢了——
// 而"空"恰恰是模型最容易反复重试的那一态。
//
// 用 depends_on 让 s2 严格排在 s1 之后（而不是并发抢锁）：并发时"谁真跑、
// 谁复用"不确定，断言会飘。
func TestExecute_OutcomeSurvivesDedupe(t *testing.T) {
	r := registry.New()
	r.MustRegister(&outcomeTool{
		name: "listed", out: map[string]any{"count": 0},
		outcome: types.OutcomeEmpty,
	})
	e := &Executor{
		Reg: r, Gate: safety.New("auto", nil, nil, nil, time.Second),
		Notifier: NopNotifier{}, Dedupe: true, StepTimeout: 2 * time.Second,
	}
	plan := types.Plan{Steps: []types.Step{
		{ID: "s1", Tool: "listed"},
		{ID: "s2", Tool: "listed", DependsOn: []string{"s1"}},
	}}
	res := e.Execute(context.Background(), plan, "t1", "auto", false)

	if !res.Steps[1].Deduped {
		t.Fatalf("第二步应走复用，实际 %+v", res.Steps[1])
	}
	if res.Empty != 2 {
		t.Errorf("复用的空结果仍应计为 Empty，实际 Empty=%d", res.Empty)
	}
	if res.Steps[1].Outcome != types.OutcomeEmpty {
		t.Errorf("复用后 Outcome 应保持 empty，实际 %q", res.Steps[1].Outcome)
	}
}

// TestBuildStepDigest_ReportsOutcomeAndFailure 重规划反馈必须真的带上上次的结果。
//
// 原先反馈只有一句"完成度 N/100。问题：…"，文案还写着"上次执行的步骤结果可在反馈中参考"，
// 而构造执行摘要的 buildDigest 只喂给反思器——规划器第二轮与第一轮几乎同样盲，
// 这正是"原地打转"频繁触发的原因之一。
func TestBuildStepDigest_ReportsOutcomeAndFailure(t *testing.T) {
	exec := &Result{
		Total: 3,
		Steps: []types.StepResult{
			{StepID: "s1", Tool: "shell.exec", Status: types.StepSucceeded, Outcome: types.OutcomeFailed, Error: "命令以退出码 1 结束"},
			{StepID: "s2", Tool: "file.search", Status: types.StepSucceeded, Outcome: types.OutcomeEmpty},
			{StepID: "s3", Tool: "file.write", Status: types.StepFailed, Error: "路径超出允许的工作区范围"},
		},
	}
	got := buildStepDigest(exec)
	for _, want := range []string{"s1", "退出码 1", "s2", "结果为空", "s3", "路径超出"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要应包含 %q，实际：\n%s", want, got)
		}
	}
	if buildStepDigest(nil) != "" {
		t.Error("nil 执行结果应返回空串")
	}
}

// TestBuildDigest_TellsReflectorAboutOutcome 反思器看到的摘要也必须区分
// "成功"、"成功但业务失败"、"成功但空结果"——否则它会照着错的前提打分。
func TestBuildDigest_TellsReflectorAboutOutcome(t *testing.T) {
	exec := &Result{
		Total: 2,
		Steps: []types.StepResult{
			{StepID: "s1", Tool: "shell.exec", Status: types.StepSucceeded, Outcome: types.OutcomeFailed, Error: "命令以退出码 1 结束"},
			{StepID: "s2", Tool: "file.search", Status: types.StepSucceeded, Outcome: types.OutcomeEmpty},
		},
	}
	got := buildDigest("目标", exec, 1, nil, nil)
	if !strings.Contains(got, "执行失败") {
		t.Errorf("业务失败应显式写成执行失败，实际：\n%s", got)
	}
	if !strings.Contains(got, "空结果") {
		t.Errorf("空结果应显式标出，实际：\n%s", got)
	}
}

// TestRunGoal_ReplanFeedbackCarriesStepResults 重规划反馈必须真的带上上次的步骤结果。
//
// 这是"接线"断言，不是函数断言：buildStepDigest 本身正确，不代表它被接进了主循环。
// 原先的文案写着"上次执行的步骤结果可在反馈中参考"，而构造摘要的 buildDigest
// 只喂给反思器——规划器第二轮与第一轮几乎同样盲，这正是"原地打转"频繁触发的原因之一。
func TestRunGoal_ReplanFeedbackCarriesStepResults(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		// 第一次规划：故意用一个必定失败的工具
		planScript(`{"steps":[{"id":"s1","description":"调用会失败的工具","tool":"fail","args":{}}]}`),
		reflectScript(40, "replan", "步骤失败"),
		// 第二次规划：改对了
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(95, "done", "已回复"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "跑一次"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", res)
	}

	var plans []llm.ChatRequest
	for _, c := range f.llm.Calls {
		if llm.KindOf(c.System) == "plan" {
			plans = append(plans, c)
		}
	}
	if len(plans) < 2 {
		t.Fatalf("应有至少两次规划调用，实际 %d", len(plans))
	}
	second := plans[1].System
	if !strings.Contains(second, "重规划反馈") {
		t.Error("第二次规划的提示词里没有重规划反馈段")
	}
	if !strings.Contains(second, "boom") {
		t.Errorf("反馈里应带上上次失败的具体原因（fail 工具返回的 boom）")
	}
	if !strings.Contains(second, "s1") {
		t.Error("反馈里应指明是哪个步骤出的问题")
	}
}
