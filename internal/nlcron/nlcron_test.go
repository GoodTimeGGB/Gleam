package nlcron

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		wantCron string
		wantSec  int
	}{
		{"每天9点整理下载目录", "0 9 * * *", 0},
		{"每天上午九点半提醒我", "30 9 * * *", 0},
		{"每日 08:15", "15 8 * * *", 0},
		{"工作日18:30备份", "30 18 * * 1-5", 0},
		{"周末晚上8点", "0 20 * * 0,6", 0},
		{"每周一和周五早上9点开会", "0 9 * * 1,5", 0},
		{"每周三下午3点", "0 15 * * 3", 0},
		{"周一到周五 23:00", "0 23 * * 1-5", 0},
		{"每月1号9点生成月报", "0 9 1 * *", 0},
		{"每月十五号 8:00", "0 8 15 * *", 0},
		{"每天中午12点", "0 12 * * *", 0},
		{"每天中午1点", "0 13 * * *", 0},
		{"每隔30分钟检查一次", "", 1800},
		{"每小时同步", "", 3600},
		{"每隔2小时", "", 7200},
		{"每天 21:45", "45 21 * * *", 0},
	}
	for _, c := range cases {
		sp, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) 返回错误: %v", c.in, err)
			continue
		}
		if c.wantCron != "" && sp.Cron != c.wantCron {
			t.Errorf("Parse(%q) cron = %q, 期望 %q", c.in, sp.Cron, c.wantCron)
		}
		if c.wantSec > 0 && sp.IntervalSec != c.wantSec {
			t.Errorf("Parse(%q) interval = %d, 期望 %d", c.in, sp.IntervalSec, c.wantSec)
		}
		if sp.Cron == "" && sp.IntervalSec == 0 {
			t.Errorf("Parse(%q) 未产生任何调度规则", c.in)
		}
		if sp.Summary == "" {
			t.Errorf("Parse(%q) 缺少人读摘要", c.in)
		}
	}
}

func TestParseDefaultTime(t *testing.T) {
	// 只说“每天定时”未给时刻，默认上午 9 点。
	sp, err := Parse("每天定时帮我整理桌面")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Cron != "0 9 * * *" {
		t.Fatalf("cron = %q, 期望 0 9 * * *", sp.Cron)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "随便什么时候", "帮我写个报告"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) 应当失败", in)
		}
	}
	// 间隔最小 5 秒。
	if _, err := Parse("每隔1秒"); err == nil {
		t.Error("每隔1秒 应当被拒绝")
	}
}

func TestResolvePriority(t *testing.T) {
	// 显式 cron 优先于自然语言。
	cron, sec, _, err := Resolve("0 7 * * *", 0, "每天9点")
	if err != nil || cron != "0 7 * * *" || sec != 0 {
		t.Fatalf("显式 cron 优先级错误: %q %d %v", cron, sec, err)
	}
	// 间隔次之。
	cron, sec, _, err = Resolve("", 600, "每天9点")
	if err != nil || cron != "" || sec != 600 {
		t.Fatalf("间隔优先级错误: %q %d %v", cron, sec, err)
	}
	// 都没有则解析 when。
	cron, sec, summary, err := Resolve("", 0, "每天9点")
	if err != nil || cron != "0 9 * * *" || summary == "" {
		t.Fatalf("自然语言解析错误: %q %d %s %v", cron, sec, summary, err)
	}
	// 全空报错。
	if _, _, _, err := Resolve("", 0, ""); err == nil {
		t.Error("缺少触发方式应当报错")
	}
}
