package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// Executor 计划执行器：DAG 依赖调度 + 协程并发 + 单步超时（可重试）+ 安全门控审批。
type Executor struct {
	Reg            *registry.Registry
	Gate           *safety.Gate
	Notifier       Notifier
	MaxConcurrency int
	StepTimeout    time.Duration
	StepRetries    int  // 超时后的自动重试次数（设计文档 §9：超时后重试或跳过）
	Dedupe         bool // 低价值调用治理：复用重复的只读调用、跳过已知失败的重复调用
	// MaxOutputRunes 单条工具输出注入下游（引用替换）时的字符预算，0 表示不限。
	// 大输出不先处理就追加，一个 file.read 就能把后续步骤的参数和上下文撑爆。
	MaxOutputRunes int
	// DataDir 数据目录：超长输出被截断时，完整原文落盘到
	// `<DataDir>/tool-output/<taskID>/<stepID>.txt`（缓存，不是归档）。
	// 空值时退化为纯截断——不报错，因为落盘失败不该让任务失败。
	DataDir string
	// Reviewer 可选：审核器。对"本来会被自动放行"的中高风险动作先做一次独立快筛，
	// 被标记才升级人工——审批量一大，人就会变成无脑点同意。
	Reviewer Reviewer
	// Goal 本次任务的用户目标（审核器需要它判断"这算合理解读吗"）。
	Goal string
	// OnUsage 可选：上报工具调用/重试/去重次数（taskID, toolCalls, retries, deduped），供消耗看板统计
	OnUsage func(taskID string, toolCalls, retries, deduped int)
	// Carried 可选：stepID -> 上一次运行的结果。命中的步骤**这次不执行**，
	// 直接把当时的结果填进去，并立刻放行依赖它的步骤。
	//
	// 为什么要有它：`replay --rerun` 会把整条计划真实再跑一遍——一个 20 步的任务在第
	// 17 步失败，从头重跑等于把前 16 步的副作用（写文件、发请求）再做一遍。恢复的
	// 语义是"直接接在断点之后"，所以前面的步骤要**沿用结果而不是重做**。
	//
	// 它是"预置结果"而不是"跳过"：依赖解析、下游参数替换、汇总计数全部照常工作，
	// 与真跑出来的结果走同一条路——两条路各写一遍，迟早会出现"复用时的行为不一样"。
	Carried map[string]types.StepResult
	// Sink 可选：每步结束后回调一次，用于运行中落盘（append-only 步骤日志）。
	//
	// 为什么必须"运行中"：任务记录是跑完之后写一次的终态快照，进程若在运行中退出，
	// 那次运行**什么都没有**——没有记录就没有回放、没有归因、没有"跑到哪一步了"。
	Sink StepSink
}

// StepSink 接收每一步的结果，用于运行中落盘。
//
// 刻意只给「已经出结果的那一步」，不给中间态：追加式日志的价值是"每一步都有据可查"，
// 而写入方不需要理解执行器内部结构——接口越窄，越不容易在演进中被改坏。
type StepSink interface {
	RecordStep(taskID string, r types.StepResult)
}

// Reviewer 审核器：在动作执行前做一次独立判断。
// 刻意只给「用户目标 + 要执行的动作」，不给 Agent 的推理过程——
// Agent 能给任何动作配上说得过去的理由，让审核者看到推理就等于让它自己审自己。
type Reviewer interface {
	Review(userGoal, tool string, args map[string]any) (approved bool, reason string)
}

// reportUsage 上报一次工具执行、重试或被去重省下的调用。
func (e *Executor) reportUsage(taskID string, toolCalls, retries, deduped int) {
	if e == nil || e.OnUsage == nil {
		return
	}
	e.OnUsage(taskID, toolCalls, retries, deduped)
}

// readOnly 判断工具在当前门控下是否为只读（只读结果才允许被复用）。
func (e *Executor) readOnly(tool types.Tool) bool {
	if tool == nil {
		return false
	}
	if e.Gate == nil {
		return tool.Permission() == types.PermissionReadOnly
	}
	return e.Gate.EffectivePermission(tool) == types.PermissionReadOnly
}

// capOutput 按字符预算收敛一次引用替换取到的值。
// 字符串直接截断；结构化输出先序列化量一下规模，超限才退化为截断后的文本——
// 这样既不改变常见小结果的类型，又能拦住"一个 500KB 文件灌进下游参数"。
//
// spillID 是**产出这段内容的那一步**的 ID（不是正在引用它的那一步）：
// 落盘文件名要跟着内容的来源走，否则同一步引用两个来源时会互相覆盖，
// 而模型拿到的路径会指向另一段内容——比不给路径更坏。
func (e *Executor) capOutput(v any, taskID, spillID string) any {
	if e.MaxOutputRunes <= 0 {
		return v
	}
	if s, ok := v.(string); ok {
		return e.fitRunes(s, taskID, spillID)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	if len([]rune(string(b))) <= e.MaxOutputRunes {
		return v
	}
	return e.fitRunes(string(b), taskID, spillID)
}

// spillMinBudget 低于这个预算就不落盘。
//
// 提示语本身（含一条落盘路径）要占几十上百字，预算太小时它会**把正文挤没**；
// 而"落了盘但提示语里放不下路径"等于没落——没人知道去哪取。
// 所以小预算下老实纯截断，不假装还有后路。
const spillMinBudget = 160

// truncNoteMinBody 提示语之外至少要给正文留这么多字，否则退回纯截断。
//
// 这条是硬约束的兜底：**结果长度不能超过预算**。预算存在的意义就是
// "注入下游的东西有上限"；如果一条长路径能让它超标，那这个上限就是假的。
const truncNoteMinBody = 80

// fitRunes 按「头 + 尾 + 省略说明」截断，保留两端信息量最高的部分，
// 并明确告诉模型被截断了、原始多长、**完整内容在哪**（不能让它以为看到的就是全部）。
//
// **为什么要把完整原文落盘**：头 3/5 + 尾 2/5 看起来照顾了两端，但丢掉的中段
// 可能正是关键（一份两万字日志里的第 1 万行）。原来的兜底是"让模型自己再调
// file.read 按范围取"——而那要求模型**先意识到自己缺了什么**，可它只看到首尾，
// 恰恰意识不到中间有什么。给一条路径，它就有机会去看。
func (e *Executor) fitRunes(s, taskID, spillID string) string {
	r := []rune(s)
	budget := e.MaxOutputRunes
	if len(r) <= budget {
		return s
	}
	if budget < spillMinBudget {
		return string(r[:budget])
	}
	// 先算提示语、再按剩余预算分配首尾。反过来做（先分配首尾再追加提示语）
	// 会让结果比预算长出"提示语那一段"，而调用方是拿预算当上限用的。
	note := e.truncNote(len(r), taskID, spillID, s)
	body := budget - len([]rune(note))
	if body < truncNoteMinBody {
		return string(r[:budget])
	}
	head := body * 3 / 5
	tail := body - head
	return string(r[:head]) + note + string(r[len(r)-tail:])
}

// truncNote 截断提示语：说清被截断了、原始多长、完整内容在哪（或为什么取不到）。
func (e *Executor) truncNote(total int, taskID, spillID, content string) string {
	if p, err := SpillOutput(e.DataDir, taskID, spillID, content); err == nil {
		return fmt.Sprintf("\n…[已截断：原始 %d 字。完整内容见 %s（临时文件，任务结束后可能被清理）]…\n", total, p)
	} else {
		// 落盘失败要说出来，不能装作没发生：否则模型以为"完整内容可取"却取不到，
		// 于是反复重试同一个引用，而真正的原因（磁盘/权限/没配数据目录）一次都没被说出来。
		return fmt.Sprintf("\n…[已截断：原始 %d 字。完整内容未能落盘（%v），请缩小读取范围或用 file.read 按路径精确取用]…\n", total, err)
	}
}

// stepFingerprint 生成"工具 + 参数"指纹，用于识别完全相同的调用。
// encoding/json 对 map 按键排序序列化，因此同参数得到的字符串是稳定的。
func stepFingerprint(tool string, args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return tool + "|<?>"
	}
	return tool + "|" + string(b)
}

// Result 一次计划执行的结果。
type Result struct {
	Steps        []types.StepResult
	ByID         map[string]*types.StepResult
	ExecutedPlan types.Plan // 参数替换后的实际执行计划（供技能固化与复现）
	ReplyText    string     // reply 步骤的回复内容
	Succeeded    int
	Failed       int
	Skipped      int
	Empty        int // 成功执行但结果为空——不是错误，但要让报告看得见
	Total        int
	Carried      int // 其中沿用上次结果、这次没有执行的步骤数
	Cancelled    bool
	// PreImages 本次任务动到的每个路径**写前**的样子（写前快照的内存侧）。
	// 盘上那份在 `snapshots/<taskID>/manifest.json`，这里带出来只为省一次读盘
	// ——任务正常跑完时内存里就有，没必要再绕一趟磁盘。
	PreImages []preImage
}

// cachedCall 被复用的只读调用结果。
// 必须连 Outcome 一起存：一个「空结果」被复用时若记成 ok，去重就把它的语义弄丢了。
type cachedCall struct {
	out     any
	outcome types.StepOutcome
	note    string
}

// execState 单次执行的可共享状态。
type execState struct {
	plan     types.Plan
	results  []types.StepResult
	finished []chan struct{}
	idxOf    map[string]int

	// 调用去重（低价值调用治理）：指纹 -> 结果
	mu       sync.Mutex
	done     map[string]cachedCall    // 已成功的只读调用：指纹 -> 输出（含结果性质）
	failed   map[string]string        // 已失败的调用：指纹 -> 错误信息
	inflight map[string]chan struct{} // 正在执行的相同调用：指纹 -> 完成信号（单飞）

	// 写前快照（本次任务共享一个记录器，内部自带锁）
	snap *snapshotter
}

// tryAcquire 认领一次调用：返回 (完成信号, 是否由本步骤执行)。
// 未抢到的步骤等待前一个相同调用结束，再复用它的结果（并发下的单飞去重）。
func (s *execState) tryAcquire(fp string) (chan struct{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.inflight[fp]; ok {
		return ch, false
	}
	ch := make(chan struct{})
	s.inflight[fp] = ch
	return ch, true
}

// release 释放认领并广播完成（须在写入缓存/失败表之后调用）。
func (s *execState) release(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.inflight[fp]; ok {
		close(ch)
		delete(s.inflight, fp)
	}
}

// cacheLoad 读取可复用的调用结果。
func (s *execState) cacheLoad(fp string) (cachedCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.done[fp]
	return v, ok
}

func (s *execState) cacheStore(fp string, out any, outcome types.StepOutcome, note string) {
	s.mu.Lock()
	s.done[fp] = cachedCall{out: out, outcome: outcome, note: note}
	s.mu.Unlock()
}

// failedLoad 读取已知失败的调用错误（用于跳过重复烧钱的调用）。
func (s *execState) failedLoad(fp string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.failed[fp]
	return v, ok
}

func (s *execState) failedStore(fp, errMsg string) {
	s.mu.Lock()
	s.failed[fp] = errMsg
	s.mu.Unlock()
}

// resultOf 仅在收到依赖步骤的 finished 信号后调用（channel 保证 happens-before）。
func (s *execState) resultOf(id string) (*types.StepResult, bool) {
	i, ok := s.idxOf[id]
	if !ok {
		return nil, false
	}
	<-s.finished[i]
	return &s.results[i], true
}

// record 把一步的结果追加进运行日志（配了 Sink 才写）。
//
// 写失败不阻断执行：日志是治理地基，不是执行前提——为了留痕而让任务失败，
// 是拿"能不能跑完"去换"能不能查"，这个交换不成立。Sink 实现负责自己吞掉错误。
func (e *Executor) record(taskID string, r types.StepResult) {
	if e.Sink == nil {
		return
	}
	e.Sink.RecordStep(taskID, r)
}

// Execute 执行计划。taskID 用于进度事件关联；mode 为执行模式；
// preApproved 表示计划已整体批准（plan_first），中风险步骤免重复审批。
func (e *Executor) Execute(ctx context.Context, plan types.Plan, taskID, mode string, preApproved bool) *Result {
	res := &Result{Total: len(plan.Steps), ByID: map[string]*types.StepResult{}}
	if res.Total == 0 {
		return res
	}
	res.ExecutedPlan = types.Plan{Goal: plan.Goal, EstimatedTime: plan.EstimatedTime}

	state := &execState{
		plan:     plan,
		results:  make([]types.StepResult, res.Total),
		finished: make([]chan struct{}, res.Total),
		idxOf:    map[string]int{},
		done:     map[string]cachedCall{},
		failed:   map[string]string{},
		inflight: map[string]chan struct{}{},
		snap:     newSnapshotter(e.DataDir, taskID),
	}
	for i := range state.finished {
		state.finished[i] = make(chan struct{})
	}
	for i, st := range plan.Steps {
		state.idxOf[st.ID] = i
	}

	// 恢复：把上一次运行的结果预置进去，并**先于所有 goroutine** 放行依赖它们的步骤。
	// 顺序很重要——预置必须在启动 goroutine 之前完成，否则依赖方会等一个永远不关的
	// 完成信号（或读到空结果），而那种失败是随机的、只在特定并发交错下出现。
	carried := 0
	for i, st := range plan.Steps {
		prev, ok := e.Carried[st.ID]
		if !ok {
			continue
		}
		prev.StepID = st.ID
		prev.Tool = st.Tool
		prev.Carried = true
		state.results[i] = prev
		close(state.finished[i])
		// 沿用的步骤也要落日志：运行日志要能逐字对应 Result.Steps（含沿用步），
		// 漏掉它们，一次中途退出的恢复在日志里会看起来"少了前几步"——读的人
		// 分不清是沿用了还是丢了。而且终态记录与从日志重建的视图会给出不同的
		// 「沿用」计数，正是两处各算一遍的老问题。
		e.record(taskID, state.results[i])
		carried++
	}

	conc := e.MaxConcurrency
	if conc <= 0 {
		conc = 8
	}
	sem := make(chan struct{}, conc)
	timeout := e.StepTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	var doneCount atomic.Int64
	doneCount.Store(int64(carried)) // 沿用的步骤一开始就算"已完成"，进度才不会是假的
	var wg sync.WaitGroup
	total := int64(res.Total)
	notify := func(ev types.ProgressEvent) {
		if e.Notifier != nil {
			e.Notifier.OnProgress(ev)
		}
	}
	if carried > 0 {
		notify(progress(taskID, "execute",
			fmt.Sprintf("开始执行 %d 个步骤（前 %d 步沿用上次结果，不会重新执行）", total, carried), 10, "info"))
	} else {
		notify(progress(taskID, "execute", fmt.Sprintf("开始执行 %d 个步骤", total), 10, "info"))
	}

	for i := range plan.Steps {
		if _, ok := e.Carried[plan.Steps[i].ID]; ok {
			// 已预置：不执行，也不再 close（通道在上面已经关过，重复关闭会 panic）
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			step := plan.Steps[i]

			// 等待依赖完成
			var depFailed []string
			for _, dep := range step.DependsOn {
				if _, exists := state.idxOf[dep]; !exists {
					continue
				}
				dr, _ := state.resultOf(dep)
				if dr == nil || dr.Status != types.StepSucceeded {
					depFailed = append(depFailed, dep)
				}
			}
			if len(depFailed) > 0 {
				state.results[i] = types.StepResult{
					StepID: step.ID, Tool: step.Tool, Status: types.StepSkipped,
					Error:     fmt.Sprintf("依赖步骤未成功: %s", strings.Join(depFailed, ", ")),
					StartedAt: time.Now(), FinishedAt: time.Now(),
				}
				close(state.finished[i])
				e.record(taskID, state.results[i])
				n := doneCount.Add(1)
				notify(progress(taskID, "execute", fmt.Sprintf("⏭ 跳过 %s（依赖未满足）", step.ID), progressPct(n, total, 10, 80), "warn"))
				return
			}
			// 取消检查
			if ctx.Err() != nil {
				state.results[i] = types.StepResult{
					StepID: step.ID, Tool: step.Tool, Status: types.StepSkipped,
					Error: "任务已取消", StartedAt: time.Now(), FinishedAt: time.Now(),
				}
				close(state.finished[i])
				e.record(taskID, state.results[i])
				n := doneCount.Add(1)
				notify(progress(taskID, "execute", fmt.Sprintf("⏭ 跳过 %s（任务已取消）", step.ID), progressPct(n, total, 10, 80), "warn"))
				return
			}

			// 走 guard：一次工具 panic 只该让**这一步**失败，不该让整批的结果一起消失。
			// 收尾（close / 落盘 / 计数 / 进度）刻意留在 guard 之外——它在 defer 里，
			// 而 recover 之后 goroutine 的剩余语句会被跳过，收尾放进去就会漏掉，
			// 依赖这一步的下游会永远等不到放行。
			state.results[i] = e.runStepGuarded(ctx, state, step, taskID, mode, preApproved, timeout, sem)
			close(state.finished[i])
			// 落盘放在 close 之后：依赖方不必等写文件，而"记录到哪一步"最多落后一步，
			// 不会出现"结果已放行、日志里却没有"的顺序倒挂。
			e.record(taskID, state.results[i])

			n := doneCount.Add(1)
			r := state.results[i]
			icon, kind := "✅", "info"
			if r.Status == types.StepFailed {
				icon, kind = "❌", "error"
			}
			notify(progress(taskID, "execute",
				fmt.Sprintf("%s %s %s（%d/%d）", icon, step.ID, step.Describe(), n, total),
				progressPct(n, total, 10, 80), kind))
		}(i)
	}
	wg.Wait()

	// 汇总
	for i := range state.results {
		r := state.results[i]
		res.Steps = append(res.Steps, r)
		res.ByID[r.StepID] = &res.Steps[i]
		if r.Carried {
			// 单独计数：沿用来的步骤也计进 Succeeded/Failed（它确实有那个状态），
			// 但报告必须能说出"其中几步不是这次跑的"——否则一次恢复会被读成
			// "这次全都跑通了"，而实际上大部分步骤这次根本没动。
			res.Carried++
		}
		switch {
		case r.Status == types.StepSucceeded && r.Outcome == types.OutcomeFailed:
			// 工具跑完了，但业务上失败（shell 非零退出、file.search 超时…）。
			// 以前这里计入 Succeeded，任务于是被判成 success——完成率失真的根因。
			res.Failed++
		case r.Status == types.StepSucceeded:
			res.Succeeded++
			if r.Outcome == types.OutcomeEmpty {
				res.Empty++
			}
			if r.Tool == "reply" && res.ReplyText == "" {
				if m, ok := r.Output.(map[string]any); ok {
					if text, ok := m["text"].(string); ok {
						res.ReplyText = text
					}
				}
			}
		case r.Status == types.StepFailed:
			res.Failed++
		default:
			res.Skipped++
		}
		exec := types.Step{ID: r.StepID, Tool: r.Tool, DependsOn: plan.Steps[i].DependsOn, Description: plan.Steps[i].Description}
		if r.FinalArgs != nil {
			exec.Args = r.FinalArgs
		} else {
			exec.Args = plan.Steps[i].Args
		}
		res.ExecutedPlan.Steps = append(res.ExecutedPlan.Steps, exec)
	}
	if ctx.Err() != nil {
		res.Cancelled = true
	} else {
		// 存在因取消而跳过的步骤也视为整体取消
		for _, r := range res.Steps {
			if r.Status == types.StepSkipped && strings.Contains(r.Error, "取消") {
				res.Cancelled = true
				break
			}
		}
	}
	// 落盘缓存按任务数裁剪（与任务记录同一个保留上限）。放在这里而不是任务收尾：
	// 收尾路径有三条（正常/取消/技能），挂三条里迟早漏一条——而漏掉的那条会一直涨。
	// 代价是每跑一个任务扫一次目录，与"无界增长"比可以忽略。
	PruneSpill(e.DataDir, spillMaxTasks)
	// 写前快照同一条路、同一个理由。它比落盘缓存更该有上限：一份 1MB 的旧内容是
	// 实打实的用户文件副本，不设限等于"跑几百个任务之后由磁盘来决定什么时候满"。
	PruneSnapshots(e.DataDir, snapshotMaxTasks)
	res.PreImages = state.snap.records()
	return res
}

// runStepGuarded 是 runStep 的 panic 边界：工具炸了只让这一步失败，不让整批陪葬。
//
// 为什么必须有它：工具实现在**同进程**里执行，一次 nil deref / 越界 / 对空 map 写入
// 就会终止整个进程。而执行器早就把「步骤失败」建模成一等状态（StepFailed + ErrorKind），
// 没有任何理由让一次工具 panic 表现得比"这一步失败"更严重。真正被它救回来的场景是**批**：
// `gleam eval` 跑 200 条用例时，第 5 条的工具 panic 不该让另外 199 条的结果一起消失——
// 那些结果已经花掉了模型调用，而且重跑还要再花一次。
//
// 边界为什么放在这里、而不是放在调用方：panic 发生在**子 goroutine** 里
// （见 Execute 里 `go func(i int)`），Go 的 recover 只在**同一个 goroutine 的 defer** 里有效，
// 所以 eval runner / cmdGoal / webui handler 各自加 recover 都接不住。
// 加在这一处，四条执行路径（eval / goal / webui / 定时任务）一起受益——一个事实一个 owner。
//
// 为什么 Error 里要带栈：recover 之后 Go 不再打印任何东西，只留一个 panic 值的话，
// 「哪一行炸的」就彻底丢了，而那正是修它唯一需要的信息。
// 为什么要压栈：Error 会进 tasks/*.json 与 runs/*.jsonl，完整栈几十行、几 KB，
// 灌进去会让任务记录被一条诊断信息淹掉（本仓库对无界输出一贯的做法是先截断）。
func (e *Executor) runStepGuarded(ctx context.Context, state *execState, step types.Step, taskID, mode string, preApproved bool, timeout time.Duration, sem chan struct{}) (r types.StepResult) {
	started := time.Now()
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		r = types.StepResult{
			StepID:    step.ID,
			Tool:      step.Tool,
			Status:    types.StepFailed,
			ErrorKind: types.ErrInternal,
			Error:     panicBrief(p),
			// 没有 Output / Outcome：这一步**没有跑完**，不是"跑完了但业务失败"。
			// 编一个 Outcome 出来会让下游的归因把它当成业务失败去重试，方向就错了。
			StartedAt:  started,
			FinishedAt: time.Now(),
		}
	}()
	return e.runStep(ctx, state, step, taskID, mode, preApproved, timeout, sem)
}

// panicBrief 把 panic 压成"能定位、又有界"的一段文本。
//
// 前缀固定为「工具 panic」：能走到这个 recover 的 panic 绝大多数发生在工具实现里
// （门控与输出处理是本仓库代码、且有测试），用最可能的来源命名，读的人一眼知道去哪儿找；
// 栈的前几帧会把真实位置钉死，所以即使偶尔不是工具，也不会被这个前缀误导。
func panicBrief(p any) string {
	lines := strings.Split(string(debug.Stack()), "\n")
	// 第 0 行是 "goroutine N [running]:"，1..6 帧足够看出炸在哪个函数。
	if len(lines) > 7 {
		lines = lines[:7]
	}
	return fmt.Sprintf("工具 panic: %v\n%s", p, strings.TrimRight(strings.Join(lines, "\n"), "\n"))
}

// adjudicate 对一步做裁决：安全门控 →（可选）审核模型快筛。
//
// review=false 只用于"参数里还带着步骤引用"的第一次裁决：那时门控和审核模型看到的
// 都是占位符，花钱让审核模型筛一个假值没有意义，等替换成真值再筛。
//
// ---------- 审核模型快筛 ----------
// 只审"本来会被自动放行"的中高风险动作：低风险是只读，高风险本来就有人看。
// 审核不通过不直接拒绝，而是升级为人工确认——最终决定权仍在人手里。
func (e *Executor) adjudicate(ctx context.Context, tool types.Tool, args map[string]any, preApproved, review bool) safety.Decision {
	dec := e.Gate.EvaluateStepIn(ctx, tool, args, preApproved)
	if !review || dec.NeedApproval || dec.Risk == "low" || e.Reviewer == nil {
		return dec
	}
	if ok, why := e.Reviewer.Review(e.Goal, tool.Name(), args); !ok {
		if e.Gate != nil {
			e.Gate.Record(safety.AuditEntry{
				Tool: tool.Name(), Risk: dec.Risk, Action: "reviewed_block",
				Reason: "审核模型标记，已升级为人工确认", Detail: why,
			})
		}
		dec.NeedApproval = true
		dec.Reason = "审核模型认为这个动作超出了目标范围：" + why
	}
	return dec
}

// awaitApproval 走一遍人工确认：待批状态落盘 → 通知审批通道 → 等结果 → 审计留痕。
// 结果就地写进 r（被拒 = 失败，被取消 = 跳过），调用方拿到 cancelled 或 !approved
// 都直接收尾，不再往下执行。
func (e *Executor) awaitApproval(ctx context.Context, r *types.StepResult, taskID string, step types.Step, dec safety.Decision) (approved, cancelled bool) {
	req := types.ApprovalRequest{
		TaskID: taskID,
		Plan:   []string{step.Describe()},
		Risk:   dec.Risk,
		Reason: dec.Reason,
		StepID: step.ID,
	}
	// 等待审批状态落盘（P4-2）：进程若在此期间退出，重启后能列出"有任务卡在审批"，
	// 而不是静默消失——用户至少知道刚才那条任务停在哪一步、要批什么。
	if e.Gate != nil {
		e.Gate.MarkPending(safety.PendingApproval{
			TaskID: taskID, StepID: step.ID, Tool: step.Tool,
			Risk: dec.Risk, Reason: dec.Reason,
		})
	}
	// 审批有结果即摘除（批准/拒绝/超时/取消都是"有结果"）
	clearPending := func() {
		if e.Gate != nil {
			e.Gate.ClearPending(taskID, step.ID)
		}
	}
	respCh := make(chan types.ApprovalResponse, 1)
	if e.Notifier != nil {
		go func() { respCh <- e.Notifier.OnApproval(req) }()
	} else {
		respCh <- types.ApprovalResponse{Approved: false, Note: "无审批通道"}
	}
	actx, acancel := context.WithTimeout(ctx, e.Gate.ApprovalTimeoutDuration())
	var note string
	select {
	case resp := <-respCh:
		approved, note = resp.Approved, resp.Note
	case <-actx.Done():
		if ctx.Err() != nil {
			r.Status = types.StepSkipped
			r.Error = "任务已取消"
			acancel()
			clearPending()
			return false, true
		}
		note = "审批超时，自动拒绝"
	}
	acancel()
	clearPending()
	// 审批结果留痕：被拒的操作必须能事后查到，否则复盘时一片空白
	if e.Gate != nil {
		action := "approved"
		if !approved {
			action = "denied"
		}
		e.Gate.Record(safety.AuditEntry{
			Tool: step.Tool, Risk: dec.Risk, Action: action,
			Reason: dec.Reason, Detail: note,
		})
	}
	if !approved {
		r.Status = types.StepFailed
		r.Error = types.ErrStepRejected.Error()
		if note != "" {
			r.Error += "（" + note + "）"
		}
		// 权限不足类错误重试无用：结构上已知，直接定类，不走文本启发式
		r.ErrorKind = types.ErrPermission
	}
	return approved, false
}

// runStep 执行单步：安全门控审批 → 参数引用替换 → 用真值重算裁决 → 调用工具（带超时）。
// 并发槽位仅在真正调用工具期间持有：审批等待、参数替换不阻塞其他步骤（丝滑关键）。
func (e *Executor) runStep(ctx context.Context, state *execState, step types.Step, taskID, mode string, preApproved bool, timeout time.Duration, sem chan struct{}) types.StepResult {
	start := time.Now()
	r := types.StepResult{StepID: step.ID, Tool: step.Tool, Status: types.StepRunning, StartedAt: start}
	finalize := func() types.StepResult {
		r.FinishedAt = time.Now()
		r.DurationMS = time.Since(start).Milliseconds()
		return r
	}

	tool, ok := e.Reg.Get(step.Tool)
	if !ok {
		r.Status = types.StepFailed
		r.Error = fmt.Sprintf("工具 %q 不存在", step.Tool)
		r.ErrorKind = types.ErrNotFound
		return finalize()
	}

	// 安全门控：先按**计划里的字面参数**裁一次。带引用时这次看到的还是占位符，
	// 所以替换完还要再裁一次（见「派发前重算裁决」）。
	dec := e.adjudicate(ctx, tool, step.Args, preApproved, !hasRefs(step.Args))
	if dec.NeedApproval {
		if approved, cancelled := e.awaitApproval(ctx, &r, taskID, step, dec); cancelled || !approved {
			return finalize()
		}
	}

	// 引用前序步骤结果（此时依赖均已完成，读取安全）
	args, err := substituteArgs(step.Args, func(ref string) (any, bool, error) {
		id, path := splitRef(ref)
		dep, ok := state.resultOf(id)
		if !ok {
			return nil, false, fmt.Errorf("引用的步骤 %q 不存在", id)
		}
		if dep.Status != types.StepSucceeded {
			return nil, false, fmt.Errorf("引用的步骤 %q 未成功执行", id)
		}
		v, ok := navigate(dep.Output, path)
		if !ok {
			return nil, false, fmt.Errorf("步骤 %q 的结果中不存在字段 %q", id, path)
		}
		// 落盘跟着**内容的来源**（id）走，而不是跟着正在引用它的那一步：
		// 一步引用两个来源时，用引用方命名会让后一个覆盖前一个，
		// 模型拿到的路径指向另一段内容——比不给路径更坏。
		return e.capOutput(v, taskID, id), true, nil
	})
	if err != nil {
		r.Status = types.StepFailed
		r.Error = err.Error()
		// 引用替换失败是计划层面的参数问题（引用了不存在的步骤/字段），改计划才修得好
		r.ErrorKind = types.ErrParam
		return finalize()
	}
	r.FinalArgs = args

	// ---------- 派发前重算裁决 ----------
	// 上面那次裁决看的是计划里的字面值：`$ref:s1.output.path` 不是绝对路径，门控会按
	// "相对路径在工作区内"放过它，而真值可能指向信任范围外。所以在真正派发之前，
	// 用**要执行的那份参数**再裁一次。只收紧不放宽：先前已经问过人的不重复问
	// （人已经看过这一步），先前自动放行而新裁决要求批准的，重新问一次——
	// 这次的 Reason 里是真值，用户在卡片上看到的是实际会动的路径。
	final := dec
	if hasRefs(step.Args) {
		final = e.adjudicate(ctx, tool, args, preApproved, true)
		if final.NeedApproval && !dec.NeedApproval {
			if approved, cancelled := e.awaitApproval(ctx, &r, taskID, step, final); cancelled || !approved {
				return finalize()
			}
		}
	}
	if !dec.NeedApproval && !final.NeedApproval && final.Risk != "low" && e.Gate != nil {
		// 全量审计（P5-2 / 站点 F3）：自动放行的**副作用动作**也要留痕。
		// 只读不记——它们没有副作用，记下来只是噪音；审计的对象是"谁动了什么"。
		//
		// 记的是**重算之后**的风险与理由：写"路径 X 在信任范围内"而 X 是个占位符，
		// 台账就在替一次没做过的检查作证——比不记更坏，因为它读起来像查过了。
		e.Gate.Record(safety.AuditEntry{
			Tool: step.Tool, Risk: final.Risk, Action: "auto", Reason: final.Reason,
		})
	}

	// ---------- 低价值调用治理 ----------
	// 同一任务里重复的只读调用直接复用结果；已失败过的相同调用不再重试第二遍。
	// reply 不参与：它没有外部成本，重复出现时语义由模型决定。
	dedupable := e.Dedupe && step.Tool != "reply"
	var fp string
	if dedupable {
		fp = stepFingerprint(step.Tool, args)
		if hit, ok := state.cacheLoad(fp); ok {
			r.Status = types.StepSucceeded
			r.Output = hit.out
			r.Outcome = hit.outcome
			r.Error = hit.note
			r.Deduped = true
			e.reportUsage(taskID, 0, 0, 1)
			if e.Notifier != nil {
				e.Notifier.OnProgress(progress(taskID, "execute",
					fmt.Sprintf("♻️ %s 复用前序相同调用，跳过重复执行", step.ID), 0, "info"))
			}
			return finalize()
		}
		if errMsg, ok := state.failedLoad(fp); ok {
			r.Status = types.StepFailed
			r.Error = fmt.Sprintf("与已失败的调用完全相同，已跳过（原因：%s）", types.Shorten(errMsg, 120))
			// 沿用原失败的类别：跳过只是省一次注定相同的失败，不改变归因
			r.ErrorKind = types.ClassifyError(errMsg)
			r.Deduped = true
			e.reportUsage(taskID, 0, 0, 1)
			if e.Notifier != nil {
				e.Notifier.OnProgress(progress(taskID, "execute",
					fmt.Sprintf("⏭ %s 跳过重复的失败调用（%s）", step.ID, step.Describe()), 0, "warn"))
			}
			return finalize()
		}
		// 并发单飞：相同调用正在执行时，等它结束再复用结果，避免两边同时打出去
		if ch, mine := state.tryAcquire(fp); !mine {
			select {
			case <-ch:
			case <-ctx.Done():
			}
			if hit, ok := state.cacheLoad(fp); ok {
				r.Status = types.StepSucceeded
				r.Output = hit.out
				r.Outcome = hit.outcome
				r.Error = hit.note
				r.Deduped = true
				e.reportUsage(taskID, 0, 0, 1)
				return finalize()
			}
			if errMsg, ok := state.failedLoad(fp); ok {
				r.Status = types.StepFailed
				r.Error = fmt.Sprintf("与已失败的调用完全相同，已跳过（原因：%s）", types.Shorten(errMsg, 120))
				r.ErrorKind = types.ClassifyError(errMsg)
				r.Deduped = true
				e.reportUsage(taskID, 0, 0, 1)
				return finalize()
			}
			// 前序调用没有留下可用结果（被取消等），本步骤自行执行
		} else {
			_ = ch
			// 由本步骤执行；结果写入缓存后再广播给等待者
			defer state.release(fp)
		}
	}

	// ---------- 写前快照 ----------
	// 放在审批之后、去重之后、真正调用之前：被拒的步骤没有副作用，被复用的步骤
	// 这一轮也没动手，都在前面 return 了。判"要不要快照"用**工具自己的静态声明**
	// 而不是 e.readOnly()——后者问的是"在当前门控下是否只读"，用户把某个写工具
	// 设成只读时，那不该顺手关掉这道保险。
	if tool.Permission() != types.PermissionReadOnly {
		state.snap.capture(ctx, step.ID, step.Tool, tool, args)
	}

	// 带超时执行；仅在真正调用工具期间持有并发槽位（审批等待不阻塞其他步骤）；
	// 超时类错误自动重试（§9），其他错误立即失败，取消立即跳过
	sem <- struct{}{}
	defer func() { <-sem }()
	attempts := 1 + e.StepRetries
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; ; attempt++ {
		e.reportUsage(taskID, 1, 0, 0)
		tctx, cancel := context.WithTimeout(ctx, timeout)
		out, err := tool.Execute(tctx, args)
		timedOut := tctx.Err() != nil && ctx.Err() == nil
		cancel()
		if ctx.Err() != nil {
			r.Status = types.StepSkipped
			r.Error = "任务已取消"
			return finalize()
		}
		if err == nil && !timedOut {
			r.Status = types.StepSucceeded
			r.Output = out
			r.Outcome = types.OutcomeOK
			// 问一次"这次到底做成了没有"：shell 非零退出、file.search 超时这类
			// "跑完了但没做成"只有工具自己知道（原因在 payload 里，执行器看不见）。
			if rep, ok := tool.(types.OutcomeReporter); ok {
				if oc, note := rep.Outcome(args, out); oc != "" {
					r.Outcome = oc
					// 抬到 Error：否则原因只留在 payload 里，反思器与用户都看不见
					r.Error = note
					if oc == types.OutcomeFailed {
						r.ErrorKind = types.ClassifyError(note)
					}
				}
			}
			// 只读调用的结果可被后续相同调用复用（有副作用的工具不做缓存）
			if dedupable && e.readOnly(tool) {
				state.cacheStore(fp, out, r.Outcome, r.Error)
			}
			// 「一次就对」与「重试三次才对」必须在数据里可区分（否则后者是看不见的隐患）
			r.Attempt = attempt
			r.Retried = attempt > 1
			return finalize()
		}
		r.Status = types.StepFailed
		r.Outcome = types.OutcomeFailed
		if err != nil {
			if timedOut {
				r.Error = fmt.Sprintf("步骤超时（%v）: %v", timeout, err)
				r.ErrorKind = types.ErrTimeout
			} else {
				r.Error = err.Error()
				r.ErrorKind = types.ClassifyError(r.Error)
				if dedupable {
					state.failedStore(fp, r.Error)
				}
				r.Attempt = attempt
				r.Retried = attempt > 1
				return finalize()
			}
		} else {
			// 工具未响应取消信号但在超时后返回，仍按超时处理
			r.Error = fmt.Sprintf("步骤超时（%v）", timeout)
			r.ErrorKind = types.ErrTimeout
		}
		if attempt >= attempts {
			// 记住这次失败：后续完全相同的调用直接跳过，不再重复消耗
			if dedupable {
				state.failedStore(fp, r.Error)
			}
			r.Attempt = attempt
			r.Retried = attempt > 1
			return finalize()
		}
		e.reportUsage(taskID, 0, 1, 0)
		if e.Notifier != nil {
			e.Notifier.OnProgress(progress(taskID, "execute",
				fmt.Sprintf("⏳ %s 超时，自动重试（第 %d/%d 次）", step.ID, attempt, attempts-1), 0, "warn"))
		}
	}
}

// ---------- 参数引用替换 ----------

// substituteArgs 深拷贝并替换参数中的步骤引用：
//   - "$ref:s2" / "$ref:s2.field" 整值引用
//   - "{ref:s2.field}" 字符串插值
func substituteArgs(args map[string]any, resolve func(ref string) (any, bool, error)) (map[string]any, error) {
	if len(args) == 0 {
		return args, nil
	}
	return substMap(args, resolve)
}

func substMap(m map[string]any, resolve func(ref string) (any, bool, error)) (map[string]any, error) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		sv, err := substValue(v, resolve)
		if err != nil {
			return nil, err
		}
		out[k] = sv
	}
	return out, nil
}

func substValue(v any, resolve func(ref string) (any, bool, error)) (any, error) {
	switch t := v.(type) {
	case string:
		if ref, ok := exactRef(t); ok {
			v, _, err := resolve(ref)
			return v, err
		}
		if strings.Contains(t, "{ref:") {
			return interpRef(t, resolve)
		}
		return t, nil
	case map[string]any:
		return substMap(t, resolve)
	case []any:
		arr := make([]any, 0, len(t))
		for _, item := range t {
			sv, err := substValue(item, resolve)
			if err != nil {
				return nil, err
			}
			arr = append(arr, sv)
		}
		return arr, nil
	default:
		return t, nil
	}
}

func exactRef(s string) (string, bool) {
	if strings.HasPrefix(s, "$ref:") && len(s) > len("$ref:") {
		return s[len("$ref:"):], true
	}
	return "", false
}

// hasRefs 判断参数里是否还带着步骤引用（`$ref:` 整值 / `{ref:}` 插值）。
//
// 只用来决定"替换之后要不要重算裁决"：不带引用时两次裁决的输入完全相同，
// 重算只是白跑一次门控（以及一次审核模型调用，那个是要花钱的）。
func hasRefs(v any) bool {
	switch t := v.(type) {
	case string:
		if _, ok := exactRef(t); ok {
			return true
		}
		return strings.Contains(t, "{ref:")
	case map[string]any:
		for _, item := range t {
			if hasRefs(item) {
				return true
			}
		}
	case []any:
		for _, item := range t {
			if hasRefs(item) {
				return true
			}
		}
	}
	return false
}

// splitRef 拆出步骤 ID 与字段路径。
func splitRef(ref string) (id, path string) {
	if i := strings.Index(ref, "."); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// navigate 按点路径取值：map 键或数组下标。
func navigate(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	parts := strings.Split(path, ".")
	for _, p := range parts {
		switch t := v.(type) {
		case map[string]any:
			if nv, ok := t[p]; ok {
				v = nv
				continue
			}
			return nil, false
		case []any:
			idx := 0
			if _, err := fmt.Sscanf(p, "%d", &idx); err != nil || idx < 0 || idx >= len(t) {
				return nil, false
			}
			v = t[idx]
			continue
		default:
			return nil, false
		}
	}
	return v, true
}

func interpRef(s string, resolve func(ref string) (any, bool, error)) (any, error) {
	var b strings.Builder
	for {
		start := strings.Index(s, "{ref:")
		if start < 0 {
			b.WriteString(s)
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			b.WriteString(s)
			break
		}
		end += start
		b.WriteString(s[:start])
		ref := s[start+len("{ref:") : end]
		v, _, err := resolve(ref)
		if err != nil {
			return nil, err
		}
		b.WriteString(stringify(v))
		s = s[end+1:]
	}
	return b.String(), nil
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func progress(taskID, phase, msg string, pct int, kind string) types.ProgressEvent {
	return types.ProgressEvent{TaskID: taskID, Phase: phase, Message: msg, Progress: pct, Kind: kind}
}

func progressPct(done, total int64, base, span int) int {
	if total == 0 {
		return base
	}
	return base + int(int64(span)*done/total)
}
