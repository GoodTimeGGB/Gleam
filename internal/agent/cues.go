// Package agent 实现 Gleam 的自主任务引擎：Plan-Execute-Reflect 三阶段循环。
package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gleam/pkg/types"
)

// cues.go — 候补目标：从本机任务归档里浮出来的「你可能想动一下」。
//
// 完整理由在设计文档 §4.6.35，这里只留三条口径：
//  1. **只提议，不执行**。这一层没有手：不提交任务、不写文件、不改配置。
//     采纳是用户点下去之后的另一件事（接入层只记状态，见 cues_store.go）。
//  2. **卡片不落盘**。它是任务历史的一个函数：历史没变，重算一次还是这几张。
//     只有「别再提 / 已采纳」这个人的决定才落盘——否则清理逻辑会成为新的失配点。
//  3. **不调模型**。主动提议必须可复现：同一段历史刷新一次换一个说法，
//     用户就没法照着做决定；而它会在每次打开界面时烧一次出网。

// cueSignals 三条线索，**信号类型的唯一 owner**。
//
// 登记了却没人派生 = 界面上永远不会出现这一类，清单变成许愿单；
// 派生了没登记 = 这张表说的「只从这三条里想」当场变成假话。
// 双向对账：静态判据见 scripts/check-cue-owner.py（第 1 层），
// 运行时判据见 TestCues_EverySignalProducesARow（闸门只能读源码，判不出「今天到底浮没浮出一张卡」）。
var cueSignals = []string{"repeat_failure", "half_done", "tool_failure"}

// cueSignalText 信号的人话。**这一份是唯一出处**：卡片上的牌子与「已按下」列表都取它。
//
// 与 cueKindText 同一套路子：取值 owner 与显示口径分开写，加一条线索时两处都只有一处可改。
func cueSignalText(signal string) string {
	switch signal {
	case "repeat_failure":
		return "同一个目标反复失败"
	case "half_done":
		return "同一个目标每次只做一半"
	case "tool_failure":
		return "同一类失败反复出现"
	}
	return signal
}

const (
	cueRepeatMin = 2   // 同一目标失败够两次才提：一次是意外，两次才是模式
	cueHalfMin   = 2   // 同上：每次只做一半，两次起才不像碰巧
	cueKindMin   = 3   // 同一类失败累计三步以上，才说「该修的是层，不是重跑」
	cueKindTasks = 2   // 而且不能只发生在同一个任务里
	cueTaskScan  = 200 // 与 maxRetainedTasks 同量级：只从本机还留着的归档里想线索
	cueLimit     = 3   // 一屏最多三张：候补区挤满十条就不是提议，是新的待办堆
	cueGoalCut   = 40  // 卡片标题里引用原目标的截断长度
	cueEvidMax   = 3   // 每条线索最多列三次现场
)

// CueRow 一张候补目标卡。
//
// 所有字段都是**给人读的一句一句话**：界面走 textContent，所以这里不许出现 markdown
// 强调（check-cue-owner.py 第 ③ 条钉这个）——星号在上界面那一刻就是泄露。
type CueRow struct {
	ID         string   `json:"id"` // 线索指纹；稳定，「别再提」按它记
	Signal     string   `json:"signal"`
	SignalText string   `json:"signal_text"`
	Title      string   `json:"title"`
	Goal       string   `json:"goal"` // 采纳时填进输入区的那句话
	Why        string   `json:"why"`
	Evidence   []string `json:"evidence"`
	Count      int      `json:"count"`
	TraceID    string   `json:"trace_id,omitempty"`
	LastSeen   string   `json:"last_seen,omitempty"`
}

// CueLedger 一次候补视图。除卡片外还要交代**它凭什么这么说**。
type CueLedger struct {
	Rows       []CueRow           `json:"rows"`
	Suppressed []CueSuppressedRow `json:"suppressed"`
	Limit      int                `json:"limit"`
	Overflow   int                `json:"overflow"` // 够格却没挤进来的条数
	Truncated  string             `json:"truncated,omitempty"`
	Scanned    int                `json:"scanned"`
	Skipped    int                `json:"skipped"` // 读不动的归档（一条历史的损失，不打翻整页）
	Coverage   string             `json:"coverage"`
	Note       string             `json:"note,omitempty"`
}

// CueSuppressedRow 处置记录给人看的那一份：在落盘那条之上多补两句人话。
//
// 为什么不在前端把 reason/signal 翻译成中文：这两个取值的含义归这一层写死，界面再 map
// 一次就是第二个 owner，漂移的方向永远是新取值在界面上裸奔成英文（M3 那条老坑）。
type CueSuppressedRow struct {
	CueSuppression
	SignalText string `json:"signal_text"`
	ReasonText string `json:"reason_text"`
}

// traceGroup 同一个 TraceID 聚出来的一批运行。
//
// 这就是 types.go 里那句「失败聚类因此不需要额外的索引」的兑现：TraceID 由
// 「目标 + 模式 + 角色 + 模型」内容派生、不含时间戳，所以同一个输入反复出现时
// 天然落在同一组里——不管它是用户手动重提的，还是定时任务按点跑出来的。
type traceGroup struct {
	traceID  string
	goal     string
	failed   []types.GoalResult
	partial  []types.GoalResult
	everWon  bool
	lastSeen time.Time
}

// CueView 算出当前的候补目标。**只读**：不写盘、不提交任务。
func (a *Agent) CueView() CueLedger {
	results, skipped, err := ListTaskResults(a.Cfg.DataDir, cueTaskScan)
	led := CueLedger{
		Limit:    cueLimit,
		Scanned:  len(results),
		Skipped:  skipped,
		Coverage: cueCoverage(len(results), skipped),
	}
	if err != nil {
		// 归档目录读不动不是「没有线索」，得说清楚这一页为什么是空的。
		led.Note = fmt.Sprintf("任务归档读不动，这一页什么都没算出来：%v", err)
		return led
	}
	kept := loadCueStore(a.Cfg.DataDir)
	led.Suppressed = kept.list()
	rows := deriveCues(results, kept)
	if len(rows) > cueLimit {
		// 够格却没挤进来的条数要说出来：静默截断会让人以为「就这三条」。
		// 这句话也归这里算——界面只把字段原样印出来，两处各写一版就会漂移。
		led.Overflow = len(rows) - cueLimit
		led.Truncated = fmt.Sprintf("另有 %d 条够格但没挤进这一屏：名额按严重程度排，前面的占满了。", led.Overflow)
		rows = rows[:cueLimit]
	}
	led.Rows = rows
	led.Note = kept.note()
	return led
}

// deriveCues 三条线索各浮一次卡，滤掉已处置的，再按严重度排。
//
// 排序不靠 map 遍历顺序（那是随机的）：先次数、再指纹。
// 同一段历史两次算出来的结果必须一字不差——否则「别再提」记不住它提过什么。
// 截断留给调用方：处置入口要能找到没挤进配额的那张卡。
func deriveCues(results []*types.GoalResult, kept *cueStore) []CueRow {
	var rows []CueRow
	groups := groupByTrace(results)
	kinds := tallyFailureKinds(results)
	for _, signal := range cueSignals {
		switch signal {
		case "repeat_failure":
			rows = append(rows, repeatFailureRows(groups)...)
		case "half_done":
			rows = append(rows, halfDoneRows(groups)...)
		case "tool_failure":
			rows = append(rows, toolFailureRows(kinds)...)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].ID < rows[j].ID
	})
	// 已采纳/已说别再提的不再浮出来，但**不占配额**：它们仍在 suppressed 里可撤销。
	alive := make([]CueRow, 0, len(rows))
	for _, r := range rows {
		if !kept.muted(r.ID) {
			alive = append(alive, r)
		}
	}
	return alive
}

// ---------- 线索一：同一个目标反复失败 ----------

func repeatFailureRows(groups map[string]*traceGroup) []CueRow {
	var rows []CueRow
	for _, g := range groups {
		if len(g.failed) < cueRepeatMin {
			continue
		}
		rows = append(rows, CueRow{
			ID:         "repeat_failure:" + g.traceID,
			Signal:     "repeat_failure",
			SignalText: cueSignalText("repeat_failure"),
			Title:      fmt.Sprintf("「%s」已经失败 %d 次", cutRunes(g.goal, cueGoalCut), len(g.failed)),
			Goal: fmt.Sprintf("查清「%s」为什么反复失败：列出每一次的失败类别与最后一步的错误，给出一个改走的方案（把目标拆小 / 换任务模式 / 补上缺失的引用），不要照原样再跑一遍",
				cutRunes(g.goal, cueGoalCut)),
			Why:      "同一个输入配同一个模型，trace_id 才会相同——它反复出现说明卡住的是做法，不是运气。",
			Evidence: g.evidence(),
			Count:    len(g.failed),
			TraceID:  g.traceID,
			LastSeen: stamp(g.lastSeen),
		})
	}
	return rows
}

// ---------- 线索二：同一个目标每次只做一半 ----------

func halfDoneRows(groups map[string]*traceGroup) []CueRow {
	var rows []CueRow
	for _, g := range groups {
		if len(g.partial) < cueHalfMin {
			continue
		}
		// 中间成功过一次就不提了：这条路走得通，剩下的半路更像偶发，不值得占一张卡。
		if g.everWon {
			continue
		}
		// 同一条 TraceID 上两种线索都够格时只浮更严重的那张：两张卡说的是同一件事。
		if len(g.failed) >= cueRepeatMin {
			continue
		}
		rows = append(rows, CueRow{
			ID:         "half_done:" + g.traceID,
			Signal:     "half_done",
			SignalText: cueSignalText("half_done"),
			Title:      fmt.Sprintf("「%s」连着 %d 次停在半路", cutRunes(g.goal, cueGoalCut), len(g.partial)),
			Goal: fmt.Sprintf("给「%s」定几条能核对到底的验收标准，再找出每次都停在同一处的原因",
				cutRunes(g.goal, cueGoalCut)),
			Why:      "每次都停在同一个地方，通常是承诺本身含糊——不是再试一次就能过。",
			Evidence: g.evidence(),
			Count:    len(g.partial),
			TraceID:  g.traceID,
			LastSeen: stamp(g.lastSeen),
		})
	}
	return rows
}

// ---------- 线索三：同一类失败跨任务反复出现 ----------

func toolFailureRows(kinds map[types.ErrorKind]*kindTally) []CueRow {
	var rows []CueRow
	for _, k := range kinds {
		if k.steps < cueKindMin || len(k.tasks) < cueKindTasks {
			continue
		}
		human := cueKindText(k.kind)
		rows = append(rows, CueRow{
			ID:         "tool_failure:" + string(k.kind),
			Signal:     "tool_failure",
			SignalText: cueSignalText("tool_failure"),
			Title:      fmt.Sprintf("%s 在最近 %d 个任务里出现了 %d 次", human, len(k.tasks), k.steps),
			Goal: fmt.Sprintf("排查「%s」这类失败在最近 %d 个任务里出现 %d 次的原因：先复现一次，再判断该改的是工具、权限还是参数；不要靠重跑碰运气",
				human, len(k.tasks), k.steps),
			Why:      "归因分布回答的是「该先修哪一层」；同类失败跨任务反复出现时，重跑只是把同一堵墙再撞一遍。",
			Evidence: k.evidence(),
			Count:    k.steps,
			LastSeen: stamp(k.last),
		})
	}
	return rows
}

// kindTally 一类失败在近期归档里的累计。
type kindTally struct {
	kind   types.ErrorKind
	steps  int
	tasks  map[string]bool
	sample string
	last   time.Time
}

func (k *kindTally) evidence() []string {
	out := []string{fmt.Sprintf("最后一次：%s", stamp(k.last))}
	if k.sample != "" {
		out = append(out, "现场："+cutRunes(k.sample, 80))
	}
	out = append(out, fmt.Sprintf("涉及 %d 个任务、累计 %d 步", len(k.tasks), k.steps))
	return out
}

// ---------- 聚合（全部只读） ----------

func groupByTrace(results []*types.GoalResult) map[string]*traceGroup {
	groups := map[string]*traceGroup{}
	for _, g := range results {
		if g == nil || g.TraceID == "" {
			continue // 对话模式与更早的归档没有 trace_id：不硬凑一组
		}
		tg := groups[g.TraceID]
		if tg == nil {
			tg = &traceGroup{traceID: g.TraceID, goal: g.Goal}
			groups[g.TraceID] = tg
		}
		if tg.goal == "" {
			tg.goal = g.Goal
		}
		if g.FinishedAt.After(tg.lastSeen) {
			tg.lastSeen = g.FinishedAt
		}
		switch g.Status {
		case types.GoalSuccess:
			tg.everWon = true
		case types.GoalFailed, types.GoalCancelled:
			tg.failed = append(tg.failed, *g)
		case types.GoalPartial:
			tg.partial = append(tg.partial, *g)
		default:
			// 还在 running 或出现新的状态值：它既不算失败也不算半路，不硬塞进任何一组。
		}
	}
	return groups
}

func tallyFailureKinds(results []*types.GoalResult) map[types.ErrorKind]*kindTally {
	kinds := map[types.ErrorKind]*kindTally{}
	for _, g := range results {
		if g == nil {
			continue
		}
		for k, n := range g.FailureBreakdown {
			t := kinds[k]
			if t == nil {
				t = &kindTally{kind: k, tasks: map[string]bool{}}
				kinds[k] = t
			}
			t.steps += n
			t.tasks[g.TaskID] = true
			if g.FinishedAt.After(t.last) {
				t.last = g.FinishedAt
			}
		}
		if t := strings.TrimSpace(g.Error); t != "" && len(g.FailureBreakdown) == 0 {
			// 归档里只有错误文本、没有归因（更早的记录）：按分类器补一次归类，不改变历史文件。
			k := types.ClassifyError(t)
			t2 := kinds[k]
			if t2 == nil {
				t2 = &kindTally{kind: k, tasks: map[string]bool{}}
				kinds[k] = t2
			}
			t2.steps++
			t2.tasks[g.TaskID] = true
			if t2.sample == "" {
				t2.sample = t
			}
			if g.FinishedAt.After(t2.last) {
				t2.last = g.FinishedAt
			}
		}
		for i := range g.Steps {
			st := g.Steps[i]
			if st.ErrorKind == "" {
				continue
			}
			t := kinds[st.ErrorKind]
			if t == nil {
				t = &kindTally{kind: st.ErrorKind, tasks: map[string]bool{}}
				kinds[st.ErrorKind] = t
			}
			if t.sample == "" && st.Error != "" {
				t.sample = fmt.Sprintf("%s：%s", st.Tool, st.Error)
			}
		}
	}
	return kinds
}

// ---------- 文案（人话 owner 在这里） ----------

// cueKindText 失败归因的人话。**这一份是唯一的人话出处**：界面拿它，候补卡也拿它，
// 别处再写一版就会漂移——漂移的方向通常是新加的那一类在某处裸奔成英文。
func cueKindText(k types.ErrorKind) string {
	switch k {
	case types.ErrParam:
		return "参数错（改参数可重试）"
	case types.ErrFormat:
		return "格式错（改格式可重试）"
	case types.ErrBusiness:
		return "业务拒绝（换方案，重试无用）"
	case types.ErrPermission:
		return "权限不足（重试无用）"
	case types.ErrNotFound:
		return "资源不存在（换目标）"
	case types.ErrTimeout:
		return "超时或被限流（退避重试）"
	case types.ErrUpstream:
		return "上游服务异常（有限重试）"
	case types.ErrInternal:
		return "工具自身缺陷（要改代码）"
	case types.ErrUnknown:
		return "归不了类的失败"
	}
	return string(k)
}

// cueCoverage 交代这张表凭什么这么说。
//
// 刻意不写成「全部历史」：tasks/ 会按任务数裁剪，看不见的那部分不是没有发生。
func cueCoverage(scanned, skipped int) string {
	s := fmt.Sprintf("这一页只从本机还留着的任务归档里想线索：读了 %d 条", scanned)
	if skipped > 0 {
		s += fmt.Sprintf("，另有 %d 条读不动已跳过", skipped)
	}
	s += fmt.Sprintf("；更早的归档已按任务数裁剪，看不见不等于没发生。最多列 %d 张卡。", cueLimit)
	return s
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "时间未记录"
	}
	return t.Format("2006-01-02 15:04")
}

func cutRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// evidence 一个目标这几次的现场，最多三行——卡片要能一眼读完。
func (g *traceGroup) evidence() []string {
	runs := append([]types.GoalResult{}, g.failed...)
	runs = append(runs, g.partial...)
	sort.Slice(runs, func(i, j int) bool { return runs[i].FinishedAt.After(runs[j].FinishedAt) })
	var out []string
	for i, r := range runs {
		if i >= cueEvidMax {
			out = append(out, fmt.Sprintf("…另有 %d 次同类记录", len(runs)-cueEvidMax))
			break
		}
		status := "失败"
		if r.Status == types.GoalPartial {
			status = fmt.Sprintf("只做了一半（完成度 %d）", r.Score)
		}
		line := fmt.Sprintf("%s · %s", stamp(r.FinishedAt), status)
		if k := dominantKind(r.FailureBreakdown); k != "" {
			line += " · " + cueKindText(k)
		}
		if strings.TrimSpace(r.Error) != "" {
			line += "：" + cutRunes(r.Error, 60)
		}
		out = append(out, line)
	}
	if g.traceID != "" {
		out = append(out, "同一 trace_id："+g.traceID)
	}
	return out
}

func dominantKind(m map[types.ErrorKind]int) types.ErrorKind {
	var best types.ErrorKind
	bestN := 0
	for k, n := range m {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}
