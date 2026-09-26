package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/agent"
	"gleam/internal/atomicfile"
	"gleam/internal/config"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// cmdReplay 回放一条历史任务：读 tasks/<taskID>.json，把"当时到底发生了什么"排给人看。
//
// 为什么要有它：GoalResult 一直有 ExecutedPlan 字段，注释写着"供技能固化与复现"，
// 但"复现"从未实现——结果落了盘，计划没落盘，复盘的人只能看着结果猜计划。
// 现在 ExecutedPlan 随 GoalResult 一起进 tasks/<id>.json，回放才有了凭据。
//
// **同一份记录，四种问法**（审计的四种用法）：
//   - 回放（默认）：打印当时的计划、每步的结果（含 attempt 与归因）、用量与归因分布。
//   - 重跑（--rerun）：用当时的计划原样再执行一遍，输出逐项对比。
//   - 恢复（--from <stepID>）：该步**之前**沿用当时结果、不重新执行，从该步往下跑。
//     恢复不只是省时间——从头重跑会把前面每一步的副作用（写文件、发请求）再做一遍。
//   - 分叉（--from <stepID> --tool/--args）：同一前提换一种走法，落成一条新记录。
//   - 比较（--diff <A> <B>）：两条记录逐步对比——分叉与原来的差异、两次重跑之间的差异。
//
// **重跑/恢复/分叉的产物落 replays/，不进 tasks/**：那条目录是任务列表与质量统计
// （完成率、用户重试率、首次验收通过率）的分母来源，把回放混进去，指标就会被复盘
// 动作本身污染——用户重放一次历史任务，统计里就多一个"用户提交过的任务"。
//
// 重跑会真实调用工具（有副作用的会真的做），所以必须显式指定，不提供静默重跑。
// 读取不装配运行时以外的任何东西：任务文件是自包含的 JSON，看历史不需要模型与网络。
func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	dataDir := fs.String("data-dir", "", "数据目录")
	rerun := fs.Bool("rerun", false, "用当时的计划真实重跑一遍（会真实调用工具）")
	from := fs.String("from", "", "从这一步开始重跑；之前的步骤沿用当时结果，不会重新执行")
	forkTool := fs.String("tool", "", "分叉：把 --from 指定的那一步换成这个工具")
	forkArgs := fs.String("args", "", "分叉：该步骤的新参数（JSON 对象）")
	diff := fs.Bool("diff", false, "比较两条记录：gleam replay --diff <记录A> <记录B>")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型（仅真实重跑时有意义）")
	flagArgs, positionals := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	rt, err := buildRuntime(*configPath, "", *dataDir, *mockLLM, "", agent.NopNotifier{})
	if err != nil {
		return err
	}
	defer rt.cleanup()

	if *diff {
		if len(positionals) != 2 {
			return fmt.Errorf("用法: gleam replay --diff <记录A> <记录B>")
		}
		a, err := loadRun(rt.cfg.DataDir, positionals[0])
		if err != nil {
			return err
		}
		b, err := loadRun(rt.cfg.DataDir, positionals[1])
		if err != nil {
			return err
		}
		fmt.Print(renderDiff(a.view(), b.view()))
		return nil
	}

	if len(positionals) != 1 {
		return fmt.Errorf("用法: gleam replay <taskID> [--rerun | --from <stepID> [--tool <name>] [--args <json>] | --diff <A> <B>]")
	}
	if *rerun && *from != "" {
		return fmt.Errorf("--rerun（从头原样重跑）与 --from（从某一步继续）只能选一个")
	}
	if (*forkTool != "" || *forkArgs != "") && *from == "" {
		return fmt.Errorf("分叉要指明改哪一步：--from <stepID> --tool <name>（现在只给了 --tool/--args）")
	}

	src, err := loadRun(rt.cfg.DataDir, positionals[0])
	if err != nil {
		return err
	}
	fmt.Print(renderLoaded(src))

	if src.RunLogOnly {
		return fmt.Errorf("这次运行没有跑完（没有终态记录），无法重跑——它只留下了运行中的步骤日志")
	}
	if !*rerun && *from == "" {
		fmt.Println("\n（--rerun 原样重跑；--from <stepID> 从某一步继续；--from <stepID> --tool <name> 换一种走法；--diff <A> <B> 比较两条记录。重跑会真实调用工具，请确认无副作用顾虑后再用）")
		return nil
	}
	if src.Plan() == nil || len(src.Plan().Steps) == 0 {
		return fmt.Errorf("这条任务没有落盘执行计划（ExecutedPlan 为空），无法重跑——它可能是在旧版本下产出的")
	}

	plan := *src.Plan()
	plan.Steps = append([]types.Step(nil), src.Plan().Steps...)
	kind := types.ReplayRerun
	var carried map[string]types.StepResult
	var overrideTool string
	var overrideArgs map[string]any

	if *from != "" {
		idx := -1
		for i, st := range plan.Steps {
			if st.ID == *from {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("计划里没有步骤 %q；可用：%s", *from, strings.Join(stepIDs(plan), "、"))
		}
		kind = types.ReplayResume
		// 之前的步骤沿用当时的结果：不执行、不重复副作用。
		// 缺一条记录就拒绝，而不是"缺了就当没这回事、顺手跑一遍"——
		// 那会把恢复悄悄变成重跑，而重跑是有副作用的。
		carried = map[string]types.StepResult{}
		for i := 0; i < idx; i++ {
			st := plan.Steps[i]
			r, ok := src.resultOf(st.ID)
			if !ok {
				return fmt.Errorf("步骤 %q 当时没有结果记录，无法沿用；不能从 %q 恢复——"+
					"跳过它意味着把它重新执行一遍（可能有副作用）。请换一个起点，或直接用 --rerun",
					st.ID, *from)
			}
			carried[st.ID] = r
		}
		if *forkTool != "" || *forkArgs != "" {
			kind = types.ReplayFork
			if *forkTool != "" {
				plan.Steps[idx].Tool = *forkTool
				overrideTool = *forkTool
			}
			if *forkArgs != "" {
				args := map[string]any{}
				if err := json.Unmarshal([]byte(*forkArgs), &args); err != nil {
					return fmt.Errorf("--args 不是合法的 JSON 对象: %w", err)
				}
				plan.Steps[idx].Args = args
				overrideArgs = args
			}
		}
		if warn := carriedWarning(carried, plan, *from); warn != "" {
			fmt.Println(warn)
		}
	}

	replayTaskID := "replay-" + src.TaskID()
	executor, params := rerunExecutor(src.Goal(), src.Snapshot(), rt.cfg, rt.reg, rt.gate, rt.cfg.DataDir, replayTaskID)
	executor.Carried = carried

	switch kind {
	case types.ReplayFork:
		fmt.Printf("\n分叉中：从 %s 起重跑，该步换成 %s（真实调用工具）…\n", *from, forkSummary(overrideTool, overrideArgs))
	case types.ReplayResume:
		fmt.Printf("\n恢复中：从 %s 起重跑，前 %d 步沿用当时结果（不会重新执行）…\n", *from, len(carried))
	default:
		fmt.Printf("\n重跑中：%d 个步骤（真实调用工具）…\n", len(plan.Steps))
	}
	fmt.Printf("%s\n", params.Origin())

	start := time.Now()
	exec := executor.Execute(context.Background(), plan, replayTaskID, string(types.ModeAuto), false)
	wall := time.Since(start).Milliseconds()

	rec := replayRecordOf(kind, src.TaskID(), *from, overrideTool, overrideArgs, exec, wall, &params)
	path, saveErr := saveReplay(rt.cfg.DataDir, rec)
	agent.DiscardRunLog(rt.cfg.DataDir, replayTaskID)

	fmt.Print(renderDiff(src.view(), viewOfReplay(rec)))
	if saveErr != nil {
		fmt.Fprintf(os.Stderr, "\n[gleam] 回放记录未能存档：%v（本次结果只在上面，无法与别的记录比较）\n", saveErr)
	} else {
		fmt.Printf("\n本次回放已存档（%s）：gleam replay --diff %s %s 可与原任务逐步对比\n",
			path, src.TaskID(), rec.ReplayID)
	}
	if exec.Failed > 0 {
		return fmt.Errorf("重跑存在失败步骤")
	}
	return nil
}

// ---------- 记录的读取 ----------

// loadedRun 一条读出来的运行记录。
//
// 三种来源只会命中一个，而且**它们是三件不同的事**：跑完的任务（有完整计划）、
// 一次回放的产物（有结果、也有原计划）、只跑了一半的运行（只有步骤日志，没有计划）。
// 用一个结构显式表达"有几种入口"，比在调用点各写一遍 if 更不容易漏。
type loadedRun struct {
	Ref    string
	Task   *types.GoalResult
	Replay *types.ReplayRecord
	// LogSteps 只在 RunLogOnly 时非空。
	LogSteps   []types.StepResult
	RunLogOnly bool
}

func (l loadedRun) Goal() string {
	switch {
	case l.Task != nil:
		return l.Task.Goal
	case l.Replay != nil:
		return l.Replay.SourceTaskID + " 的回放"
	default:
		return ""
	}
}

func (l loadedRun) Plan() *types.Plan {
	if l.Task != nil {
		return l.Task.ExecutedPlan
	}
	return nil
}

func (l loadedRun) Snapshot() *types.ConfigSnapshot {
	switch {
	case l.Task != nil:
		return l.Task.ConfigSnapshot
	case l.Replay != nil:
		return l.Replay.ConfigSnapshot
	default:
		return nil
	}
}

func (l loadedRun) TaskID() string {
	switch {
	case l.Task != nil:
		return l.Task.TaskID
	case l.Replay != nil:
		return l.Replay.SourceTaskID
	default:
		return l.Ref
	}
}

func (l loadedRun) resultOf(stepID string) (types.StepResult, bool) {
	steps := l.LogSteps
	if l.Task != nil {
		steps = l.Task.Steps
	} else if l.Replay != nil {
		steps = l.Replay.Steps
	}
	for _, r := range steps {
		if r.StepID == stepID {
			return r, true
		}
	}
	return types.StepResult{}, false
}

// view 归一成可比较视图（比较逻辑只认这一种形状）。
func (l loadedRun) view() runView {
	switch {
	case l.Task != nil:
		v := runView{
			Ref: l.Task.TaskID, Label: "任务", TaskID: l.Task.TaskID, Goal: l.Task.Goal,
			Status: l.Task.Status, Steps: l.Task.Steps, Plan: l.Task.ExecutedPlan,
			Snapshot: l.Task.ConfigSnapshot, Usage: l.Task.Usage,
			DurationMs: l.Task.Usage.DurationMs, FirstPass: l.Task.FirstPass, Reworks: l.Task.Reworks,
		}
		v.count()
		return v
	case l.Replay != nil:
		return viewOfReplay(l.Replay)
	default:
		v := runView{Ref: l.Ref, Label: "未跑完的运行", RunLogOnly: true, Steps: l.LogSteps}
		v.count()
		return v
	}
}

func loadRun(dataDir, ref string) (loadedRun, error) {
	ref = strings.TrimSuffix(ref, ".json")
	// 显式前缀是"两个目录里都存在"时的出口；不给前缀就按 tasks → replays → runs 找。
	switch {
	case strings.HasPrefix(ref, "task:"):
		return loadRunFrom(dataDir, strings.TrimPrefix(ref, "task:"), "task")
	case strings.HasPrefix(ref, "replay:"):
		return loadRunFrom(dataDir, strings.TrimPrefix(ref, "replay:"), "replay")
	}
	taskPath := agent.TaskArchivePath(dataDir, ref)
	replayPath := replayFile(dataDir, ref)
	taskOK, replayOK := fileExists(taskPath), fileExists(replayPath)
	if taskOK && replayOK {
		return loadedRun{}, fmt.Errorf("%q 在 tasks/ 与 replays/ 里都存在，无法判断读哪一条（用 task:%s 或 replay:%s 指明）", ref, ref, ref)
	}
	if taskOK {
		return loadRunFrom(dataDir, ref, "task")
	}
	if replayOK {
		return loadRunFrom(dataDir, ref, "replay")
	}
	// 运行日志是最后一条线索：任务跑完了就会被删，所以"有内容"才说明这次运行
	// 是半途没了的。有内容就当成一条可读的记录，而不是继续报"找不到"——
	// 那正是最需要它的场合（没有终态记录，日志是唯一凭据）。
	if steps, err := agent.ReadRunLog(dataDir, ref); err == nil && len(steps) > 0 {
		return loadedRun{Ref: ref, LogSteps: steps, RunLogOnly: true}, nil
	}
	return loadedRun{}, fmt.Errorf("读不到记录 %q（数据目录 %s）：tasks/%s.json、replays/%s.json 都不存在，"+
		"运行日志 runs/%s.jsonl 也没有——这条任务可能既没跑完、也没留下运行日志", ref, dataDir, ref, ref, ref)
}

func loadRunFrom(dataDir, id, kind string) (loadedRun, error) {
	if kind != "replay" {
		// 任务归档的读法只有一个 owner（agent.ReadTaskResult）：路径形状、
		// "没有这条"与"文件坏了"怎么分，写侧与读侧必须给同一个答案。
		g, err := agent.ReadTaskResult(dataDir, id)
		if err != nil {
			return loadedRun{}, err
		}
		if g == nil {
			return loadedRun{}, fmt.Errorf("读取%s记录失败: 没有 tasks/%s.json", kind, id)
		}
		return loadedRun{Ref: g.TaskID, Task: g}, nil
	}
	path := replayFile(dataDir, id)
	if path == "" {
		return loadedRun{}, fmt.Errorf("回放 ID %q 不能作文件名", id)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return loadedRun{}, fmt.Errorf("读取%s记录失败: %w", kind, err)
	}
	var r types.ReplayRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return loadedRun{}, fmt.Errorf("解析回放记录失败: %w", err)
	}
	return loadedRun{Ref: r.ReplayID, Replay: &r}, nil
}

// replayFile 回放产物的落点（replays/<id>.json）；id 不能作文件名时返回空。
// 名字规则与任务归档共用 agent.SafeTaskName——两套形状迟早漂移成"写得出去读不回来"。
func replayFile(dataDir, id string) string {
	if agent.SafeTaskName(id) == "" {
		return ""
	}
	return filepath.Join(dataDir, "replays", id+".json")
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// saveReplay 把一次回放落进 replays/（**不是 tasks/**，回放不是任务）。
func saveReplay(dataDir string, rec *types.ReplayRecord) (string, error) {
	path := replayFile(dataDir, rec.ReplayID)
	if path == "" {
		return "", fmt.Errorf("回放 ID %q 不能作文件名，未归档", rec.ReplayID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(rec, "", " ")
	if err != nil {
		return "", err
	}
	if err := atomicfile.Write(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// replayRecordOf 由执行结果构造回放记录。计数走 countSteps 这**一份**实现——
// 记录里写的数与比较时算出的数必须同源，否则"记录说成功 3 步、对比说成功 2 步"
// 这种自相矛盾迟早会出现，而读的人只会更不信这张表。
func replayRecordOf(kind types.ReplayKind, srcTaskID, from, tool string, args map[string]any,
	exec *agent.Result, wallMS int64, params *rerunParams) *types.ReplayRecord {
	rec := &types.ReplayRecord{
		ReplayID:       types.NewID(),
		Kind:           kind,
		SourceTaskID:   srcTaskID,
		FromStep:       from,
		OverrideTool:   tool,
		OverrideArgs:   args,
		CreatedAt:      time.Now(),
		Steps:          exec.Steps,
		DurationMs:     wallMS,
		ConfigSnapshot: params.Snapshot,
	}
	rec.Succeeded, rec.Failed, rec.Skipped, rec.Empty, rec.Carried = countSteps(exec.Steps)
	return rec
}

// ---------- 归一视图与比较 ----------

// runView 两条记录归一后的可比较视图。
//
// 任务记录与回放记录的字段不同，但回答同一批问题：做了哪几步、每步结果如何、
// 耗时多少、当时用什么配置跑的。**比较逻辑只写一份**——两份实现迟早会给出两个
// 不同的"差异"，而复盘的人只会看到其中一个。
type runView struct {
	Ref    string
	Label  string
	TaskID string
	Goal   string
	// Status 回放记录没有整体状态（它是执行结果，不是任务判定），留空。
	Status     types.GoalStatus
	Steps      []types.StepResult
	Plan       *types.Plan
	Snapshot   *types.ConfigSnapshot
	Usage      types.TaskUsage
	DurationMs int64
	FirstPass  bool
	Reworks    int
	RunLogOnly bool

	Succeeded, Failed, Skipped, Empty, Carried int
}

func (v *runView) count() {
	v.Succeeded, v.Failed, v.Skipped, v.Empty, v.Carried = countSteps(v.Steps)
}

// countSteps 由步骤结果算出五项计数。
//
// 判据与执行器汇总时**逐字一致**：Outcome=failed 的步骤计入 Failed，
// 不因为 Status=succeeded 就算成功——否则"跑完了但没做成"会被回放报告洗白。
func countSteps(steps []types.StepResult) (succeeded, failed, skipped, empty, carried int) {
	for _, r := range steps {
		if r.Carried {
			carried++
		}
		switch {
		case r.Status == types.StepSucceeded && r.Outcome == types.OutcomeFailed:
			failed++
		case r.Status == types.StepSucceeded:
			succeeded++
			if r.Outcome == types.OutcomeEmpty {
				empty++
			}
		case r.Status == types.StepFailed:
			failed++
		default:
			skipped++
		}
	}
	return
}

func viewOfReplay(r *types.ReplayRecord) runView {
	v := runView{
		Ref: r.ReplayID, Label: "回放", TaskID: r.SourceTaskID,
		Goal: r.SourceTaskID + " 的回放", Steps: r.Steps,
		Snapshot: r.ConfigSnapshot, DurationMs: r.DurationMs,
	}
	v.count()
	return v
}

// ---------- 渲染 ----------

// renderLoaded 按记录来源选渲染方式。三种来源的内容本就不同（任务有提示词分段与
// 归因分布，回放只有执行结果，半截运行只有步骤），所以不强行合并成一个渲染器——
// 把没有的东西印成空，比不印更容易被读成"当时是空的"。
func renderLoaded(l loadedRun) string {
	switch {
	case l.Task != nil:
		return renderTrace(l.Task)
	case l.Replay != nil:
		return renderReplayTrace(l.Replay)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "任务回放  %s\n", l.Ref)
		fmt.Fprintln(&b, "**这次运行没有跑完**：没有终态记录，以下是运行日志里恢复到的步骤。")
		fmt.Fprintln(&b, "没有落盘的计划，所以它无法重跑；能回答的是「当时跑到哪一步、每步什么结果」。")
		fmt.Fprintf(&b, "\n步骤（%d 个）：\n", len(l.LogSteps))
		for _, st := range l.LogSteps {
			fmt.Fprintf(&b, "  %s\n", stepLine(st))
		}
		return b.String()
	}
}

// renderTrace 把一条历史任务排成人能读的文本。
func renderTrace(g *types.GoalResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务回放  %s\n", g.TaskID)
	fmt.Fprintf(&b, "目标：%s\n", g.Goal)
	fmt.Fprintf(&b, "trace_id：%s（同输入+同模型重复出现时相同，可据此聚类）\n", g.TraceID)
	fmt.Fprintf(&b, "状态：%s  完成度 %d/100  规则集 %s\n", g.Status, g.Score, g.RuleSet)
	// 快照与规则集是同一类东西的两个半边：规则集回答"提示词是哪一版"，
	// 快照回答"模型与阈值是哪一套"。缺了后一半，两条历史结果之间就不可比。
	if g.ConfigSnapshot != nil {
		fmt.Fprintf(&b, "配置快照：%s\n", describeSnapshot(g.ConfigSnapshot))
	} else {
		fmt.Fprintf(&b, "配置快照：无（产出于本字段落地之前；--rerun 会回退当前配置，差异不能全归给环境）\n")
	}
	if g.Error != "" {
		fmt.Fprintf(&b, "错误：%s\n", g.Error)
	}

	if bd := g.PromptBreakdown; bd != nil {
		fmt.Fprintf(&b, "\n提示词分段：指令 %d / 能力 %d / 知识 %d / 状态 %d（合计 %d",
			bd.Instruction, bd.Capability, bd.Knowledge, bd.State, bd.Total)
		if bd.Total > 0 {
			fmt.Fprintf(&b, "，状态占比 %.0f%%", bd.StateShare()*100)
		}
		fmt.Fprintln(&b, "）")
	}

	if len(g.Steps) > 0 {
		fmt.Fprintf(&b, "\n步骤（%d 个）：\n", len(g.Steps))
		for _, st := range g.Steps {
			fmt.Fprintf(&b, "  %s\n", stepLine(st))
		}
	}

	if len(g.FailureBreakdown) > 0 {
		fmt.Fprintf(&b, "\n失败归因分布：")
		for k, n := range g.FailureBreakdown {
			fmt.Fprintf(&b, " %s=%d", k, n)
		}
		fmt.Fprintln(&b)
	}

	// 交付侧的一次性。三种情形都要能区分：一次通过 / 返工 N 轮 / 没走到验收
	// （两个字段都为零值，例如对话模式）——只印其中一种，另两种就混进来看不见了。
	switch {
	case g.FirstPass:
		fmt.Fprintf(&b, "\n返工：0 轮（一次通过）\n")
	case g.Reworks > 0:
		fmt.Fprintf(&b, "\n返工：%d 轮（非一次通过）\n", g.Reworks)
	}

	u := g.Usage
	fmt.Fprintf(&b, "\n用量：模型调用 %d（token %d/%d）· 工具调用 %d · 重试 %d · 去重 %d · 耗时 %dms\n",
		u.LLMCalls, u.PromptTokens, u.CompletionTokens, u.ToolCalls, u.Retries, u.Deduped, u.DurationMs)

	if g.ExecutedPlan != nil && len(g.ExecutedPlan.Steps) > 0 {
		fmt.Fprintf(&b, "\n当时的计划（%d 步，可 --rerun 原样重跑）：\n", len(g.ExecutedPlan.Steps))
		for _, st := range g.ExecutedPlan.Steps {
			fmt.Fprintf(&b, "  %s → %s %s\n", st.ID, st.Tool, st.Describe())
		}
	}
	return b.String()
}

// renderReplayTrace 打印一条回放记录。
//
// 与任务回放刻意不同：回放记录里**没有**提示词分段、规则集与交付口径——它不是一次
// 任务判定，是一次执行。
func renderReplayTrace(r *types.ReplayRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "回放记录  %s（%s）\n", r.ReplayID, replayKindLabel(r.Kind))
	fmt.Fprintf(&b, "源自任务：%s\n", r.SourceTaskID)
	if r.FromStep != "" {
		fmt.Fprintf(&b, "起点：%s 起（之前的步骤沿用当时结果，没有重新执行）\n", r.FromStep)
	}
	if r.OverrideTool != "" || len(r.OverrideArgs) > 0 {
		fmt.Fprintf(&b, "改动：%s\n", forkSummary(r.OverrideTool, r.OverrideArgs))
	}
	fmt.Fprintf(&b, "时间：%s\n", r.CreatedAt.Format("2006-01-02 15:04:05"))
	if r.ConfigSnapshot != nil {
		fmt.Fprintf(&b, "配置快照：%s\n", describeSnapshot(r.ConfigSnapshot))
	} else {
		fmt.Fprintf(&b, "配置快照：无（这次回放没有记录配置）\n")
	}
	s, f, sk, e, c := countSteps(r.Steps)
	fmt.Fprintf(&b, "\n结果：成功 %d / 失败 %d / 跳过 %d / 空结果 %d（共 %d 步）", s, f, sk, e, len(r.Steps))
	if c > 0 {
		fmt.Fprintf(&b, "，其中 %d 步沿用（这次没有执行）", c)
	}
	fmt.Fprintf(&b, "\n耗时：%dms\n", r.DurationMs)
	if len(r.Steps) > 0 {
		fmt.Fprintf(&b, "\n步骤（%d 个）：\n", len(r.Steps))
		for _, st := range r.Steps {
			fmt.Fprintf(&b, "  %s\n", stepLine(st))
		}
	}
	return b.String()
}

// stepLine 一行步骤结果。三种"没真跑"的原因必须分得开：
// 沿用（这次没执行）/ 复用（去重省下的调用）/ 重试（第几次才成）。
func stepLine(st types.StepResult) string {
	line := fmt.Sprintf("[%s] %s (%s)", st.Status, st.StepID, st.Tool)
	if st.Outcome != "" && st.Outcome != types.OutcomeOK {
		line += fmt.Sprintf(" outcome=%s", st.Outcome)
	}
	if st.Retried {
		line += fmt.Sprintf(" 第 %d 次尝试", st.Attempt)
	}
	if st.Carried {
		line += " 沿用（未执行）"
	}
	if st.Deduped {
		line += " 复用"
	}
	if st.Error != "" {
		line += "\n        " + types.Shorten(st.Error, 160)
		if st.ErrorKind != "" {
			line += fmt.Sprintf("（%s）", st.ErrorKind)
		}
	}
	return line
}

func replayKindLabel(k types.ReplayKind) string {
	switch k {
	case types.ReplayResume:
		return "恢复"
	case types.ReplayFork:
		return "分叉"
	default:
		return "原样重跑"
	}
}

func forkSummary(tool string, args map[string]any) string {
	parts := []string{}
	if tool != "" {
		parts = append(parts, "工具="+tool)
	}
	if len(args) > 0 {
		if b, err := json.Marshal(args); err == nil {
			parts = append(parts, "参数="+string(b))
		}
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, " · ")
}

// renderDiff 逐步对比两条记录。
//
// 这是"并行比较"的落地：分叉与原来的差异、两次重跑之间的差异、原任务与恢复后的差异，
// 都走这**一份**实现——两处各写一份 diff，迟早会出现两边对同一个变化给出不同判断。
func renderDiff(a, b runView) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\n对比（%s → %s）：\n", describeView(a), describeView(b))
	if a.Status != "" || b.Status != "" {
		fmt.Fprintf(&out, "  状态：  %s → %s\n", orDash(string(a.Status)), orDash(string(b.Status)))
	}
	fmt.Fprintf(&out, "  结果：  成功 %d / 失败 %d / 跳过 %d / 空结果 %d（共 %d 步） → 成功 %d / 失败 %d / 跳过 %d / 空结果 %d（共 %d 步）\n",
		a.Succeeded, a.Failed, a.Skipped, a.Empty, len(a.Steps),
		b.Succeeded, b.Failed, b.Skipped, b.Empty, len(b.Steps))
	if a.Carried > 0 || b.Carried > 0 {
		// 沿用步数必须印：不印的话，"这次成功 3 步"会被读成"这次跑了 3 步"，
		// 而实际上可能只有 1 步是这次跑的。
		fmt.Fprintf(&out, "  沿用：  %d 步 → %d 步（沿用=这次没有执行，结果取自上一次）\n", a.Carried, b.Carried)
	}
	fmt.Fprintf(&out, "  重试：  %d → %d 个步骤经历重试\n", retriedCount(a.Steps), retriedCount(b.Steps))
	fmt.Fprintf(&out, "  耗时：  %dms → %dms\n", a.DurationMs, b.DurationMs)

	// 逐步差异：计划相同、结果不同的步骤，就是环境、数据或走法变了的证据。
	fmt.Fprintf(&out, "  逐步：\n")
	old := map[string]types.StepResult{}
	for _, st := range a.Steps {
		old[st.StepID] = st
	}
	seen := map[string]bool{}
	for _, st := range b.Steps {
		seen[st.StepID] = true
		o, ok := old[st.StepID]
		if !ok {
			fmt.Fprintf(&out, "    %s：A 里不存在此步骤结果\n", st.StepID)
			continue
		}
		same := o.Status == st.Status && o.Outcome == st.Outcome && o.Tool == st.Tool
		mark := "一致"
		if !same {
			if o.Tool != st.Tool {
				mark = fmt.Sprintf("换工具（%s → %s）", o.Tool, st.Tool)
			} else {
				mark = fmt.Sprintf("变化（%s → %s）", orOK(o.Status, o.Outcome), orOK(st.Status, st.Outcome))
			}
		}
		fmt.Fprintf(&out, "    %s (%s)：%s", st.StepID, st.Tool, mark)
		if st.Carried {
			fmt.Fprintf(&out, " [沿用]")
		}
		if !same && st.Error != "" {
			fmt.Fprintf(&out, "：%s", types.Shorten(st.Error, 120))
		}
		fmt.Fprintln(&out)
	}
	for _, st := range a.Steps {
		if !seen[st.StepID] {
			fmt.Fprintf(&out, "    %s：B 里不存在此步骤结果（当时 %s）\n", st.StepID, orOK(st.Status, st.Outcome))
		}
	}
	return out.String()
}

func describeView(v runView) string {
	label := v.Label
	if label == "" {
		label = "记录"
	}
	return fmt.Sprintf("%s %s", label, v.Ref)
}

func orOK(st types.StepStatus, oc types.StepOutcome) string {
	if oc == "" {
		return string(st)
	}
	return string(st) + "/" + string(oc)
}

func retriedCount(steps []types.StepResult) int {
	n := 0
	for _, st := range steps {
		if st.Retried {
			n++
		}
	}
	return n
}

func stepIDs(plan types.Plan) []string {
	out := make([]string, 0, len(plan.Steps))
	for _, st := range plan.Steps {
		out = append(out, st.ID)
	}
	return out
}

// carriedWarning 在恢复/分叉之前提醒：沿用下来的步骤里有没成功的。
//
// 那种情况下目标步大概率会因"依赖未满足"被跳过——**那正是当时的前提，不是 bug**。
// 不说的话，用户会以为 --from 坏了，然后改用 --rerun 把副作用又跑一遍。
func carriedWarning(carried map[string]types.StepResult, plan types.Plan, target string) string {
	var bad []string
	for _, st := range plan.Steps {
		if st.ID == target {
			break
		}
		r, ok := carried[st.ID]
		if !ok {
			continue
		}
		if r.Status != types.StepSucceeded || r.Outcome == types.OutcomeFailed {
			bad = append(bad, st.ID)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	sort.Strings(bad)
	return fmt.Sprintf("提醒：沿用下来的步骤 %s 当时没有成功，依赖它们的步骤这次仍会被跳过——"+
		"这是当时的前提，不是恢复失败。要改这个前提，请把起点提前到那一步。", strings.Join(bad, "、"))
}

// rerunExecutor 组装重跑用的执行器，并把"这套参数从哪来"一起交出来。
//
// 单独成函数是为了让"用快照"这件事本身可被测试钉住：口径算对了不等于接上了——
// 把 resolveRerunParams 写对、却在这里传 nil 或活配置，单元测试照样全绿，
// 而每一次重跑都会用今天的配置去比昨天的结果（这是 P4-2b 栽过的同一个坑）。
// runID 是这次回放自己的 ID（与源任务区分开），dataDir 为空则不落运行日志。
func rerunExecutor(goal string, snap *types.ConfigSnapshot, live *config.Config, reg *registry.Registry,
	gate *safety.Gate, dataDir, runID string) (*agent.Executor, rerunParams) {
	params := resolveRerunParams(snap, live)
	e := &agent.Executor{
		Reg:            reg,
		Gate:           gate,
		Notifier:       agent.NopNotifier{},
		MaxConcurrency: params.MaxConcurrency,
		StepTimeout:    params.StepTimeout,
		StepRetries:    params.StepRetries,
		Dedupe:         params.Dedupe,
		MaxOutputRunes: params.MaxOutputRunes,
		Goal:           goal,
	}
	// 运行日志也在这里接：回放同样可能跑到一半就没了（用户关窗口、强杀），
	// 而回放记录是**跑完才写**的。少了这根线，一次没跑完的恢复在事后什么都查不到。
	//
	// 接在这里而不是调用点，是为了让这根线可被测试钉住——写在调用点，
	// 单测照样全绿，而每次回放都没留下任何运行中的凭据（接线断言的老坑）。
	if dataDir != "" {
		e.Sink = agent.NewRunLog(dataDir)
	}
	return e, params
}

// rerunParams 是重跑真正喂给执行器的参数。
//
// 单独拎成一个类型，是因为这几项有一个必须做对的选择：**用哪一套**。
// 复现实验的前提是只动一个变量；拿当前配置重跑，比出来的差异分不清是环境变了还是配置变了。
type rerunParams struct {
	MaxConcurrency int
	StepTimeout    time.Duration
	StepRetries    int
	Dedupe         bool
	MaxOutputRunes int
	// Snapshot 本次回放实际生效的配置快照：与 GoalResult 的那一份同源，
	// 落进回放记录后，两次回放之间的差异也能归因到"是不是换了配置"。
	Snapshot *types.ConfigSnapshot
	// FromSnapshot 记录这套参数是取自任务启动时的快照，还是回退的当前配置。
	// 不记的话，回退会被读成复现——"以为在复现、其实变量变了两个"是最坏的复盘姿势。
	FromSnapshot bool
}

// resolveRerunParams 优先用任务启动时的快照，快照缺失才回退当前配置。
//
// 为什么要留回退：本字段落地之前产出的任务文件里没有 config_snapshot，
// 直接拒绝重跑会让所有历史任务失去重跑能力——那是比"参数不精确"更差的取舍。
// 但回退必须被说出来（见 FromSnapshot / Origin），否则读的人会把结果当成可复现的对比。
func resolveRerunParams(snap *types.ConfigSnapshot, live *config.Config) rerunParams {
	if snap != nil {
		return rerunParams{
			MaxConcurrency: snap.MaxConcurrency,
			StepTimeout:    time.Duration(snap.StepTimeoutSecs) * time.Second,
			StepRetries:    snap.StepRetries,
			Dedupe:         snap.DedupeCalls,
			MaxOutputRunes: snap.MaxOutputRunes,
			Snapshot:       snap,
			FromSnapshot:   true,
		}
	}
	if live == nil {
		return rerunParams{}
	}
	fallback := live.Snapshot()
	return rerunParams{
		MaxConcurrency: live.Agent.MaxConcurrency,
		StepTimeout:    time.Duration(live.Agent.StepTimeoutSecs) * time.Second,
		StepRetries:    live.Agent.StepRetries,
		Dedupe:         live.Agent.DedupeCalls,
		MaxOutputRunes: live.Agent.MaxOutputRunes,
		// 回退时也把实际用的那套记下来：只写"回退了"而不写"回退成什么"，
		// 事后仍然答不上来这次到底是用什么跑的。
		Snapshot: &fallback,
	}
}

// Origin 用一句话说明这套参数从哪来。这句话必须印出来：不说，回退就会被当成复现。
func (p rerunParams) Origin() string {
	if p.FromSnapshot {
		return "参数取自任务启动时的快照（本次只动\"环境\"这一个变量）"
	}
	return "参数回退当前配置（该任务没有快照）——差异里混着配置变化，不能全归给环境"
}

// describeSnapshot 把快照压成一行。快照里本来就只有"影响行为的关键项"，
// 但逐字段平铺仍看不出哪几项决定了结果，所以按用途分三段：模型 → 停止条件 → 单步执行。
func describeSnapshot(s *types.ConfigSnapshot) string {
	model := s.Model
	if model == "" {
		model = "未指定"
	}
	if s.Provider != "" {
		model = s.Provider + "/" + model
	}
	parts := []string{"模型 " + model}
	if s.FastModel != "" {
		parts = append(parts, "快模型 "+s.FastModel)
	}
	if len(s.Tiers) > 0 {
		keys := make([]string, 0, len(s.Tiers))
		for k := range s.Tiers {
			keys = append(keys, k)
		}
		// map 遍历顺序随机：不排序，同一份快照每次印出来的顺序都不一样，
		// 而"这次和上次印得不一样"会被误读成"配置变了"。
		sort.Strings(keys)
		pairs := make([]string, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, k+"="+s.Tiers[k])
		}
		parts = append(parts, "档位 "+strings.Join(pairs, "，"))
	}
	if s.SafetyMode != "" {
		parts = append(parts, "安全模式 "+s.SafetyMode)
	}
	parts = append(parts,
		fmt.Sprintf("完成阈值 %d", s.DoneThreshold),
		fmt.Sprintf("最大重规划 %d", s.MaxReplans),
		fmt.Sprintf("最大步数 %d", s.MaxSteps),
	)
	parts = append(parts,
		fmt.Sprintf("并发 %d", s.MaxConcurrency),
		"单步超时 "+limitLabel(s.StepTimeoutSecs, "s"),
		fmt.Sprintf("单步重试 %d", s.StepRetries),
		"去重 "+onOff(s.DedupeCalls),
		"输出预算 "+limitLabel(s.MaxOutputRunes, " 字符"),
		"工具菜单 "+limitLabel(s.MaxToolSchemas, " 个"),
		"逐步反思 "+onOff(s.ReflectEachStep),
	)
	return strings.Join(parts, " · ")
}

// onOff 把布尔印成开/关——"去重 false"要读的人自己翻译一次。
func onOff(v bool) string {
	if v {
		return "开"
	}
	return "关"
}

// limitLabel 把"0 表示不限"的预算印成不限。直接印 0 会被读成"零预算"，
// 与真实语义正好相反——这种字段印错比不印更坏。
func limitLabel(n int, unit string) string {
	if n <= 0 {
		return "不限"
	}
	return fmt.Sprintf("%d%s", n, unit)
}
