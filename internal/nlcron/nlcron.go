// Package nlcron 把中文自然语言时间描述解析成调度规则（Cron 或固定间隔）。
// 面向“每天早上9点 / 工作日18:30 / 每周一和周五晚上8点 / 每月1号9点 / 每隔30分钟”等口语表达，
// 覆盖日常场景，不追求完整时间语法；无法识别时返回带示例的错误。
package nlcron

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Spec 解析结果：Cron 与 IntervalSec 二选一，Summary 为人读说明。
type Spec struct {
	Cron        string
	IntervalSec int
	Summary     string
}

// 数字片段：阿拉伯数字 / 中文数字 / “半”。
const numPat = `(?:\d{1,3}|[零一二两三四五六七八九十]{1,3}|半)`

var (
	// 裸频率
	intervalBareRe = regexp.MustCompile(`每(?:半个)?小时|每刻钟|每分钟|每分|每秒`)
	// 周几范围：周一到周五 / 星期一至星期五
	dowRangeRe = regexp.MustCompile(`(?:星期|礼拜|周)\s*([日天一二三四五六])\s*(?:到|至|~|～|-)\s*(?:星期|礼拜|周)?\s*([日天一二三四五六])`)
	// 周几枚举：周一 / 星期三 / 礼拜五
	dowItemRe = regexp.MustCompile(`(?:星期|礼拜|周)\s*([日天一二三四五六])`)
	// 每月 N 号/日
	monthlyRe = regexp.MustCompile(`每(?:个)?月\s*(` + numPat + `)\s*(?:号|日|天)`)
	dailyRe   = regexp.MustCompile(`每天|每日|天天|每日定时|每天定时`)
	// HH:MM
	clockColonRe = regexp.MustCompile(`(\d{1,2})[:：](\d{1,2})`)
	// X 点 [半|一刻|三刻|N 分]
	clockDianRe = regexp.MustCompile(`(` + numPat + `)\s*点(?:\s*(半|一刻|三刻|` + numPat + `\s*分?))?`)
	periodRe    = regexp.MustCompile(`凌晨|清晨|早上|早晨|上午|中午|下午|傍晚|晚上|晚`)
)

var cnDigit = map[rune]int{
	'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
	'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

var dowCN = map[rune]int{'日': 0, '天': 0, '一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6}

var dowName = []string{"日", "一", "二", "三", "四", "五", "六"}

// Parse 解析中文时间描述。
func Parse(text string) (Spec, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Spec{}, fmt.Errorf("时间描述为空")
	}

	// 1) 固定间隔：每隔 N 秒/分/时
	if sp, ok, err := parseInterval(text); err != nil {
		return Spec{}, err
	} else if ok {
		return sp, nil
	}

	// 2) 工作日 / 周末
	switch {
	case strings.Contains(text, "工作日") || strings.Contains(text, "平日"):
		return buildCron(text, "1-5", 0, "工作日 %s（周一至周五）", false)
	case strings.Contains(text, "周末"):
		return buildCron(text, "0,6", 0, "周末 %s（周六、周日）", false)
	}

	// 3) 每周几（范围 + 枚举）
	dows := map[int]bool{}
	for _, m := range dowRangeRe.FindAllStringSubmatch(text, -1) {
		a, b := dowCN[[]rune(m[1])[0]], dowCN[[]rune(m[2])[0]]
		for d := a; ; d = (d + 1) % 7 {
			dows[d] = true
			if d == b {
				break
			}
		}
	}
	for _, m := range dowItemRe.FindAllStringSubmatch(text, -1) {
		dows[dowCN[[]rune(m[1])[0]]] = true
	}
	if len(dows) > 0 {
		list := make([]int, 0, len(dows))
		for d := range dows {
			list = append(list, d)
		}
		// 展示按周一到周六、周日顺序
		order := []int{1, 2, 3, 4, 5, 6, 0}
		sorted := make([]int, 0, len(list))
		names := make([]string, 0, len(list))
		for _, d := range order {
			if dows[d] {
				sorted = append(sorted, d)
				names = append(names, "周"+dowName[d])
			}
		}
		parts := compressDows(sorted)
		return buildCron(text, parts, 0,
			"每"+strings.Join(names, "、")+" %s", false)
	}

	// 4) 每月 N 号
	if m := monthlyRe.FindStringSubmatch(text); m != nil {
		day, ok := atoiCN(m[1])
		if !ok || day < 1 || day > 31 {
			return Spec{}, fmt.Errorf("日期无效：%q", m[1])
		}
		return buildCron(text, "", day, fmt.Sprintf("每月 %d 号 %%s", day), false)
	}

	// 5) 每天（含无频率词、仅给时间点的兜底，在创建定时任务语境下视为每天）
	hasDaily := dailyRe.MatchString(text)
	h, mi, hasTime := parseClock(text)
	if hasDaily || hasTime {
		if !hasTime {
			h, mi = 9, 0 // “每天定时整理”这类未给时刻的表达，默认上午 9 点
		}
		cron := fmt.Sprintf("%d %d * * *", mi, h)
		suffix := ""
		if !hasTime {
			suffix = "（未指定时刻，默认上午 9:00）"
		}
		return Spec{Cron: cron, Summary: "每天 " + fmt.Sprintf("%02d:%02d", h, mi) + suffix}, validate(cron)
	}

	return Spec{}, fmt.Errorf("没看懂时间 %q，可试试「每天9点」「工作日18:30」「每周一早上9点」「每月1号9点」「每隔30分钟」", text)
}

// Resolve 统一优先级：显式 cron/interval 优先，否则解析自然语言 when。
// 返回 cron、间隔秒与人类摘要。
func Resolve(cron string, intervalSec int, when string) (string, int, string, error) {
	cron = strings.TrimSpace(cron)
	if cron != "" {
		return cron, 0, "", nil
	}
	if intervalSec > 0 {
		return "", intervalSec, "", nil
	}
	when = strings.TrimSpace(when)
	if when == "" {
		return "", 0, "", fmt.Errorf("需要提供触发方式：自然语言时间（when）、cron 或间隔秒数")
	}
	sp, err := Parse(when)
	if err != nil {
		return "", 0, "", err
	}
	return sp.Cron, sp.IntervalSec, sp.Summary, nil
}

// parseInterval 解析“每隔 N 秒/分/时”与裸“每小时/每分钟/每秒”。
func parseInterval(text string) (Spec, bool, error) {
	re := regexp.MustCompile(`每(?:隔)?\s*(` + numPat + `)\s*(?:个)?(秒钟|秒|分钟|分|小时|钟头)`)
	if m := re.FindStringSubmatch(text); m != nil {
		mult, err := fracNum(m[1])
		if err != nil {
			return Spec{}, true, err
		}
		sec := intervalUnitSec(m[2])
		total := int(mult * float64(sec))
		if total < 5 {
			return Spec{}, true, fmt.Errorf("间隔最小为 5 秒")
		}
		return Spec{IntervalSec: total, Summary: "每隔 " + humanInterval(total)}, true, nil
	}
	if m := intervalBareRe.FindString(text); m != "" {
		var sec int
		switch {
		case strings.Contains(m, "小时"):
			sec = 1800 // 每半小时
			if !strings.Contains(m, "半") {
				sec = 3600
			}
		case strings.Contains(m, "刻钟"):
			sec = 900
		case strings.Contains(m, "分"):
			sec = 60
		default:
			sec = 5 // 每秒受最小间隔限制
		}
		return Spec{IntervalSec: sec, Summary: "每隔 " + humanInterval(sec)}, true, nil
	}
	return Spec{}, false, nil
}

func intervalUnitSec(unit string) int {
	switch {
	case strings.Contains(unit, "小时") || strings.Contains(unit, "钟头"):
		return 3600
	case strings.Contains(unit, "分"):
		return 60
	default:
		return 1
	}
}

// buildCron 组装带时刻/星期/月日的 cron。day>0 时为每月任务，dow 非空时为每周任务。
func buildCron(text, dow string, day int, summaryFmt string, _ bool) (Spec, error) {
	h, mi, hasTime := parseClock(text)
	if !hasTime {
		h, mi = 9, 0
	}
	var cron string
	switch {
	case day > 0:
		cron = fmt.Sprintf("%d %d %d * *", mi, h, day)
	case dow != "":
		cron = fmt.Sprintf("%d %d * * %s", mi, h, dow)
	default:
		cron = fmt.Sprintf("%d %d * * *", mi, h)
	}
	summary := strings.Replace(summaryFmt, "%s", fmt.Sprintf("%02d:%02d", h, mi), 1)
	if !hasTime {
		summary += "（未指定时刻，默认上午 9:00）"
	}
	return Spec{Cron: cron, Summary: summary}, validate(cron)
}

// parseClock 提取时刻：优先 HH:MM，其次“X点[Y分/半/一刻/三刻]”，并按上午/下午等时段词换算 24 小时制。
func parseClock(text string) (int, int, bool) {
	if m := clockColonRe.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		if h > 23 || mi > 59 {
			return 0, 0, false
		}
		return applyPeriod(h, text), mi, true
	}
	if m := clockDianRe.FindStringSubmatch(text); m != nil {
		n, ok := atoiCN(m[1])
		if !ok || n > 23 {
			return 0, 0, false
		}
		mi := 0
		switch {
		case m[2] == "半":
			mi = 30
		case m[2] == "一刻":
			mi = 15
		case m[2] == "三刻":
			mi = 45
		case m[2] != "":
			mnRaw := strings.TrimSuffix(strings.TrimSpace(m[2]), "分")
			if v, ok2 := atoiCN(mnRaw); ok2 && v <= 59 {
				mi = v
			}
		}
		return applyPeriod(n, text), mi, true
	}
	return 0, 0, false
}

// applyPeriod 依据时段词把 12 小时制读法换算成 24 小时制。
func applyPeriod(h int, text string) int {
	p := periodRe.FindString(text)
	switch p {
	case "下午", "傍晚", "晚上", "晚":
		if h == 12 {
			return 0 // 晚上 12 点即午夜 0 点
		}
		if h < 12 {
			return h + 12
		}
	case "中午":
		if h < 12 {
			return h + 12 // 中午 1 点即 13 点
		}
	case "凌晨":
		if h == 12 {
			return 0
		}
	}
	return h
}

// compressDows 把连续的星期压缩成区间（如 1,2,3,4,5 → 1-5），其余用逗号拼接。
func compressDows(dows []int) string {
	if len(dows) == 0 {
		return ""
	}
	var segs []string
	start, prev := dows[0], dows[0]
	flush := func(end int) {
		switch {
		case end == start:
			segs = append(segs, strconv.Itoa(start))
		case end == start+1:
			segs = append(segs, strconv.Itoa(start), strconv.Itoa(end))
		default:
			segs = append(segs, strconv.Itoa(start)+"-"+strconv.Itoa(end))
		}
	}
	for i := 1; i < len(dows); i++ {
		if dows[i] == prev+1 {
			prev = dows[i]
			continue
		}
		flush(prev)
		start, prev = dows[i], dows[i]
	}
	flush(prev)
	return strings.Join(segs, ",")
}

func validate(cron string) error {
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return fmt.Errorf("内部错误：非法 cron %q", cron)
	}
	return nil
}

// atoiCN 支持阿拉伯数字与简单中文数字（一~九十九，含“两/十”）。
func atoiCN(s string) (int, bool) {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n, true
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) == 0 {
		return 0, false
	}
	if len(r) == 1 {
		if d, ok := cnDigit[r[0]]; ok {
			return d, true
		}
		return 0, false
	}
	idx := -1
	for i, c := range r {
		if c == '十' {
			idx = i
		}
	}
	if idx < 0 {
		return 0, false
	}
	tens := 1
	if idx > 0 {
		d, ok := cnDigit[r[idx-1]]
		if !ok {
			return 0, false
		}
		tens = d
	}
	ones := 0
	if idx < len(r)-1 {
		d, ok := cnDigit[r[idx+1]]
		if !ok {
			return 0, false
		}
		ones = d
	}
	return tens*10 + ones, true
}

// fracNum 解析数量，允许“半”=0.5。
func fracNum(s string) (float64, error) {
	if strings.TrimSpace(s) == "半" {
		return 0.5, nil
	}
	n, ok := atoiCN(s)
	if !ok {
		return 0, fmt.Errorf("数量无效：%q", s)
	}
	return float64(n), nil
}

func humanInterval(sec int) string {
	switch {
	case sec%3600 == 0:
		return fmt.Sprintf("%d 小时", sec/3600)
	case sec%60 == 0:
		return fmt.Sprintf("%d 分钟", sec/60)
	default:
		return fmt.Sprintf("%d 秒", sec)
	}
}
