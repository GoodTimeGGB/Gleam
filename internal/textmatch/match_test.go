package textmatch

import "testing"

// 中文取相邻二元组：没有分词器，二元组是零依赖下最便宜的近似。
func TestTerms_CJKBigrams(t *testing.T) {
	got := Terms("销售数据")
	for _, want := range []string{"销售", "售数", "数据"} {
		if !got[want] {
			t.Errorf("应切出二元组 %q，实际 %v", want, got)
		}
	}
	// 单字不成词：4 个字只切出 3 个二元组，不该出现单字项
	if got["销"] || got["据"] {
		t.Errorf("不该出现单字项: %v", got)
	}
}

// 两字母词不进检索词表：go / in / is / id 到处都是，当检索词只会制造噪音。
func TestTerms_DropsShortAlnum(t *testing.T) {
	for _, w := range []string{"go", "in", "is", "id"} {
		if Terms(w)[w] {
			t.Errorf("两字母词 %q 不该进入检索词表", w)
		}
	}
	if !Terms("goa")["goa"] {
		t.Error("三字母词应当保留")
	}
	// 大小写归一：词表与匹配都按小写
	if !Terms("SALES")["sales"] {
		t.Errorf("应归一为小写: %v", Terms("SALES"))
	}
}

// 英文/数字必须**整词**匹配：子串匹配会让 "goa" 命中 "goals"，
// 于是一个短词能让半个候选列表都"相关"起来。
func TestScore_AlnumIsWholeWord(t *testing.T) {
	terms := Terms("goa") // 词表：{goa}
	if n := Score(terms, "inspect goals and objectives"); n != 0 {
		t.Errorf("整词匹配不该命中 goals，实际得分 %d", n)
	}
	if n := Score(terms, "check goa state"); n != 1 {
		t.Errorf("整词命中应得 1 分，实际 %d", n)
	}
}

// 中文按子串匹配（二元组没有词边界），大小写不敏感。
func TestScore_CJKIsSubstring(t *testing.T) {
	terms := Terms("日报")
	if n := Score(terms, "把当天记录整理成日报并发送"); n != 1 {
		t.Errorf("子串命中应得 1 分，实际 %d", n)
	}
	if n := Score(terms, "周报与月报"); n != 0 {
		t.Errorf("不该命中无关词，实际得分 %d", n)
	}
	if n := Score(Terms("REPORT"), "daily report"); n != 1 {
		t.Errorf("hay 应内部归一为小写，实际得分 %d", n)
	}
}

// 空词表恒 0：调用方不必先判空，也就不会因为漏判而误报"相关"。
func TestScore_EmptyTerms(t *testing.T) {
	if n := Score(nil, "任意文本"); n != 0 {
		t.Errorf("空词表应得 0 分，实际 %d", n)
	}
	if n := Score(map[string]bool{}, "任意文本"); n != 0 {
		t.Errorf("空词表应得 0 分，实际 %d", n)
	}
	// 标点/空白切不出词项
	if n := Score(Terms("，。！？"), "，。！？"); n != 0 {
		t.Errorf("纯标点应切不出词项，实际得分 %d", n)
	}
}

func TestIsAlnumWord(t *testing.T) {
	if !IsAlnumWord("sales") || !IsAlnumWord("v2") {
		t.Error("英文/数字词应判为 true")
	}
	if IsAlnumWord("销售") {
		t.Error("中文二元组应判为 false")
	}
	if IsAlnumWord("") {
		t.Error("空串应判为 false")
	}
}
