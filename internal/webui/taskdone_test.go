package webui

import (
	"testing"
	"time"

	"gleam/pkg/types"
)

// doneEvent 造一个后台任务完成事件。
func doneEvent(taskID string, status types.GoalStatus) types.TaskDoneEvent {
	return types.TaskDoneEvent{
		TaskID: taskID,
		Origin: "定时任务「每日巡检」",
		Result: types.GoalResult{
			TaskID:     taskID,
			Goal:       "检查磁盘占用",
			Status:     status,
			Score:      0,
			Error:      "磁盘 /data 已用 96%",
			StartedAt:  time.Now().Add(-2 * time.Minute),
			FinishedAt: time.Now(),
		},
	}
}

// TestWebUI_OnTaskDone_RegistersUnknownTask 定时任务的结果必须能在界面上查到。
//
// 定时任务不是从 `/api/goals` 提交的，webui 的任务表里从来没有它。所以
// "落进任务事件流"这段代码如果只写 `if t, ok := s.tasks[id]; ok`，
// 在定时任务场景下**一次都不会执行**——看起来做了持久化，实际只剩广播。
// 而用户几小时后打开窗口时 SSE 早就断了，"当时不在场就永远不知道"
// 恰好是定时任务的常态。
//
// 本用例不预先造任务，直接推事件，然后走真实的读接口：
// 读不到 = 结果没送达。
func TestWebUI_OnTaskDone_RegistersUnknownTask(t *testing.T) {
	f := newFixture(t, nil)

	f.srv.OnTaskDone(doneEvent("sched-t1", types.GoalFailed))

	// 1) 任务详情能查到，且带着完成事件。
	detail := f.call("GET", "/api/goals/sched-t1", nil)
	if detail["task_id"] != "sched-t1" {
		t.Fatalf("任务详情里 task_id = %v", detail["task_id"])
	}
	if detail["goal"] != "检查磁盘占用" {
		t.Errorf("详情应带上目标，实际 %v", detail["goal"])
	}
	// 状态要如实反映失败——把失败登记成成功比不登记更坏。
	if detail["status"] != string(types.GoalFailed) {
		t.Errorf("详情状态应为 failed，实际 %v", detail["status"])
	}
	events, _ := detail["events"].([]any)
	found := false
	for _, e := range events {
		ev := e.(map[string]any)
		if ev["type"] != "task_done" {
			continue
		}
		found = true
		// 事件体在 data 里（sseEvent 的外层只有 type/data）。
		body, _ := ev["data"].(map[string]any)
		if body == nil {
			t.Fatalf("task_done 事件缺 data 体：%v", ev)
		}
		// 读的人不在现场：不知道是哪个任务、什么状态，这条事件就只是噪音。
		if body["origin"] != "定时任务「每日巡检」" {
			t.Errorf("事件应带来源，实际 %v", body["origin"])
		}
		if s, _ := body["line"].(string); s == "" {
			t.Error("事件应带一行摘要")
		}
		if s, _ := body["title"].(string); s == "" {
			t.Error("事件应带标题")
		}
	}
	if !found {
		t.Errorf("详情里应能看到 task_done 事件，实际 %v", events)
	}

	// 2) 任务列表里也要出现——用户是从列表进入详情的。
	list := f.call("GET", "/api/goals", nil)
	inList := false
	for _, it := range list["goals"].([]any) {
		if it.(map[string]any)["task_id"] == "sched-t1" {
			inList = true
		}
	}
	if !inList {
		t.Error("任务列表里应能看到定时任务的结果")
	}
}

// TestWebUI_OnTaskDone_KeepsExistingTaskState 已登记的任务不能被后台事件改写状态。
//
// 一个正在跑的任务收到 `task_done`（比如同一 taskID 的旧事件迟到），
// 不能把它的状态从 running 改成别的——那会让界面上的进度条突然跳到终点。
func TestWebUI_OnTaskDone_KeepsExistingTaskState(t *testing.T) {
	f := newFixture(t, nil)

	// 造一个"正在跑"的任务（走真实提交路径，拿到真实登记）。
	f.srv.mu.Lock()
	f.srv.tasks["live-1"] = &taskInfo{
		ID: "live-1", Goal: "用户提交的目标", Status: types.GoalRunning, Started: time.Now(),
	}
	f.srv.mu.Unlock()

	f.srv.OnTaskDone(doneEvent("live-1", types.GoalFailed))

	detail := f.call("GET", "/api/goals/live-1", nil)
	if detail["goal"] != "用户提交的目标" {
		t.Errorf("不该被后台事件改写目标，实际 %v", detail["goal"])
	}
	if detail["status"] != string(types.GoalRunning) {
		t.Errorf("不该被后台事件改写状态，实际 %v", detail["status"])
	}
	// 但事件本身要落进去——否则"结果送达"又断了。
	found := false
	for _, e := range detail["events"].([]any) {
		if e.(map[string]any)["type"] == "task_done" {
			found = true
		}
	}
	if !found {
		t.Error("已有任务也应收到 task_done 事件")
	}
}

// TestWebUI_OnTaskDone_MissingStartTimeStillListed 开始时间为零时也要能列出来。
//
// 有些结果路径（启动期失败、外部注入）可能没填 StartedAt。若直接拿零值当 Started，
// 任务会排到列表最末（甚至被裁剪逻辑当成"最旧"先删掉），用户就看不到它。
func TestWebUI_OnTaskDone_MissingStartTimeStillListed(t *testing.T) {
	f := newFixture(t, nil)

	ev := doneEvent("sched-zero", types.GoalFailed)
	ev.Result.StartedAt = time.Time{}
	f.srv.OnTaskDone(ev)

	detail := f.call("GET", "/api/goals/sched-zero", nil)
	if s, _ := detail["started_at"].(string); s == "" {
		t.Error("开始时间为零时应补一个可读时间，而不是留空")
	}
}
