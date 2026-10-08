package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/config"
	"gleam/pkg/types"
)

// ---------- 候补目标：三条线索浮不浮得出卡、处置记不记得住 ----------

// cueAgent 造一个只带数据目录的 Agent：算候补卡不需要模型、不需要 Notifier。
// 这本身就是这一层的验收点——**它只读历史**，所以能这样一行装配起来测。
func cueAgent(t *testing.T) *Agent {
	t.Helper()
	return &Agent{Cfg: &config.Config{DataDir: t.TempDir()}}
}

// archive 落一份任务归档。TraceID 由调用方给定，因为「同一个目标反复出现」正是它的全部意义。
func archive(t *testing.T, a *Agent, g *types.GoalResult) {
	t.Helper()
	if g.StartedAt.IsZero() {
		g.StartedAt = time.Now().Add(-time.Hour)
	}
	if g.FinishedAt.IsZero() {
		g.FinishedAt = g.StartedAt.Add(2 * time.Minute)
	}
	if err := SaveTaskResult(a.Cfg.DataDir, g); err != nil {
		t.Fatalf("归档失败：%v", err)
	}
}

func failedRun(taskID, goal, trace string, kinds map[types.ErrorKind]int) *types.GoalResult {
	return &types.GoalResult{
		TaskID: taskID, Goal: goal, Status: types.GoalFailed, TraceID: trace,
		Score: 20, Error: "上游返回 502，重试三次仍然失败", FailureBreakdown: kinds,
	}
}

// quietFailedRun 只留下状态，不带错误文本也不带归因：只想数「同一个目标失败几次」时用它的。
func quietFailedRun(taskID, goal, trace string) *types.GoalResult {
	return &types.GoalResult{TaskID: taskID, Goal: goal, Status: types.GoalFailed, TraceID: trace, Score: 10}
}

// TestCues_EverySignalProducesARow 每条登记的信号都必须**造得出卡**。
//
// 闸门 check-cue-owner.py 只能读源码，判不出「今天到底浮没浮出一张卡」——分支可能写在
// 那里却永远进不去（阈值算错、聚合漏填字段），而界面只会显示「目前没有候补目标」。
func TestCues_EverySignalProducesARow(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, failedRun("t1", "把报告转成表格", "traceFail", map[types.ErrorKind]int{types.ErrUpstream: 1}))
	archive(t, a, failedRun("t2", "把报告转成表格", "traceFail", map[types.ErrorKind]int{types.ErrUpstream: 1}))
	archive(t, a, &types.GoalResult{
		TaskID: "t3", Goal: "整理这一周的剪辑素材", Status: types.GoalPartial,
		TraceID: "traceHalf", Score: 55,
	})
	archive(t, a, &types.GoalResult{
		TaskID: "t4", Goal: "整理这一周的剪辑素材", Status: types.GoalPartial,
		TraceID: "traceHalf", Score: 60,
	})
	// 第三条线索要跨任务：同一个任务里连错三步只是这一次坏了，两个任务各错两次才是同一层在坏。
	archive(t, a, failedRun("t5", "另一个目标", "traceOther", map[types.ErrorKind]int{types.ErrTimeout: 2}))
	archive(t, a, failedRun("t6", "又一个目标", "traceOther2", map[types.ErrorKind]int{types.ErrTimeout: 2}))

	got := map[string]bool{}
	for _, row := range a.CueView().Rows {
		got[row.Signal] = true
	}
	for _, sig := range cueSignals {
		if !got[sig] {
			t.Errorf("信号 %q 登记了却浮不出卡（派生分支或阈值坏了）", sig)
		}
	}
}

// TestCues_RepeatedFailureWinsOverHalfDone 同一条 TraceID 上两种线索都够格时只出一张。
func TestCues_RepeatedFailureWinsOverHalfDone(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, failedRun("t1", "同一个目标", "traceX", nil))
	archive(t, a, failedRun("t2", "同一个目标", "traceX", nil))
	archive(t, a, &types.GoalResult{TaskID: "t3", Goal: "同一个目标", Status: types.GoalPartial, TraceID: "traceX", Score: 50})
	archive(t, a, &types.GoalResult{TaskID: "t4", Goal: "同一个目标", Status: types.GoalPartial, TraceID: "traceX", Score: 40})

	var sigs []string
	for _, row := range a.CueView().Rows {
		sigs = append(sigs, row.Signal)
	}
	if len(sigs) != 1 || sigs[0] != "repeat_failure" {
		t.Errorf("只该浮更严重的那张，实得 %v", sigs)
	}
}

// TestCues_HalfDoneSkipsTraceThatOnceWon 中间成功过一次就不提：这条路走得通。
func TestCues_HalfDoneSkipsTraceThatOnceWon(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, &types.GoalResult{TaskID: "t1", Goal: "写周报", Status: types.GoalSuccess, TraceID: "traceY", Score: 100})
	archive(t, a, &types.GoalResult{TaskID: "t2", Goal: "写周报", Status: types.GoalPartial, TraceID: "traceY", Score: 50})
	archive(t, a, &types.GoalResult{TaskID: "t3", Goal: "写周报", Status: types.GoalPartial, TraceID: "traceY", Score: 45})

	if rows := a.CueView().Rows; len(rows) != 0 {
		t.Errorf("成功过一次的目标不该提「每次只做一半」，实得 %+v", rows)
	}
}

// TestCues_ReadOnlyAndStable 算一次卡不许写盘，也不许两次算出两个样子。
//
// 这两条是「不调模型」那口径的可执行版本：同一段历史必须每次给出同一页，
// 否则用户照着做的决定是靠不住的。
func TestCues_ReadOnlyAndStable(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, failedRun("t1", "反复失败的目标", "traceZ", map[types.ErrorKind]int{types.ErrPermission: 1}))
	archive(t, a, failedRun("t2", "反复失败的目标", "traceZ", map[types.ErrorKind]int{types.ErrPermission: 1}))

	first, err := json.Marshal(a.CueView())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(a.CueView())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("同一段历史两次算出了不同的页：\n%s\n%s", first, second)
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "cues.json")); !os.IsNotExist(err) {
		t.Errorf("只读的一页算完却留下了 %s：卡片不该落盘", filepath.Join(a.Cfg.DataDir, "cues.json"))
	}
}

// TestCues_DismissSurvivesRestart 「别再提」是唯一要落盘的东西，它必须跨视图、跨进程生效。
func TestCues_DismissSurvivesRestart(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, failedRun("t1", "别再提我已知晓", "traceD", nil))
	archive(t, a, failedRun("t2", "别再提我已知晓", "traceD", nil))

	rows := a.CueView().Rows
	if len(rows) == 0 {
		t.Fatal("前置条件不成立：本该浮出一张卡")
	}
	id := rows[0].ID
	if err := a.CueDismiss(id); err != nil {
		t.Fatalf("记处置失败：%v", err)
	}
	// 换一个 Agent 实例（同数据目录）＝重启
	b := &Agent{Cfg: &config.Config{DataDir: a.Cfg.DataDir}}
	if got := b.CueView(); len(got.Rows) != 0 {
		t.Errorf("记了别再提之后仍浮出 %d 张卡", len(got.Rows))
	}
	if len(b.CueView().Suppressed) != 1 {
		t.Errorf("处置记录没进 suppressed，撤销入口就找不到它：%+v", b.CueView().Suppressed)
	}
	if err := b.CueUnsuppress(id); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if len(b.CueView().Rows) == 0 {
		t.Error("撤销「别再提」之后那张卡应该回来")
	}
}

// TestCues_AdoptRecordsStateWithoutRunningAnything 采纳只是把话交给用户，**不是执行**。
func TestCues_AdoptRecordsStateWithoutRunningAnything(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, failedRun("t1", "采纳但不该跑起来", "traceA", nil))
	archive(t, a, failedRun("t2", "采纳但不该跑起来", "traceA", nil))
	id := a.CueView().Rows[0].ID

	before := tree(t, a.Cfg.DataDir)
	if err := a.CueAdopt(id); err != nil {
		t.Fatalf("采纳失败：%v", err)
	}
	after := tree(t, a.Cfg.DataDir)
	if diff := fileSetDiff(before, after); len(diff) != 1 || diff[0] != "cues.json" {
		t.Errorf("采纳之后盘上多出的不该只有处置记录，实得 %v", diff)
	}
	var found bool
	for _, s := range a.CueView().Suppressed {
		if s.Fingerprint == id && s.Reason == "adopted" {
			found = true
		}
	}
	if !found {
		t.Errorf("处置记录没标成 adopted：%+v", a.CueView().Suppressed)
	}
}

// TestCues_RespectsLimit 配额要说得出「还有几条没挤进来」，不许静默截断。
func TestCues_RespectsLimit(t *testing.T) {
	a := cueAgent(t)
	for i := 0; i < 5; i++ {
		trace := fmt.Sprintf("traceLimit%d", i)
		goal := fmt.Sprintf("反复失败的目标 %d", i)
		archive(t, a, quietFailedRun(fmt.Sprintf("a%d", i), goal, trace))
		archive(t, a, quietFailedRun(fmt.Sprintf("b%d", i), goal, trace))
	}
	led := a.CueView()
	if len(led.Rows) != cueLimit {
		t.Errorf("最多 %d 张，实得 %d", cueLimit, len(led.Rows))
	}
	if led.Overflow != 2 {
		t.Errorf("没挤进来的条数应报 2，实得 %d", led.Overflow)
	}
	// 截断这句话由后端写：界面只印字段。两处各写一版就会漂成"数字对、说法不承认截断"。
	if !strings.Contains(led.Truncated, "2 条") {
		t.Errorf("截断要交代没挤进来的条数，实得 %q", led.Truncated)
	}
	if !strings.Contains(led.Coverage, fmt.Sprintf("最多列 %d 张卡", cueLimit)) {
		t.Errorf("覆盖范围该交代配额：%s", led.Coverage)
	}
}

// TestCues_CoverageIsHonestAboutTrim 空历史要说成「读了 0 条」，不能糊成「一切正常」。
func TestCues_CoverageIsHonestAboutTrim(t *testing.T) {
	a := cueAgent(t)
	led := a.CueView()
	if led.Scanned != 0 || len(led.Rows) != 0 {
		t.Fatalf("前置条件：空目录应算出空页（scanned=%d rows=%d）", led.Scanned, len(led.Rows))
	}
	for _, want := range []string{"读了 0 条", "裁剪"} {
		if !strings.Contains(led.Coverage, want) {
			t.Errorf("覆盖范围缺 %q：%s", want, led.Coverage)
		}
	}
}

// TestCues_UnreadableArchiveSaysSo 归档目录坏了不是「没有线索」，两者在界面上必须分得开。
func TestCues_UnreadableArchiveSaysSo(t *testing.T) {
	a := cueAgent(t)
	// tasks 位置放一个同名文件，让 ReadDir 之后按目录读的路径失败。
	p := filepath.Join(a.Cfg.DataDir, "tasks")
	if err := os.WriteFile(p, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	led := a.CueView()
	if led.Note == "" && led.Coverage == "" {
		t.Error("归档读不动时至少要留下一句可看见的说明")
	}
}

// TestCues_KindTextCoversEveryEnum 失败归因不许裸奔成英文（M3 那条口径在这层同样生效）。
func TestCues_KindTextCoversEveryEnum(t *testing.T) {
	for _, k := range []types.ErrorKind{
		types.ErrParam, types.ErrFormat, types.ErrBusiness, types.ErrPermission,
		types.ErrNotFound, types.ErrTimeout, types.ErrUpstream, types.ErrInternal, types.ErrUnknown,
	} {
		if got := cueKindText(k); got == string(k) || got == "" {
			t.Errorf("失败类别 %q 没有人话映射：%q", k, got)
		}
	}
}

// TestCues_LabelsCoverEveryEnumValue 信号与处置原因都不许裸奔成英文。
//
// 为什么连「已按下」列表一起测：那张列表里唯一的取值来源就是 signal 与 reason，
// 界面只印 signal_text / reason_text。少一条映射，那一行就写成 dismissed / tool_failure，
// 而它是用户找回被自己按掉的卡片的唯一路径——M3 那条老坑在这层同样成立。
func TestCues_LabelsCoverEveryEnumValue(t *testing.T) {
	for _, sig := range cueSignals {
		if got := cueSignalText(sig); got == sig || got == "" {
			t.Errorf("线索 %q 没有人话映射：%q", sig, got)
		}
	}
	for _, r := range []string{cueDismissed, cueAdopted} {
		if got := cueReasonText(r); got == r || got == "" {
			t.Errorf("处置原因 %q 没有人话映射：%q", r, got)
		}
	}

	a := cueAgent(t)
	archive(t, a, failedRun("t1", "标签要跟着记录走", "traceL", nil))
	archive(t, a, failedRun("t2", "标签要跟着记录走", "traceL", nil))
	if err := a.CueDismiss(a.CueView().Rows[0].ID); err != nil {
		t.Fatalf("记处置失败：%v", err)
	}
	sup := a.CueView().Suppressed
	if len(sup) != 1 {
		t.Fatalf("应有一条处置记录，实得 %d", len(sup))
	}
	if sup[0].SignalText == "" || sup[0].SignalText == sup[0].Signal {
		t.Errorf("处置记录没带线索的人话：%+v", sup[0])
	}
	if sup[0].ReasonText != "别再提" {
		t.Errorf("处置记录的原因应印成「别再提」，实得 %q", sup[0].ReasonText)
	}
}

// TestCues_AuthoredCopyHasNoMarkdown 卡片走 textContent，星号会原样上界面。
//
// 只判我们自己写的那几句（SignalText / Why）：目标原文是用户打出来的，
// 他打了星号就该看见星号——那不是我加了 markdown。
func TestCues_AuthoredCopyHasNoMarkdown(t *testing.T) {
	a := cueAgent(t)
	archive(t, a, failedRun("t1", "带**强调**的目标", "traceM", map[types.ErrorKind]int{types.ErrTimeout: 3}))
	archive(t, a, failedRun("t2", "带**强调**的目标", "traceM", map[types.ErrorKind]int{types.ErrTimeout: 3}))
	archive(t, a, failedRun("t3", "另一个**加粗**目标", "traceM2", map[types.ErrorKind]int{types.ErrTimeout: 3}))
	archive(t, a, failedRun("t4", "另一个**加粗**目标", "traceM2", map[types.ErrorKind]int{types.ErrTimeout: 3}))

	led := a.CueView()
	if len(led.Rows) == 0 {
		t.Fatal("前置条件：本该浮出卡")
	}
	checked := []string{led.Coverage, led.Note}
	for _, row := range led.Rows {
		checked = append(checked, row.SignalText, row.Why)
	}
	for _, s := range checked {
		if strings.Contains(s, "**") {
			t.Errorf("候补目标页的自作文案里出现了 markdown 强调：%q", s)
		}
	}
}

// ---------- helpers ----------

func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil // 读不动的位置不参与对照，它们不是这次动作产生的
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func fileSetDiff(before, after []string) []string {
	in := map[string]bool{}
	for _, p := range before {
		in[p] = true
	}
	var out []string
	for _, p := range after {
		if !in[p] {
			out = append(out, p)
		}
	}
	return out
}
