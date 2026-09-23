package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron 五段 Cron 表达式（分 时 日 月 周），支持 * , - / 与数字。
// 周字段 0/7 均为周日。
type Cron struct {
	min, hour, dom, mon, dow fieldSpec
	domRestricted            bool // 日/周均为受限字段时的"或"语义（标准 Cron）
	dowRestricted            bool
}

type fieldSpec struct {
	any    bool
	values map[int]bool
}

func (f fieldSpec) match(v int) bool {
	if f.any {
		return true
	}
	return f.values[v]
}

// ParseCron 解析五段 Cron 表达式。
func ParseCron(expr string) (*Cron, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式需要 5 段（分 时 日 月 周），收到 %d 段: %q", len(fields), expr)
	}
	c := &Cron{min: fieldSpec{values: map[int]bool{}}, hour: fieldSpec{values: map[int]bool{}},
		dom: fieldSpec{values: map[int]bool{}}, mon: fieldSpec{values: map[int]bool{}}, dow: fieldSpec{values: map[int]bool{}}}
	var err error
	if c.min.any, c.min.values, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("分钟字段: %w", err)
	}
	if c.hour.any, c.hour.values, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("小时字段: %w", err)
	}
	if c.dom.any, c.dom.values, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("日字段: %w", err)
	}
	if c.mon.any, c.mon.values, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("月字段: %w", err)
	}
	if c.dow.any, c.dow.values, err = parseField(fields[4], 0, 7); err != nil {
		return nil, fmt.Errorf("周字段: %w", err)
	}
	// 规范化 7 -> 0（周日）
	if !c.dow.any {
		if c.dow.values[7] {
			c.dow.values[0] = true
		}
		delete(c.dow.values, 7)
	}
	c.domRestricted = !c.dom.any
	c.dowRestricted = !c.dow.any
	return c, nil
}

// parseField 解析单字段：支持 *、a-b、*/n、a-b/n、a,b,c。
func parseField(s string, min, max int) (bool, map[int]bool, error) {
	values := map[int]bool{}
	any := false
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false, nil, fmt.Errorf("空片段 %q", s)
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			stepStr := part[i+1:]
			part = part[:i]
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return false, nil, fmt.Errorf("非法步长 %q", stepStr)
			}
			step = n
		}
		lo, hi := min, max
		switch part {
		case "*", "":
			if step == 1 && len(values) == 0 && !any {
				any = true
				continue
			}
		default:
			if i := strings.Index(part, "-"); i > 0 {
				a, err1 := strconv.Atoi(part[:i])
				b, err2 := strconv.Atoi(part[i+1:])
				if err1 != nil || err2 != nil {
					return false, nil, fmt.Errorf("非法区间 %q", part)
				}
				lo, hi = a, b
			} else {
				a, err := strconv.Atoi(part)
				if err != nil {
					return false, nil, fmt.Errorf("非法数值 %q", part)
				}
				lo, hi = a, a
			}
		}
		if lo < min || hi > max || lo > hi {
			return false, nil, fmt.Errorf("数值越界 [%d,%d]: %q", min, max, part)
		}
		for v := lo; v <= hi; v += step {
			values[v] = true
		}
	}
	return any, values, nil
}

// Match 判断时刻 t（本地时区）是否命中。
func (c *Cron) Match(t time.Time) bool {
	if !c.mon.match(int(t.Month())) {
		return false
	}
	domOK := c.dom.match(t.Day())
	dowOK := c.dow.match(int(t.Weekday()))
	// 标准 Cron 语义：日与周都受限时取并集
	if c.domRestricted && c.dowRestricted {
		if !domOK && !dowOK {
			return false
		}
	} else if !domOK || !dowOK {
		return false
	}
	if !c.hour.match(t.Hour()) {
		return false
	}
	return c.min.match(t.Minute())
}

// Next 返回 after 之后（不含 after）的下一次触发时刻。
// 从 after+1 分钟起逐分钟扫描，最多回绕一年。
func (c *Cron) Next(after time.Time) time.Time {
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(1, 0, 0)
	for t.Before(limit) {
		if c.Match(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
