package agent

import (
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/scheduler"
)

// TestJobToScheduleView_PrintsEffectivePolicy 视图印的是**生效值**，不是原始字段。
//
// 空串在存储层表示"用默认"，但界面要回答的是"这个任务跑完会不会通知我"。
// 印一个空值等于让人自己去猜默认是什么，而"猜不出来"的实际后果是
// 没人知道自己会不会被半夜叫醒。
func TestJobToScheduleView_PrintsEffectivePolicy(t *testing.T) {
	cases := []struct {
		stored string
		want   string
	}{
		{"", scheduler.NotifyOnFailure}, // 空 = 默认，必须展开成具体值
		{scheduler.NotifyAlways, scheduler.NotifyAlways},
		{scheduler.NotifyNever, scheduler.NotifyNever},
		{scheduler.NotifyOnFailure, scheduler.NotifyOnFailure},
	}
	for _, c := range cases {
		got := jobToScheduleView(scheduler.Job{Name: "j", Notify: c.stored}).Notify
		if got != c.want {
			t.Errorf("存储值 %q 应印成 %q，实际 %q", c.stored, c.want, got)
		}
	}
}

// TestJobToScheduleView_DirtyStoredValuePrintedAsIs 存量脏值如实印出，不悄悄改成默认。
//
// 老版本可能往盘上写过非法值（或者有人手改了 schedules.json）。这时**不能**
// 美化：用户看到的必须与盘上一致，否则"界面说 on_failure、实际按 never 静默"
// 这种事就永远查不出来——而它恰好是最需要查出来的那一类。
func TestJobToScheduleView_DirtyStoredValuePrintedAsIs(t *testing.T) {
	got := jobToScheduleView(scheduler.Job{Name: "j", Notify: "shouty"}).Notify
	if got != "shouty" {
		t.Errorf("脏值应原样印出（好让用户发现配置坏了），实际 %q", got)
	}
}

// TestJobToScheduleView_KeepsScheduleFields 顺带钉住视图没把别的字段丢掉。
//
// 这条是防止"改通知策略时顺手把视图字段改坏"——视图是多个入口共用的，
// 少印一个字段表现为界面上某一列突然空白，而没人会想到是这里。
func TestJobToScheduleView_KeepsScheduleFields(t *testing.T) {
	next := time.Now().Add(time.Hour)
	v := jobToScheduleView(scheduler.Job{
		Name: "巡检", Cron: "0 9 * * *", Goal: "检查磁盘占用", Mode: "auto",
		Enabled: true, WhenText: "每天9点", ScheduleText: "每天 09:00",
		NextRun: &next,
	})
	for _, c := range []struct{ field, got, want string }{
		{"Name", v.Name, "巡检"},
		{"Cron", v.Cron, "0 9 * * *"},
		{"Goal", v.Goal, "检查磁盘占用"},
		{"Mode", v.Mode, "auto"},
		{"WhenText", v.WhenText, "每天9点"},
		{"ScheduleText", v.ScheduleText, "每天 09:00"},
	} {
		if c.got != c.want {
			t.Errorf("视图字段 %s = %q，期望 %q", c.field, c.got, c.want)
		}
	}
	if !v.Enabled {
		t.Error("视图应带上启用状态")
	}
	if v.NextRun == "" || !strings.Contains(v.NextRun, "T") {
		t.Errorf("NextRun 应排成 RFC3339，实际 %q", v.NextRun)
	}
}
