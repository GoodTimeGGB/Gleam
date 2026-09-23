package scheduler

import (
	"testing"
	"time"
)

func loc() *time.Location { return time.Local }

func TestParseCron_Errors(t *testing.T) {
	bad := []string{
		"", "0 9 * *", "0 9 * * * *", // 段数
		"60 * * * *", "* 24 * * *", // 越界
		"* * 0 * *", "* * 32 * *", // 日越界
		"* * * 0 *", "* * * 13 *", // 月越界
		"*/0 * * * *", "a * * * *", "1-5-9 * * * *",
	}
	for _, expr := range bad {
		if _, err := ParseCron(expr); err == nil {
			t.Errorf("%q 应报错", expr)
		}
	}
}

func TestParseCron_Valid(t *testing.T) {
	if _, err := ParseCron("0 9 * * 1-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCron("*/15 8-18 * * *"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCron("30 9 1,15 * *"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCron("0 0 * * 7"); err != nil {
		t.Fatal(err) // 7=周日
	}
}

func TestCron_Match(t *testing.T) {
	c, _ := ParseCron("30 9 * * 1-5")
	monday := time.Date(2026, 9, 7, 9, 30, 0, 0, loc()) // 周一
	if !c.Match(monday) {
		t.Error("周一 09:30 应命中")
	}
	saturday := time.Date(2026, 9, 12, 9, 30, 0, 0, loc())
	if c.Match(saturday) {
		t.Error("周六不应命中")
	}
	if c.Match(time.Date(2026, 9, 7, 9, 31, 0, 0, loc())) {
		t.Error("09:31 不应命中")
	}
}

func TestCron_Next(t *testing.T) {
	// 周五 2026-09-04 10:00 之后，0 9 * * 1-5 的下一次是周一 09:00
	c, _ := ParseCron("0 9 * * 1-5")
	after := time.Date(2026, 9, 4, 10, 0, 0, 0, loc())
	next := c.Next(after)
	want := time.Date(2026, 9, 7, 9, 0, 0, 0, loc())
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
	// 间隔 15 分钟
	c2, _ := ParseCron("*/15 * * * *")
	base := time.Date(2026, 9, 5, 12, 2, 0, 0, loc())
	if got := c2.Next(base); !got.Equal(time.Date(2026, 9, 5, 12, 15, 0, 0, loc())) {
		t.Errorf("*/15 next = %v", got)
	}
	// 周日兼容 7
	c3, _ := ParseCron("0 12 * * 7")
	sat := time.Date(2026, 9, 12, 0, 0, 0, 0, loc()) // 周六
	if got := c3.Next(sat); got.Weekday() != time.Sunday {
		t.Errorf("7 应映射周日: %v", got)
	}
	// dom/dow 或语义：9月1日 或 每周一
	c4, _ := ParseCron("0 8 1 * 1")
	sep2 := time.Date(2026, 9, 2, 0, 0, 0, 0, loc()) // 周三，1日已过
	got := c4.Next(sep2)
	if !(got.Day() == 7 && got.Weekday() == time.Monday) {
		t.Errorf("dom/dow 并集语义: %v", got)
	}
}
