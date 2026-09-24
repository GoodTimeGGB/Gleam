package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// raw 发一次请求并返回状态码与响应体（不走 fixture.call 的 4xx 断言）。
//
// 为什么需要它：本文件一半的用例要验**失败路径**（非法策略、任务不存在），
// 而 fixture.call 遇到 4xx 会直接 Fatal——那等于"只测能成功的那条路"。
func (f *fixture) raw(method, path string, body any) (int, map[string]any) {
	f.t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rdr)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestWebUI_ScheduleNotify_DefaultsPrinted 界面要回答"会不会通知我"，不能印空值。
//
// 空串在存储层表示"用默认"，但把空串原样返回给界面，用户只能去猜默认是什么——
// 而"猜不出来"的实际后果是没人知道自己会不会被半夜叫醒。
func TestWebUI_ScheduleNotify_DefaultsPrinted(t *testing.T) {
	f := newFixture(t, nil)
	defer f.call("DELETE", "/api/schedules/nj-default", nil)

	// 不传 notify：应印出生效的默认值，而不是空串。
	created := f.call("POST", "/api/schedules", map[string]any{
		"name": "nj-default", "goal": "检查磁盘占用", "cron": "0 9 * * *",
	})
	if got := created["notify"]; got != "on_failure" {
		t.Errorf("未指定策略时应印默认 on_failure，实际 %v", got)
	}

	// 列表里也要印生效值，不能只有创建响应里印。
	list := f.call("GET", "/api/schedules", nil)
	found := false
	for _, it := range list["jobs"].([]any) {
		j := it.(map[string]any)
		if j["name"] == "nj-default" {
			found = true
			if j["notify"] != "on_failure" {
				t.Errorf("列表里也应印生效策略，实际 %v", j["notify"])
			}
		}
	}
	if !found {
		t.Error("列表里应能看到刚建的任务")
	}
}

// TestWebUI_ScheduleNotify_SetAndReset 策略可改、可恢复默认。
//
// "策略是跑起来之后才会想改的东西"：一个每小时跑的任务设成 always，
// 跑两天发现太吵要改成 on_failure。要求删掉重建，用户就会选择忍受噪音。
func TestWebUI_ScheduleNotify_SetAndReset(t *testing.T) {
	f := newFixture(t, nil)
	defer f.call("DELETE", "/api/schedules/nj-set", nil)
	f.call("POST", "/api/schedules", map[string]any{
		"name": "nj-set", "goal": "检查磁盘占用", "cron": "0 9 * * *",
	})

	// 改成 never：结果只留在任务记录里，不再打扰。
	got := f.call("POST", "/api/schedules/nj-set/notify", map[string]any{"notify": "never"})
	if got["notify"] != "never" {
		t.Errorf("改策略后应返回 never，实际 %v", got["notify"])
	}
	// 再读列表确认**落盘了**，不是只在这次响应里改了。
	list := f.call("GET", "/api/schedules", nil)
	for _, it := range list["jobs"].([]any) {
		if j := it.(map[string]any); j["name"] == "nj-set" && j["notify"] != "never" {
			t.Errorf("列表里应看到 never，实际 %v", j["notify"])
		}
	}

	// 空串 = 恢复默认。
	got = f.call("POST", "/api/schedules/nj-set/notify", map[string]any{"notify": ""})
	if got["notify"] != "on_failure" {
		t.Errorf("空串应恢复默认 on_failure，实际 %v", got["notify"])
	}
}

// TestWebUI_ScheduleNotify_InvalidRejected 非法策略必须被拒，且**不留半成品**。
//
// "任务建好了但策略没设上"是最难排查的状态：任务看起来一切正常，只是跑完不通知。
// 所以策略要在创建**之前**校验——本用例同时钉住"拒绝"和"没建出来"两件事。
func TestWebUI_ScheduleNotify_InvalidRejected(t *testing.T) {
	f := newFixture(t, nil)

	code, out := f.raw("POST", "/api/schedules", map[string]any{
		"name": "nj-bad", "goal": "检查磁盘占用", "cron": "0 9 * * *", "notify": "yelling",
	})
	if code != 400 {
		t.Errorf("非法策略应返回 400，实际 %d（%v）", code, out)
	}
	// 报错信息要能照着改：点出可用值。
	msg, _ := out["error"].(string)
	for _, want := range []string{"always", "on_failure", "never"} {
		if !strings.Contains(msg, want) {
			t.Errorf("报错应列出可用值（缺 %q）：%s", want, msg)
		}
	}

	// 半成品检查：任务不该存在。
	code, _ = f.raw("POST", "/api/schedules/nj-bad/notify", map[string]any{"notify": "never"})
	if code == 200 {
		t.Error("非法策略创建失败后，不该留下同名任务（先校验再创建的判据被破坏了）")
	}
}

// TestWebUI_ScheduleEnabled_PauseResume 暂停/恢复可改且落盘（L2，2026-09-23 QA）。
// 启停是跑起来之后才会想改的开关，和 notify 同理：不该逼用户删掉重建。
func TestWebUI_ScheduleEnabled_PauseResume(t *testing.T) {
	f := newFixture(t, nil)
	defer f.call("DELETE", "/api/schedules/en-job", nil)
	created := f.call("POST", "/api/schedules", map[string]any{
		"name": "en-job", "goal": "检查磁盘占用", "cron": "0 9 * * *",
	})
	if created["enabled"] != true {
		t.Fatalf("新建任务应默认启用，实际 %v", created["enabled"])
	}
	got := f.call("POST", "/api/schedules/en-job/enabled", map[string]any{"enabled": false})
	if got["enabled"] != false {
		t.Errorf("暂停后应返回 enabled=false，实际 %v", got["enabled"])
	}
	list := f.call("GET", "/api/schedules", nil)
	for _, it := range list["jobs"].([]any) {
		if j := it.(map[string]any); j["name"] == "en-job" && j["enabled"] != false {
			t.Errorf("列表里应看到停用状态，实际 %v", j["enabled"])
		}
	}
	if got = f.call("POST", "/api/schedules/en-job/enabled", map[string]any{"enabled": true}); got["enabled"] != true {
		t.Errorf("恢复后应返回 enabled=true，实际 %v", got["enabled"])
	}
	// 缺 enabled 字段要 400，不能当成 false 处理
	if code, _ := f.raw("POST", "/api/schedules/en-job/enabled", map[string]any{}); code != 400 {
		t.Errorf("缺 enabled 字段应 400，实际 %d", code)
	}
	// 不存在的任务要报错
	if code, _ := f.raw("POST", "/api/schedules/en-nope/enabled", map[string]any{"enabled": false}); code != 400 {
		t.Errorf("不存在的任务应 400，实际 %d", code)
	}
}

// TestWebUI_ScheduleNotify_MissingJobRejected 改不存在的任务要报错，不能静默成功。
//
// 静默成功会让用户以为改好了——而那个任务从来就不存在，或者名字打错了。
func TestWebUI_ScheduleNotify_MissingJobRejected(t *testing.T) {
	f := newFixture(t, nil)
	code, out := f.raw("POST", "/api/schedules/never-existed/notify", map[string]any{"notify": "never"})
	if code != 400 {
		t.Errorf("不存在的任务应返回 400，实际 %d（%v）", code, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "never-existed") {
		t.Errorf("报错应点出是哪个任务：%v", out)
	}
}
