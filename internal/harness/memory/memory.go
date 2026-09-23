// Package memory 实现三层记忆架构：
//   - 短期记忆：内存环形缓冲（最近 N 轮对话）
//   - 工作记忆：任务状态由服务层持久化（tasks/*.json）
//   - 长期记忆：自研轻量向量索引（词法哈希向量 + 余弦相似度，纯 Go 零依赖）
package memory

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Turn 一轮对话。
type Turn struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// ShortTerm 环形缓冲短期记忆。
type ShortTerm struct {
	mu   sync.Mutex
	buf  []Turn
	cap  int
	next int
	full bool
}

// NewShortTerm 创建容量为 cap 的环缓冲。
func NewShortTerm(cap int) *ShortTerm {
	if cap <= 0 {
		cap = 20
	}
	return &ShortTerm{buf: make([]Turn, 0, cap), cap: cap}
}

// Add 追加一轮对话。
func (s *ShortTerm) Add(role, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := Turn{Role: role, Content: content, At: time.Now()}
	if len(s.buf) < s.cap {
		s.buf = append(s.buf, t)
	} else {
		s.buf[s.next] = t
		s.next = (s.next + 1) % s.cap
		s.full = true
	}
}

// Reset 清空短期对话缓冲（开始新对话时调用，不影响长期记忆与滚动摘要）。
func (s *ShortTerm) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = make([]Turn, 0, s.cap)
	s.next = 0
	s.full = false
}

// Recent 按时间序返回最近 n 轮。
func (s *ShortTerm) Recent(n int) []Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ordered []Turn
	if s.full {
		ordered = append(ordered, s.buf[s.next:]...)
		ordered = append(ordered, s.buf[:s.next]...)
	} else {
		ordered = append(ordered, s.buf...)
	}
	if n <= 0 || n >= len(ordered) {
		return append([]Turn(nil), ordered...)
	}
	return append([]Turn(nil), ordered[len(ordered)-n:]...)
}

// Oldest 返回最旧的一轮（环缓冲满时该轮即将被覆盖，Manager 先抢救进压缩溢出区）。
func (s *ShortTerm) Oldest() (Turn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		return Turn{}, false
	}
	var t Turn
	if s.full {
		t = s.buf[s.next] // next 指向最旧槽位
	} else {
		t = s.buf[0]
	}
	return t, true
}

// Len 当前轮数。
func (s *ShortTerm) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buf)
}

// ---------- 自研向量索引 ----------

// Item 长期记忆条目。
type Item struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Vec       []float32 `json:"vec"`
	// Source 来源："task"（任务收尾自动沉淀）| "user"（memory.save 工具写入）。
	// 旧文件无此字段（零值空串），读取时天然兼容。
	Source string `json:"source,omitempty"`
	// Deleted 软删除标记：检索一律跳过，条目仍在库中可追溯/可恢复。
	// 硬删除（物理移除）只发生在容量淘汰。旧文件无此字段，天然兼容。
	Deleted bool `json:"deleted,omitempty"`
}

// 检索相似度阈值：低于它的命中是词法哈希的长尾噪音（二元组偶然撞词），
// 注入上下文只会稀释注意力。取值依据：相关中文文本的余弦普遍 > 0.25，
// 完全不相关的普遍 < 0.12，0.15 在两者之间留了余量。
const MinHitScore = 0.15

// DupThreshold 去重阈值：新内容与现有条目的余弦达到它即视为同一条记忆，
// 更新原条目而不是新增——同一目标跑 10 次不该产生 10 条互相挤占的任务记录。
const DupThreshold = 0.92

// Hit 检索命中。
type Hit struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
	Score   float32  `json:"score"`
}

// tokenize 分词：拉丁词（小写）+ CJK 双字组（适配中文检索）。
func tokenize(text string) []string {
	var tokens []string
	var latin strings.Builder
	var cjk []rune
	flushLatin := func() {
		if latin.Len() > 0 {
			tokens = append(tokens, latin.String())
			latin.Reset()
		}
	}
	flushCJK := func() {
		for i := 0; i+1 < len(cjk); i++ {
			tokens = append(tokens, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-':
			latin.WriteRune(toLower(r))
			flushCJK()
		case isCJK(r):
			flushLatin()
			cjk = append(cjk, r)
		default:
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return tokens
}

func toLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

func isCJK(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF || // CJK 统一表意文字
		r >= 0x3400 && r <= 0x4DBF || // 扩展 A
		r >= 0xF900 && r <= 0xFAFF // 兼容表意文字
}

// embed 词法哈希嵌入：FNV-1a 散列到固定维度，次线性 TF 加权，L2 归一化。
// 这是词法级"语义"近似（非深度模型），对中文双字词/英文词有良好的召回表现。
func embed(text string, dim int) []float32 {
	if dim <= 0 {
		dim = 256
	}
	tf := map[string]float32{}
	for _, tok := range tokenize(text) {
		tf[tok]++
	}
	vec := make([]float32, dim)
	for tok, freq := range tf {
		idx := fnv1a(tok) % uint32(dim)
		vec[idx] += 1 + float32(freq-1)*0.5 // 次线性加权
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec
	}
	inv := float32(1.0 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= inv
	}
	return vec
}

const (
	fnvOffset uint32 = 2166136261
	fnvPrime  uint32 = 16777619
)

func fnv1a(s string) uint32 {
	h := fnvOffset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime
	}
	return h
}

// cosine 余弦相似度（向量已归一化时即点积）。
func cosine(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	if dot > 1 {
		dot = 1
	}
	return float32(dot)
}

// SearchHitsTop 通用 top-k 工具（供 Store 复用）。
//
// 两道过滤都在这里做，保证所有检索路径一致：
//   - 软删除条目跳过（Deleted 只对检索隐身，不物理消失）；
//   - 相似度低于 MinHitScore 的丢弃——词法哈希的长尾是噪音，注入只会稀释注意力。
func SearchHitsTop(items []Item, query string, k int, dim int) []Hit {
	qv := embed(query, dim)
	type pair struct {
		idx   int
		score float32
	}
	pairs := make([]pair, 0, len(items))
	for i, it := range items {
		if it.Deleted {
			continue
		}
		score := cosine(qv, it.Vec)
		if score < MinHitScore {
			continue
		}
		pairs = append(pairs, pair{i, score})
	}
	// 简单选择 top-k（条目规模适中，避免引入堆的复杂度）
	for i := 0; i < k && i < len(pairs); i++ {
		max := i
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].score > pairs[max].score {
				max = j
			}
		}
		pairs[i], pairs[max] = pairs[max], pairs[i]
	}
	out := make([]Hit, 0, k)
	for i := 0; i < k && i < len(pairs); i++ {
		it := items[pairs[i].idx]
		out = append(out, Hit{ID: it.ID, Content: it.Content, Tags: it.Tags, Score: pairs[i].score})
	}
	return out
}

// ValidateItem 基本校验（导出以便持久层复用）。
func ValidateItem(it Item) error {
	if it.ID == "" {
		return fmt.Errorf("记忆条目缺少 ID")
	}
	if strings.TrimSpace(it.Content) == "" {
		return fmt.Errorf("记忆条目内容为空")
	}
	return nil
}
