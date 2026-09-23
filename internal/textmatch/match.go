// Package textmatch 提供轻量的关键词相关度打分：中文按相邻二元组、英文/数字按整词。
//
// 它服务于「用一段文字去挑候选」这类判断——工具路由（用目标挑工具）与技能筛选
// （用目标挑技能）。两者要的是同一个问题：「这段自述里有多少词真的出现在目标里」。
//
// 为什么抽成独立包而不是各写一份：**两处各写一份判据，"相关"就会有两个含义。**
// 一处按整词、一处按子串，同一句话在工具列表里排第一、在技能列表里排不上，
// 而两边看起来都"正常"。判据只有一份实现，讨论"筛得准不准"才有意义。
//
// 它刻意不引分词器：中文没有空格，二元组是零依赖下最便宜的近似（"销售"能命中
// "销售数据"，也能命中"销售额"，代价是"数据表"这种无关词偶尔也会沾边）。
// 用辅助模型做语义筛选是另一条路，但那要花调用、且结果不再确定——
// 这一层的价值恰恰在于**零调用、可复现、可离线进 CI**。
package textmatch

import "strings"

// MinAlnumTerm 英文/数字词的最小长度。
// 两字母词（go / in / is / id）到处都是，当检索词只会制造噪音，不如不要。
const MinAlnumTerm = 3

// Terms 把查询串切成用于匹配的词项：中文取相邻二元组，英文/数字取长度 ≥MinAlnumTerm 的整词。
func Terms(s string) map[string]bool {
	terms := map[string]bool{}
	runes := []rune(strings.ToLower(s))
	for i := 0; i+1 < len(runes); i++ {
		if isCJK(runes[i]) && isCJK(runes[i+1]) {
			terms[string(runes[i:i+2])] = true
		}
	}
	for w := range alnumWords(strings.ToLower(s)) {
		if len([]rune(w)) >= MinAlnumTerm {
			terms[w] = true
		}
	}
	return terms
}

// Score 统计 terms 里有多少词项出现在 hay 中：英文/数字按整词匹配，中文按子串。
//
// 英文/数字必须整词匹配：子串匹配会把 "go" 命中描述里的 "goal"，
// 于是一个两字母的检索词能让半个菜单都"相关"起来。
// 中文没有词边界，只能按子串——这是二元组方案的已知代价，写在上面那段说明里。
func Score(terms map[string]bool, hay string) int {
	if len(terms) == 0 {
		return 0
	}
	hay = strings.ToLower(hay)
	words := alnumWords(hay)
	s := 0
	for term := range terms {
		if IsAlnumWord(term) {
			if words[term] {
				s++
			}
			continue
		}
		if strings.Contains(hay, term) {
			s++
		}
	}
	return s
}

// IsAlnumWord 判断一个词项是英文/数字词（而非中文二元组）。
// 判据是首字符：二元组必然以 CJK 开头。
func IsAlnumWord(term string) bool {
	for _, r := range term {
		return isAlnum(r)
	}
	return false
}

// alnumWords 把文本按「非字母数字」切开，返回词集合。
func alnumWords(s string) map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !isAlnum(r)
	}) {
		words[w] = true
	}
	return words
}

func isCJK(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}
