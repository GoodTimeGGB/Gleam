package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/config"
	"gleam/internal/harness/scheduler"
	"gleam/pkg/types"
)

// ---------- 测试替身 ----------

// fakeNotifier 只关心 OnTaskDone——本文件测的就是"送达"。
//
// 用一个**真的实现了接口**的替身而不是 `agent.NopNotifier{}`：
// 空实现会让"线没接"和"线接上了但没人听"长得一模一样，测试就永远绿。
type fakeNotifier struct {
	mu     sync.Mutex
	events []types.TaskDoneEvent
}

func (f *fakeNotifier) OnProgress(types.ProgressEvent) {}
func (f *fakeNotifier) OnApproval(types.ApprovalRequest) types.ApprovalResponse {
	return types.ApprovalResponse{}
}
func (f *fakeNotifier) OnSuggestion(string, string)             {}
func (f *fakeNotifier) OnSuggestSkill(string, types.SkillDraft) {}

func (f *fakeNotifier) OnTaskDone(ev types.TaskDoneEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// captureStderr 把 os.Stderr 换成管道，跑完 fn 后返回它写出的内容。
//
// 为什么要看 stderr：静默分支也要**留下痕迹**。"按策略静默"与"根本没跑"
// 在日志上必须可区分，否则排查时只能猜。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("建管道失败：%v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	func() {
		defer func() { os.Stderr = old }()
		fn()
	}()
	_ = w.Close()
	return <-done
}

// withSysNotify 替换系统通知实现，返回"发出去的通知"收集器。
//
// 必须替换：不替换就会在跑测试的开发机上真的弹窗（CI 上则是静默失败），
// 而且弹没弹窗无法断言——"断言不了"最后会变成"没人验证过"。
func withSysNotify(t *testing.T) *[]string {
	t.Helper()
	var sent []string
	old := sysNotify
	sysNotify = func(title, message string) error {
		sent = append(sent, title+" — "+message)
		return nil
	}
	t.Cleanup(func() { sysNotify = old })
	return &sent
}

// job 造一个定时任务。
func job(name, policy string) scheduler.Job {
	return scheduler.Job{Name: name, Goal: "检查磁盘占用", Notify: policy, Enabled: true}
}

// stubRunGoal 替换 fire 路径的目标执行器，返回恢复函数。
//
// 为什么要替换：本文件要验的是"通知有没有接上"，不是"规划器好不好"。
// 真跑 RunGoal 会把两个问题搅在一起，而且模型一不稳测试就红——
// 那会让人把"接线断了"读成"模型抽风"，然后去查错的地方。
func stubRunGoal(fn func(*agent.Agent, context.Context, types.GoalRequest) *types.GoalResult) func() {
	old := runGoalFor
	runGoalFor = fn
	return func() { runGoalFor = old }
}

// result 造一份指定状态的任务结果。
func result(status types.GoalStatus) *types.GoalResult {
	return &types.GoalResult{
		TaskID: "t-1",
		Goal:   "检查磁盘占用",
		Status: status,
		Score:  90,
		Error:  "磁盘 /data 已用 96%",
	}
}

// ---------- 接线断言 ----------

// TestNotifyScheduledDone_Wiring 本文件的核心：判据对 ≠ 线接上了。
//
// 单测 `Job.ShouldNotify` 只能证明"什么时候该打扰用户"这条规则写对了，
// 证明不了 `notifyScheduledDone` 真的调了它、真的发了通知、真的推了事件。
// 本仓库栽过四次的恰好是这一类（判据对、线没接），所以这里断言**两条出口**：
// 系统通知 + 事件推送。
//
// 两条出口的开关**不是同一个**：策略只管系统通知（那次要不要弹窗），事件推送
// 每次都发生。因为"这条任务跑完了"是已经发生的事实，把它一并静默掉，
// `notify: never` 的任务就什么都留不下——用户只好一直盯着它跑。
func TestNotifyScheduledDone_Wiring(t *testing.T) {
	cases := []struct {
		name        string
		policy      string
		status      types.GoalStatus
		wantSys     bool // 系统通知该不该发
		wantSilence bool // stderr 该不该出现"不弹系统通知"
	}{
		// 先正：默认策略下失败**必须**响——这是整个功能的立身之本。
		{"默认策略·失败 → 弹通知", scheduler.NotifyOnFailure, types.GoalFailed, true, false},
		{"默认策略·部分完成 → 弹通知", scheduler.NotifyOnFailure, types.GoalPartial, true, false},
		{"默认策略·已取消 → 弹通知", scheduler.NotifyOnFailure, types.GoalCancelled, true, false},
		{"always·成功 → 弹通知", scheduler.NotifyAlways, types.GoalSuccess, true, false},
		{"always·失败 → 弹通知", scheduler.NotifyAlways, types.GoalFailed, true, false},
		{"空策略等价默认·失败 → 弹通知", "", types.GoalFailed, true, false},
		// 后反：这些是"看起来可以省一次打扰"、实则省错了的场合。
		{"默认策略·成功 → 不弹，但仍送达", scheduler.NotifyOnFailure, types.GoalSuccess, false, true},
		{"never·失败 → 不弹，但仍送达", scheduler.NotifyNever, types.GoalFailed, false, true},
		{"脏策略值·失败 → 按默认走，仍弹", "yelling", types.GoalFailed, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sent := withSysNotify(t)
			sink := &fakeNotifier{}
			var out string
			out = captureStderr(t, func() {
				notifyScheduledDone(job("每日巡检", c.policy), result(c.status), sink)
			})

			if got := len(*sent) > 0; got != c.wantSys {
				t.Errorf("系统通知：发了=%v，期望=%v（stderr=%q）", got, c.wantSys, out)
			}
			// 送达与策略无关：每个用例都必须推一次事件。
			if sink.count() != 1 {
				t.Errorf("事件推送：期望每次都推 1 次，实际 %d 次（stderr=%q）", sink.count(), out)
			}
			if got := strings.Contains(out, "不弹系统通知"); got != c.wantSilence {
				t.Errorf("静默痕迹：出现=%v，期望=%v（stderr=%q）", got, c.wantSilence, out)
			}
			// 静默不等于不留痕：否则"没响"与"没跑"在日志上无法区分。
			if c.wantSilence && !strings.Contains(out, "检查磁盘占用") {
				t.Errorf("静默时也该说清是哪个任务，stderr=%q", out)
			}
		})
	}
}

// TestNotifyScheduledDone_NilResultIsNoop 结果是 nil 时不能崩。
//
// 这条不是形式主义：RunGoal 在极端路径（比如启动期就失败）下可能返回 nil，
// 而 fire 是在 goroutine 里跑的——崩在那里等于调度器静默死掉，且**没有任何通知**。
func TestNotifyScheduledDone_NilResultIsNoop(t *testing.T) {
	sent := withSysNotify(t)
	sink := &fakeNotifier{}
	out := captureStderr(t, func() {
		notifyScheduledDone(job("每日巡检", scheduler.NotifyAlways), nil, sink)
	})
	if len(*sent) != 0 || sink.count() != 0 {
		t.Errorf("nil 结果不该产生任何通知：系统=%d 事件=%d", len(*sent), sink.count())
	}
	if out != "" {
		t.Errorf("nil 结果不该有输出，实际 %q", out)
	}
}

// TestNotifyScheduledDone_NilSinkStillNotifies 事件出口为 nil 时，系统通知仍要发。
//
// 装配顺序决定了 notifier 可能还没换上（`app` 模式里 bindSchedulerFire 先跑）。
// 那时如果因为 sink 为 nil 就整条 return，用户会**连系统通知都收不到**——
// 一个失败被静默掉，而且是被"防御性写法"静默掉的。
func TestNotifyScheduledDone_NilSinkStillNotifies(t *testing.T) {
	sent := withSysNotify(t)
	out := captureStderr(t, func() {
		notifyScheduledDone(job("每日巡检", scheduler.NotifyOnFailure), result(types.GoalFailed), nil)
	})
	if len(*sent) != 1 {
		t.Fatalf("sink 为 nil 时系统通知仍应发一次，实际 %d 次（stderr=%q）", len(*sent), out)
	}
}

// TestNotifyScheduledDone_SysNotifyFailureKeepsEvent 弹窗失败不能吞掉事实。
//
// 系统通知是"尽力而为"（无头环境、权限被拒、通知中心被关都可能失败），
// 但**任务跑完了**是已经发生的事实。因为弹窗失败就不推事件，等于让
// "没收到通知"和"任务没跑"变得一样——而后者恰恰是最需要知道的。
func TestNotifyScheduledDone_SysNotifyFailureKeepsEvent(t *testing.T) {
	old := sysNotify
	sysNotify = func(string, string) error { return io.ErrClosedPipe }
	t.Cleanup(func() { sysNotify = old })

	sink := &fakeNotifier{}
	out := captureStderr(t, func() {
		notifyScheduledDone(job("每日巡检", scheduler.NotifyOnFailure), result(types.GoalFailed), sink)
	})
	if sink.count() != 1 {
		t.Errorf("弹窗失败时事件仍应推一次，实际 %d 次", sink.count())
	}
	if !strings.Contains(out, "系统通知未送达") {
		t.Errorf("弹窗失败必须在 stderr 留痕，实际 %q", out)
	}
}

// TestNotifyScheduledDone_EventCarriesOriginAndState 事件要能独立读懂。
//
// 事件会被推到宿主（事件流 / JSON-RPC），读它的人不在现场：
// 不知道是哪个任务、什么状态，这条事件就只是噪音。
func TestNotifyScheduledDone_EventCarriesOriginAndState(t *testing.T) {
	withSysNotify(t)
	sink := &fakeNotifier{}
	captureStderr(t, func() {
		notifyScheduledDone(job("磁盘巡检", scheduler.NotifyAlways), result(types.GoalPartial), sink)
	})
	if sink.count() != 1 {
		t.Fatalf("应推一次事件，实际 %d", sink.count())
	}
	ev := sink.events[0]
	if !strings.Contains(ev.Origin, "磁盘巡检") {
		t.Errorf("事件应带任务名，Origin=%q", ev.Origin)
	}
	if ev.TaskID != "t-1" {
		t.Errorf("事件应带 TaskID，实际 %q", ev.TaskID)
	}
	if ev.StateText() != "部分完成" {
		t.Errorf("状态词应为「部分完成」，实际 %q", ev.StateText())
	}
	if ev.Succeeded() {
		t.Error("部分完成不算完整成功——它正是最该通知的那一类")
	}
	// 标题与正文都要落到"哪个任务 + 什么状态"上，别让用户看"后台任务完成"。
	if !strings.Contains(ev.Title(), "磁盘巡检") || !strings.Contains(ev.Title(), "部分完成") {
		t.Errorf("标题应含任务名与状态，实际 %q", ev.Title())
	}
	if !strings.Contains(ev.Line(), "磁盘巡检") && !strings.Contains(ev.Line(), "检查磁盘占用") {
		t.Errorf("正文应点出是哪个目标，实际 %q", ev.Line())
	}
}

// TestSysNotify_DisabledStillPrints 关掉系统通知时要打印而不是静默丢弃。
//
// 自动化环境（CI / 冒烟）必须能关掉真弹窗，但"关掉"不等于"看不见"：
// 静默丢弃会让冒烟里"通知发了没有"无从断言，而断言不了最后会变成没人验证。
func TestSysNotify_DisabledStillPrints(t *testing.T) {
	t.Setenv("GLEAM_NO_SYS_NOTIFY", "1")
	// 用真实的 sysNotify（不替换），验证它自己的禁用分支。
	out := captureStderr(t, func() {
		if err := sysNotify("定时任务「每日巡检」失败", "失败：检查磁盘占用"); err != nil {
			t.Errorf("禁用状态下不该报错，实际 %v", err)
		}
	})
	for _, want := range []string{"每日巡检", "检查磁盘占用"} {
		if !strings.Contains(out, want) {
			t.Errorf("禁用时也应打印待发内容（含 %q），实际 %q", want, out)
		}
	}
}

// TestFireScheduledJob_Notifies 绑定之后跑一次任务，通知要真的出去。
//
// 这一层是上一条测试的补充：上一条直接调 `notifyScheduledDone`，只能证明
// **通知函数本身**对；这一条从 `fireScheduledJob` 进（也就是 fire 闭包真正调的那个
// 函数），证明"跑完 → 送达"这条线是连着的。
//
// 顺带钉住**归档**：定时任务不像交互任务那样有人在屏幕前，跑完只剩 `tasks/` 里那一份
// 终态快照。少了它，`notify: never` 的任务等于从没跑过（重启后界面与 `gleam replay` 都读不到）。
func TestFireScheduledJob_Notifies(t *testing.T) {
	withSysNotify(t)
	restore := stubRunGoal(func(*agent.Agent, context.Context, types.GoalRequest) *types.GoalResult {
		return result(types.GoalFailed)
	})
	defer restore()

	sink := &fakeNotifier{}
	dataDir := t.TempDir()
	rt := &runtime{agent: &agent.Agent{Notifier: sink}, cfg: &config.Config{DataDir: dataDir}}
	captureStderr(t, func() { fireScheduledJob(rt, job("每日巡检", scheduler.NotifyOnFailure)) })

	if sink.count() != 1 {
		t.Fatalf("fire 路径应送达一次事件，实际 %d", sink.count())
	}
	if !strings.Contains(sink.events[0].Origin, "每日巡检") {
		t.Errorf("事件应带任务名，实际 %q", sink.events[0].Origin)
	}
	archived, err := agent.ReadTaskResult(dataDir, "t-1")
	if err != nil || archived == nil {
		t.Fatalf("fire 路径应把终态归档到 tasks/：读到=%v err=%v", archived, err)
	}
	if archived.Status != types.GoalFailed {
		t.Errorf("归档应保留失败状态（静默的不该被写成成功），实际 %q", archived.Status)
	}
}

// TestFireScheduledJob_ArchiveIgnoresNotifyPolicy 落盘归落盘，打扰归打扰。
//
// 上面那条 `TestFireScheduledJob_Notifies` 用的是「失败 + on_failure」——那次**本来就要弹窗**，
// 所以谁把归档挪进 `ShouldNotify` 分支，它照样绿。这一条专挑**不弹窗**的三种组合
// （never、以及 on_failure 配成功），断言盘上那一份还在、事件还是推了一次。
//
// 为什么值得单独一条：`notify: never` 的任务如果连档案都不留，它就等于从没跑过——
// 界面「最近任务」读的是归档（§4.6.21），`gleam replay` 读的也是归档。
// 用户选「从不通知」说的是"别打扰我"，不是"别记下来"。
func TestFireScheduledJob_ArchiveIgnoresNotifyPolicy(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		status types.GoalStatus
	}{
		{"never·成功", scheduler.NotifyNever, types.GoalSuccess},
		{"never·失败", scheduler.NotifyNever, types.GoalFailed},
		{"on_failure·成功（按策略不弹）", scheduler.NotifyOnFailure, types.GoalSuccess},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sent := withSysNotify(t)
			restore := stubRunGoal(func(*agent.Agent, context.Context, types.GoalRequest) *types.GoalResult {
				return result(c.status)
			})
			defer restore()

			sink := &fakeNotifier{}
			dataDir := t.TempDir()
			rt := &runtime{agent: &agent.Agent{Notifier: sink}, cfg: &config.Config{DataDir: dataDir}}
			out := captureStderr(t, func() { fireScheduledJob(rt, job("每日巡检", c.policy)) })

			// 前提要先钉住：这三个用例确实都是"不该弹窗"的那条路，
			// 否则归档断言会被弹窗路径顺带掩盖（测的就不是策略静默下的行为了）。
			if len(*sent) != 0 {
				t.Errorf("按策略不该弹窗，实际弹了 %d 次（stderr=%q）", len(*sent), out)
			}
			archived, err := agent.ReadTaskResult(dataDir, "t-1")
			if err != nil || archived == nil {
				t.Fatalf("策略 %q 配 %q 仍要归档到 tasks/：读到=%v err=%v", c.policy, c.status, archived, err)
			}
			if archived.Status != c.status {
				t.Errorf("归档要保留真实终态，期望 %q 实际 %q", c.status, archived.Status)
			}
			if sink.count() != 1 {
				t.Errorf("事件仍应推一次（送达与策略无关），实际 %d 次", sink.count())
			}
		})
	}
}

// TestBindSchedulerFire_ClosureIsConnected 闭包要真的接到 fireScheduledJob 上。
//
// 本仓库栽过四次的是同一类：判据对、线没接。所以这里**不信任**"闭包里写了调用"
// 这件事，而是走调度器自己的触发路径（TriggerNow → go fire），断言通知真的到了。
func TestBindSchedulerFire_ClosureIsConnected(t *testing.T) {
	withSysNotify(t)
	restore := stubRunGoal(func(*agent.Agent, context.Context, types.GoalRequest) *types.GoalResult {
		return result(types.GoalFailed)
	})
	defer restore()

	sched, err := scheduler.Open(filepath.Join(t.TempDir(), "sched.json"), nil)
	if err != nil {
		t.Fatalf("建调度器失败：%v", err)
	}
	if _, err := sched.AddJob("每日巡检", "", 3600, "检查磁盘占用", "auto"); err != nil {
		t.Fatalf("加任务失败：%v", err)
	}

	sink := &fakeNotifier{}
	rt := &runtime{agent: &agent.Agent{Notifier: sink}, sched: sched, cfg: &config.Config{DataDir: t.TempDir()}}
	bindSchedulerFire(rt)

	// 绑定**之后**才换宿主——这同时钉住了"惰性读"：
	// 若闭包在绑定时就把 notifier 捕获了，这里换再多次也送不到。
	sink2 := &fakeNotifier{}
	rt.agent.Notifier = sink2

	captureStderr(t, func() {
		ok, err := sched.TriggerNow("每日巡检")
		if err != nil || !ok {
			// 这里用 Errorf 而不是 Fatalf：Fatalf 会 Goexit，
			// 绕过 captureStderr 的收尾（关管道、读输出），把测试挂住。
			t.Errorf("触发失败：ok=%v err=%v", ok, err)
			return
		}
		// TriggerNow 内部是 `go fire(job)`，等它跑完。
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && sink2.count() == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	})

	if sink2.count() != 1 {
		t.Fatalf("绑定时未捕获 notifier 才算对：绑定后换上的宿主应收到 1 次，实际 %d", sink2.count())
	}
	if sink.count() != 0 {
		t.Errorf("绑定期那个旧宿主不该收到事件（说明 notifier 被提前捕获了），实际 %d", sink.count())
	}
}

// TestBindSchedulerFire_NilSchedulerIsNoop 没有调度器时不能崩。
func TestBindSchedulerFire_NilSchedulerIsNoop(t *testing.T) {
	rt := &runtime{agent: &agent.Agent{Notifier: agent.NopNotifier{}}}
	bindSchedulerFire(rt) // 不该 panic
}
