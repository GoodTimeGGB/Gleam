package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gleam/internal/atomicfile"
	"gleam/pkg/types"
)

// Store 长期记忆持久化存储（JSON 文件 + 原子写入）。
type Store struct {
	mu       sync.Mutex
	path     string
	dim      int
	maxItems int
	items    []Item
	index    map[string]int
	dirty    bool
}

// OpenStore 加载长期记忆存储；文件不存在则创建空库。
func OpenStore(path string, dim, maxItems int) (*Store, error) {
	if dim <= 0 {
		dim = 256
	}
	if maxItems <= 0 {
		maxItems = 5000
	}
	s := &Store{path: path, dim: dim, maxItems: maxItems, index: map[string]int{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		// 损坏文件不阻断启动：备份后重建
		_ = os.Rename(path, path+".corrupt")
		return s, nil
	}
	for _, it := range items {
		s.appendItem(it)
	}
	return s, nil
}

func (s *Store) appendItem(it Item) {
	if _, exists := s.index[it.ID]; exists {
		return
	}
	s.index[it.ID] = len(s.items)
	s.items = append(s.items, it)
}

// Remember 新增记忆（返回条目 ID）。写入前先按内容相似度判重：
// 余弦达到 DupThreshold 视为同一条记忆，更新原条目（内容/标签/时间/向量）而不是新增。
// 没有去重时，同一目标跑 10 次会产生 10 条几乎相同的任务记录，
// top-k 检索时互相挤占，把真正有用的记忆挤出上下文。
func (s *Store) Remember(content string, tags []string) (string, error) {
	return s.RememberWithSource(content, "", tags)
}

// RememberWithSource 同 Remember，并记录条目来源（"task" = 任务收尾自动沉淀，"user" = memory.save 写入）。
func (s *Store) RememberWithSource(content, source string, tags []string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("记忆内容为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	newVec := embed(content, s.dim)
	now := time.Now()

	// 去重：只在未软删的条目里判（软删条目对检索隐身，但内容若回来写应另起一条）
	dupIdx := -1
	for i := range s.items {
		if s.items[i].Deleted {
			continue
		}
		if cosine(newVec, s.items[i].Vec) >= DupThreshold {
			dupIdx = i
			break
		}
	}
	if dupIdx >= 0 {
		it := &s.items[dupIdx]
		it.Content = content
		it.Tags = append([]string(nil), tags...)
		it.CreatedAt = now
		it.Vec = newVec
		if source != "" {
			it.Source = source
		}
		s.dirty = true
		return it.ID, nil
	}

	id := types.NewID()
	it := Item{
		ID:        id,
		Content:   content,
		Tags:      append([]string(nil), tags...),
		CreatedAt: now,
		Vec:       newVec,
		Source:    source,
	}
	// 容量淘汰：超出上限移除最旧条目
	if len(s.items) >= s.maxItems {
		oldest := 0
		for i := 1; i < len(s.items); i++ {
			if s.items[i].CreatedAt.Before(s.items[oldest].CreatedAt) {
				oldest = i
			}
		}
		delete(s.index, s.items[oldest].ID)
		s.items = append(s.items[:oldest], s.items[oldest+1:]...)
		s.reindex()
	}
	s.appendItem(it)
	s.dirty = true
	return id, nil
}

// Search 词法检索 top-k（哈希向量余弦，同义不同形的词不相关）。
func (s *Store) Search(query string, k int) []Hit {
	s.mu.Lock()
	items := append([]Item(nil), s.items...)
	s.mu.Unlock()
	if k <= 0 {
		k = 5
	}
	return SearchHitsTop(items, query, k, s.dim)
}

// Delete 物理删除指定条目。工具入口一律走 SoftDelete（软删优先，可追溯）；
// 物理删除保留给容量淘汰与测试。
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.index[id]
	if !ok {
		return false
	}
	s.items = append(s.items[:idx], s.items[idx+1:]...)
	delete(s.index, id)
	s.reindex()
	s.dirty = true
	return true
}

// SoftDelete 软删除：只打标记，条目保留在库中（检索一律跳过）。
// 删除记忆是用户明确表达"这条不对/不要了"，物理移除会让误删不可挽回——
// 软删保留了改判的可能，也保留了"曾记过什么"的痕迹。
func (s *Store) SoftDelete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.index[id]
	if !ok || s.items[idx].Deleted {
		return false
	}
	s.items[idx].Deleted = true
	s.dirty = true
	return true
}

// DeletedCount 软删条目数（体检/统计用）。
func (s *Store) DeletedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for i := range s.items {
		if s.items[i].Deleted {
			n++
		}
	}
	return n
}

// Count 条目总数。
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// Flush 原子落盘（temp + rename）。
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.items, "", " ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.path, data, 0o644); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
func (s *Store) reindex() {
	s.index = make(map[string]int, len(s.items))
	for i, it := range s.items {
		s.index[it.ID] = i
	}
}

// ---------- Manager：面向 Agent 的统一记忆门面 ----------

// Manager 组合短期与长期记忆，并实现上下文自动压缩：
// 短期环缓冲满员后，被覆盖的旧对话先进入溢出区，压缩时汇总为滚动摘要，
// 保证"超出窗口的历史对话摘要存储"（设计文档 §9 风险应对）。
type Manager struct {
	Short *ShortTerm
	Long  *Store

	mu           sync.Mutex
	overflow     []Turn   // 待压缩的旧对话（环缓冲覆盖前抢救出来）
	summary      string   // 滚动摘要（跨任务持续积累，截断保留）
	savedTok     int      // 累计压缩节省的估算 token（原始溢出 - 摘要）
	pinned       []string // 钉住区：不可压缩的用户约束/关键结论（P2-1，见 Pin）
	omittedTurns int      // 累计被压缩掉的对话轮数（摘要省略量标注用）
	omittedRunes int      // 累计被压缩掉/截断掉的字数（rune）
	dir          string
}

// pinnedCap 钉住区上限（超出丢最旧——淘汰最旧是追加式的，不重排剩余条目，不违反「绝不重排」）。
const pinnedCap = 20

// pinHints 用户消息里的约束信号词：命中即把该轮钉进不可压缩区。
// 这是启发式——多钉一条的成本只是多注入 200 字，漏钉的代价是约束被压缩丢掉，宁可误报。
var pinHints = []string{"记住", "约定", "以后", "规则是", "必须", "始终", "一直用", "不要用"}

// Pin 把一条关键结论加入钉住区（去重追加；压缩不动它，注入时排在易变段最前，绝不重排）。
// 站点 D5 的判据：压缩可以丢过程，不能丢用户定下的约束。
func (m *Manager) Pin(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pinned {
		if p == text {
			return
		}
	}
	m.pinned = append(m.pinned, text)
	if len(m.pinned) > pinnedCap {
		m.pinned = m.pinned[len(m.pinned)-pinnedCap:]
	}
	m.saveContextLocked()
}

// Pinned 返回钉住区快照（按加入顺序——顺序本身就是语义，调用方不得重排）。
func (m *Manager) Pinned() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.pinned...)
}

// Omission 返回累计省略量（被压缩掉的轮数与字数）。
func (m *Manager) Omission() (turns, runes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.omittedTurns, m.omittedRunes
}

// SummaryAnnotated 返回带省略量标注的滚动摘要。
// 「已自动压缩」只说明摘要短，不说明丢了多少——省略量必须说出来，
// 读上下文的人（和模型）才知道这段摘要是"全貌"还是"残余"。
func (m *Manager) SummaryAnnotated() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(m.summary) == "" {
		return ""
	}
	return fmt.Sprintf("%s\n（已自动压缩：累计省略 %d 轮 / 约 %d 字）", m.summary, m.omittedTurns, m.omittedRunes)
}

// overflowCap 溢出区上限（超出即丢弃最旧，防止长期运行内存增长）。
const overflowCap = 100

// summaryCap 滚动摘要保留上限（rune）。
const summaryCap = 2000

// contextFile 数据目录下的压缩状态文件。
const contextFile = "context.json"

type contextSnapshot struct {
	Overflow     []Turn   `json:"overflow"`
	Summary      string   `json:"summary"`
	SavedToken   int      `json:"saved_tokens"`
	Pinned       []string `json:"pinned,omitempty"`        // P2-1：旧文件无此字段，零值即兼容
	OmittedTurns int      `json:"omitted_turns,omitempty"` // 累计省略轮数
	OmittedRunes int      `json:"omitted_runes,omitempty"` // 累计省略字数
}

// Open 打开记忆管理器（数据目录下 memory/longterm.json 与 memory/context.json）。
func Open(dataDir string, shortCap, dim, maxItems int) (*Manager, error) {
	dir := filepath.Join(dataDir, "memory")
	long, err := OpenStore(filepath.Join(dir, "longterm.json"), dim, maxItems)
	if err != nil {
		return nil, err
	}
	m := &Manager{Short: NewShortTerm(shortCap), Long: long, dir: dir}
	// 恢复压缩状态（尽力而为，损坏即丢弃）
	if data, err := os.ReadFile(filepath.Join(dir, contextFile)); err == nil {
		var snap contextSnapshot
		if json.Unmarshal(data, &snap) == nil {
			m.overflow = snap.Overflow
			m.summary = snap.Summary
			m.savedTok = snap.SavedToken
			m.pinned = snap.Pinned
			m.omittedTurns = snap.OmittedTurns
			m.omittedRunes = snap.OmittedRunes
		}
	}
	return m, nil
}

// AddTurn 记录一轮对话到短期记忆；环缓冲满员时先把最旧一轮抢救进溢出区。
// 用户消息命中约束信号词时同时钉进不可压缩区（P2-1）：
// "以后都……"这类话进了摘要就可能被下一轮压缩磨掉，钉住区保证它活着。
func (m *Manager) AddTurn(role, content string) {
	if role == "user" && hitsAny(content, pinHints) {
		m.Pin(content)
	}
	m.mu.Lock()
	if m.Short.Len() >= m.Short.cap {
		if old, ok := m.Short.Oldest(); ok {
			m.overflow = append(m.overflow, old)
			if len(m.overflow) > overflowCap {
				m.overflow = m.overflow[1:]
			}
		}
	}
	m.mu.Unlock()
	m.Short.Add(role, content)
}

func hitsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Compress 上下文自动压缩：溢出区非空时，优先调用 summarize 生成摘要
// （可为 nil 或返回错误，此时回退到本地抽取式摘要），追加进滚动摘要并清空溢出区。
// 返回本次是否发生了压缩。
func (m *Manager) Compress(summarize func(overflow string) (string, error)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.overflow) == 0 {
		return false
	}
	turns := m.overflow
	text := renderTurns(turns)
	m.overflow = nil
	// 省略量累计：这段原文将只以摘要形式存在——丢了多少轮、多少字要说出来（P2-1）
	m.omittedTurns += len(turns)
	m.omittedRunes += len([]rune(text))

	summary := ""
	if summarize != nil {
		if s, err := summarize(text); err == nil && len([]rune(strings.TrimSpace(s))) >= 4 {
			summary = strings.TrimSpace(s)
		}
	}
	if summary == "" {
		summary = extractiveSummary(text)
	}

	// 累计压缩收益：本次溢出原文与摘要的估算 token 差
	raw := 0
	for _, t := range turns {
		raw += types.EstimateTokens(t.Content)
	}
	m.savedTok += maxInt(0, raw-types.EstimateTokens(summary))

	// 追加进滚动摘要并截断；截断丢弃的部分同样计入省略量
	prevLen := len([]rune(m.summary))
	m.summary = strings.TrimSpace(m.summary + "\n" + summary)
	if r := []rune(m.summary); len(r) > summaryCap {
		m.summary = string(r[len(r)-summaryCap:])
		m.omittedRunes += len(r) - summaryCap
	} else {
		m.omittedRunes += maxInt(0, prevLen+len([]rune(summary))-len([]rune(m.summary)))
	}
	m.saveContextLocked()
	return true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Summary 返回当前滚动摘要（供规划器注入早期上下文）。
func (m *Manager) Summary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.summary
}

// ContextStats 上下文状态统计（设置页展示与 token 估算用）。
type ContextStats struct {
	ShortTurns   int    `json:"short_turns"`   // 短期窗口内轮数
	ShortCap     int    `json:"short_cap"`     // 窗口容量
	FillPct      int    `json:"fill_pct"`      // 窗口占用率 0~100（见 fillPct）
	Overflow     int    `json:"overflow"`      // 待压缩溢出轮数
	SummaryRunes int    `json:"summary_chars"` // 滚动摘要长度（rune）
	Summary      string `json:"summary"`       // 滚动摘要全文
	SavedTokens  int    `json:"saved_tokens"`  // 累计压缩节省的估算 token
}

// fillPct 窗口占用率。归这里算而不是让前端各自除一遍：
// 界面上多一处「百分比」的算法，就早晚会出现两个面板报出两个数。
// 上限钳到 100：界面拿它画水位条，一个 105% 的宽度会直接溢出那一栏。
func fillPct(turns, capacity int) int {
	if capacity <= 0 {
		return 0
	}
	if p := 100 * turns / capacity; p < 100 {
		return p
	}
	return 100
}

// Stats 返回上下文统计快照。
func (m *Manager) Stats() ContextStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ContextStats{
		ShortTurns:   m.Short.Len(),
		ShortCap:     m.Short.cap,
		FillPct:      fillPct(m.Short.Len(), m.Short.cap),
		Overflow:     len(m.overflow),
		SummaryRunes: len([]rune(m.summary)),
		Summary:      m.summary,
		SavedTokens:  m.savedTok,
	}
}

// ClearSummary 清空滚动摘要、待压缩溢出区与钉住区（删除持久化状态）。
// 钉住区与摘要同生命周期：都属会话上下文，开始新对话时一起清。
func (m *Manager) ClearSummary() {
	m.mu.Lock()
	m.overflow = nil
	m.summary = ""
	m.savedTok = 0
	m.pinned = nil
	if m.dir != "" {
		_ = os.Remove(filepath.Join(m.dir, contextFile))
	}
	m.mu.Unlock()
}

// ResetConversation 开始新对话：清空短期对话缓冲、溢出区与滚动摘要（长期记忆保留）。
func (m *Manager) ResetConversation() {
	if m.Short != nil {
		m.Short.Reset()
	}
	m.ClearSummary()
}

// saveContextLocked 持久化压缩状态（尽力而为）。
func (m *Manager) saveContextLocked() {
	if m.dir == "" {
		return
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(contextSnapshot{
		Overflow: m.overflow, Summary: m.summary, SavedToken: m.savedTok,
		Pinned: m.pinned, OmittedTurns: m.omittedTurns, OmittedRunes: m.omittedRunes,
	})
	if err != nil {
		return
	}
	path := filepath.Join(m.dir, contextFile)
	_ = atomicfile.Write(path, data, 0o644)
}

func renderTurns(turns []Turn) string {
	var b strings.Builder
	for _, t := range turns {
		fmt.Fprintf(&b, "%s: %s\n", t.Role, t.Content)
	}
	return b.String()
}

// extractiveSummary 本地抽取式摘要：每轮取首行要点拼接（无 LLM 依赖的兜底）。
func extractiveSummary(text string) string {
	var parts []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > 80 {
			r = r[:80]
		}
		parts = append(parts, string(r))
		if len(parts) >= 8 {
			break
		}
	}
	return "历史要点：" + strings.Join(parts, " / ")
}

// Remember 写入长期记忆并立即落盘。
func (m *Manager) Remember(content string, tags []string) (string, error) {
	return m.RememberWithSource(content, "", tags)
}

// RememberWithSource 同 Remember，并记录来源（透传给 Store 的去重与入库）。
func (m *Manager) RememberWithSource(content, source string, tags []string) (string, error) {
	id, err := m.Long.RememberWithSource(content, source, tags)
	if err != nil {
		return "", err
	}
	if err := m.Long.Flush(); err != nil {
		return id, err
	}
	return id, nil
}

// Relevant 词法检索（供规划器注入上下文）。
func (m *Manager) Relevant(query string, k int) []Hit {
	return m.Long.Search(query, k)
}

// Close 落盘退出。
func (m *Manager) Close() error {
	return m.Long.Flush()
}
