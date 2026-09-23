package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/scheduler"
	"gleam/pkg/types"
)

// TestRPC_ScheduleNotify_CreateAndSet 通知策略要能建、能改、能回读。
//
// JSON-RPC 是编辑器插件与外部宿主的入口。这里的策略字段如果没接上，
// 表现是"界面上改了、实际没生效"——比没有这个功能更坏，因为用户以为设好了。
func TestRPC_ScheduleNotify_CreateAndSet(t *testing.T) {
	c, _, _ := startServer(t, nil, true)

	// 创建时带上策略。
	res, rpcErr, err := c.call("schedule/create", map[string]any{
		"name": "rpc-job", "goal": "检查磁盘占用", "cron": "0 9 * * *", "notify": "always",
	})
	if err != nil || rpcErr != nil {
		t.Fatalf("创建失败：err=%v rpcErr=%v", err, rpcErr)
	}
	if got := fieldOf(t, res, "notify"); got != "always" {
		t.Errorf("创建响应里策略应为 always，实际 %v", got)
	}

	// 改成 never。
	res, rpcErr, err = c.call("schedule/notify", map[string]any{"name": "rpc-job", "notify": "never"})
	if err != nil || rpcErr != nil {
		t.Fatalf("改策略失败：err=%v rpcErr=%v", err, rpcErr)
	}
	if got := fieldOf(t, res, "notify"); got != "never" {
		t.Errorf("改后应为 never，实际 %v", got)
	}

	// 空串恢复默认。
	res, rpcErr, err = c.call("schedule/notify", map[string]any{"name": "rpc-job", "notify": ""})
	if err != nil || rpcErr != nil {
		t.Fatalf("恢复默认失败：err=%v rpcErr=%v", err, rpcErr)
	}
	if got := fieldOf(t, res, "notify"); got != scheduler.NotifyOnFailure {
		t.Errorf("空串应恢复默认 on_failure，实际 %v", got)
	}
}

// TestRPC_ScheduleNotify_InvalidRejected 非法策略要被拒，且不留半成品。
//
// 两种失败必须都验：**拒绝了**（用户得到反馈）和**没建出来**（盘上干净）。
// 只验前者的话，"先创建后校验"的实现照样能过——而它留下的正是最难排查的状态：
// 任务看起来一切正常，只是跑完不通知。
func TestRPC_ScheduleNotify_InvalidRejected(t *testing.T) {
	c, a, _ := startServer(t, nil, true)

	_, rpcErr, err := c.call("schedule/create", map[string]any{
		"name": "rpc-bad", "goal": "检查磁盘占用", "cron": "0 9 * * *", "notify": "yelling",
	})
	if err != nil {
		t.Fatalf("调用出错：%v", err)
	}
	if rpcErr == nil {
		t.Fatal("非法策略应返回错误")
	}
	for _, want := range []string{"always", "on_failure", "never"} {
		if !strings.Contains(rpcErr.Message, want) {
			t.Errorf("报错应列出可用值（缺 %q）：%s", want, rpcErr.Message)
		}
	}

	// 半成品检查：直接问调度器，而不是问列表接口——列表接口可能自己过滤掉了。
	for _, j := range a.Sched.ListJobs() {
		if j.Name == "rpc-bad" {
			t.Error("非法策略被拒后，不该留下同名任务")
		}
	}
}

// TestRPC_ScheduleNotify_MissingJobRejected 改不存在的任务要报错。
func TestRPC_ScheduleNotify_MissingJobRejected(t *testing.T) {
	c, _, _ := startServer(t, nil, true)
	_, rpcErr, err := c.call("schedule/notify", map[string]any{"name": "ghost", "notify": "never"})
	if err != nil {
		t.Fatalf("调用出错：%v", err)
	}
	if rpcErr == nil {
		t.Fatal("不存在的任务应返回错误，不能静默成功")
	}
	if !strings.Contains(rpcErr.Message, "ghost") {
		t.Errorf("报错应点出是哪个任务：%s", rpcErr.Message)
	}
}

// TestRPC_OnTaskDonePushesNotification 宿主的事件出口要真的推出去。
//
// `Service.OnTaskDone` 只有一行——正因为它只有一行，才最容易被漏接线：
// 判据（ShouldNotify）测过了、通知函数测过了，但"推给对端"这一步没人验。
// 定时任务的价值就是不用盯着；事件推不出去，用户就必须盯着。
func TestRPC_OnTaskDonePushesNotification(t *testing.T) {
	c, a, _ := startServer(t, nil, true)

	ev := types.TaskDoneEvent{
		TaskID: "t-done",
		Origin: "定时任务「每日巡检」",
		Result: types.GoalResult{
			TaskID: "t-done", Goal: "检查磁盘占用",
			Status: types.GoalFailed, Error: "磁盘 /data 已用 96%",
		},
	}
	// 经 Service 推（真实路径），而不是直接调 Conn.Notify：
	// Bind 已经把 a.Notifier 换成了 Service 自己，所以这里拿到的就是生产路径上那个对象。
	svc, ok := a.Notifier.(*Service)
	if !ok {
		t.Fatalf("Bind 之后 a.Notifier 应是 *Service，实际 %T——说明宿主没接管事件出口", a.Notifier)
	}
	svc.OnTaskDone(ev)

	n, err := c.waitNotification("goal/done", 5*time.Second, nil)
	if err != nil {
		t.Fatalf("没收到 goal/done 通知：%v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(n.Params, &got); err != nil {
		t.Fatalf("通知参数解析失败：%v", err)
	}
	if got["task_id"] != "t-done" {
		t.Errorf("通知应带 task_id，实际 %v", got["task_id"])
	}
	if got["origin"] != "定时任务「每日巡检」" {
		t.Errorf("通知应带来源，实际 %v", got["origin"])
	}
	// 状态要在事件里能读出来——只推一个 task_id，收到的人还得再查一次才知道是成是败。
	res, _ := got["result"].(map[string]any)
	if res == nil || res["status"] != "failed" {
		t.Errorf("通知应带结果状态，实际 %v", got["result"])
	}
}

// fieldOf 取 JSON 对象里的一个字段（RawMessage → any）。
func fieldOf(t *testing.T, raw json.RawMessage, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析响应失败：%v（原文 %s）", err, raw)
	}
	return m[key]
}
