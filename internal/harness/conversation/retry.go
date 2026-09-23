package conversation

import (
	"strings"
	"time"
)

// RetryWindow 判定"短时间内再次提交"的时间窗。
// 超窗的相似提交更可能是新任务（"再整理一次"过了一周是新一轮整理，不是重试）。
const RetryWindow = 10 * time.Minute

// SimThreshold 相似度阈值：rune 二元组 Jaccard 相似度达到它即视为同一目标。
// 用户重试时的输入是原话微调（"整理下载目录的文件" → "帮我整理一下下载目录的文件"，
// Jaccard ≈ 0.54），0.45 容得下这种改写；完全不同的任务二元组交集趋近 0，不会误报。
// 取向：宁可把"像的"算进来（误报只是多记一次重试），不能漏掉真实重试。
const SimThreshold = 0.45

// SimilarGoal 判断两段目标文本是否指向同一件事。
// 纯词法判定：规范化后取 CJK 字符二元组 + 拉丁词的集合，算 Jaccard 相似度。
// 不追求语义理解——重试检测宁可把"像的"算进来（误报只是多记一次重试），
// 也不能漏掉真实重试（漏了它，重试率这个最接近真相的信号就哑了）。
func SimilarGoal(a, b string) bool {
	na, nb := normalizeGoal(a), normalizeGoal(b)
	if na == "" || nb == "" {
		return false
	}
	if na == nb {
		return true
	}
	as, bs := bigrams(na), bigrams(nb)
	if len(as) == 0 || len(bs) == 0 {
		return false
	}
	inter := 0
	for g := range as {
		if bs[g] {
			inter++
		}
	}
	union := len(as) + len(bs) - inter
	return float64(inter)/float64(union) >= SimThreshold
}

// normalizeGoal 规范化：小写、去空白与常见标点——重试者往往只改了几个字或加了个逗号。
func normalizeGoal(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			continue
		case strings.ContainsRune("，。、！？；：\"'（）()【】[],.!?;:", r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bigrams 提取特征集：CJK 相邻二元组 + 拉丁整词。
func bigrams(s string) map[string]bool {
	out := map[string]bool{}
	var runes []rune
	flushLatin := func(word string) {
		if word != "" {
			out[word] = true
		}
	}
	var latin strings.Builder
	for _, r := range s {
		switch {
		case isASCIILetterOrDigit(r):
			latin.WriteRune(r)
			runes = runes[:0] // 拉丁词打断 CJK 连续段
		default:
			flushLatin(latin.String())
			latin.Reset()
			runes = append(runes, r)
		}
	}
	flushLatin(latin.String())
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = true
	}
	return out
}

func isASCIILetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// DetectRetry 判断该会话最近一条用户消息是否构成"重试"：
// 时间窗内、且与本次目标相似。目标刚被用户重发一遍是最接近真相的质量信号——
// 产品指标全是代理，"用户觉得没用又发了一遍"不是。
func (s *Store) DetectRetry(id, goal string) bool {
	p, err := s.path(id)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.readLocked(p)
	if err != nil {
		return false
	}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role != "user" {
			continue
		}
		return time.Since(m.CreatedAt) <= RetryWindow && SimilarGoal(m.Content, goal)
	}
	return false
}
