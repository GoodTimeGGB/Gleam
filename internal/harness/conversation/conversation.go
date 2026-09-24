// Package conversation 提供多会话持久化：每个会话一个 JSON 文件，
// 支撑左侧常驻会话列表、历史回看与继续对话。
package conversation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gleam/pkg/types"
)

// ErrInvalidID 会话 id 形态非法（含 `/\`、非十六进制字符等，会被拼进文件路径）。
// ErrNotFound 会话文件不存在。二者让处理器区分 400/404，而非把 OS 报错原样回吐。
var (
	ErrInvalidID = errors.New("非法会话 ID")
	ErrNotFound  = errors.New("会话不存在")
)

// Message 一条会话消息（用户输入或助手回复）。
type Message struct {
	Role      string    `json:"role"` // user | assistant
	Content   string    `json:"content"`
	TaskID    string    `json:"task_id,omitempty"`
	Mode      string    `json:"mode,omitempty"` // chat | work | code
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Conversation 一次完整会话（多轮对话）。
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	SpaceID   string    `json:"space_id,omitempty"` // 所属微光空间；空视为默认空间
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Summary 会话列表项（不含完整消息，减少列表开销）。
type Summary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	SpaceID   string    `json:"space_id,omitempty"`
	Preview   string    `json:"preview"`
	Count     int       `json:"count"`
	UpdatedAt time.Time `json:"updated_at"`
}

const maxTitleRunes = 24

// DefaultSpaceID 默认微光空间 ID，须与 space.DefaultID 保持一致。
const DefaultSpaceID = "default"

// Store 会话存储（dataDir/conversations/<id>.json）。
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open 打开（必要时创建）会话目录，并把历史无空间归属的会话迁入默认空间。
func Open(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录失败: %w", err)
	}
	s := &Store{dir: dir}
	if err := s.migrateLegacy(); err != nil {
		return nil, fmt.Errorf("迁移历史会话失败: %w", err)
	}
	return s, nil
}

// migrateLegacy 为缺少 space_id 的存量会话补上默认空间归属（只打标记，不移动文件）。
func (s *Store) migrateLegacy() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var c Conversation
		if err := json.Unmarshal(data, &c); err != nil || strings.TrimSpace(c.SpaceID) != "" {
			continue
		}
		c.SpaceID = DefaultSpaceID
		out, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			continue
		}
		_ = os.WriteFile(path, out, 0o644)
	}
	return nil
}

// path 返回会话文件路径（id 仅允许十六进制，防目录穿越）。
func (s *Store) path(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) || id != strings.Map(safeRune, id) {
		return "", ErrInvalidID
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func safeRune(r rune) rune {
	if (r >= 'a' && r <= 'f') || (r >= '0' && r <= '9') {
		return r
	}
	return -1
}

// Create 新建一个属于指定空间的空会话并落盘。spaceID 为空时归属默认空间。
func (s *Store) Create(spaceIDs ...string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	spaceID := DefaultSpaceID
	if len(spaceIDs) > 0 && strings.TrimSpace(spaceIDs[0]) != "" {
		spaceID = strings.TrimSpace(spaceIDs[0])
	}
	now := time.Now()
	c := &Conversation{ID: types.NewID(), Title: "新对话", SpaceID: spaceID, CreatedAt: now, UpdatedAt: now}
	if err := s.writeLocked(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Get 读取单个会话。
func (s *Store) Get(id string) (*Conversation, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(p)
}

// List 返回会话摘要，按最近更新倒序。
// 可选 spaceID：非空时仅返回该空间的会话；为空时返回全部空间的会话。
func (s *Store) List(spaceIDs ...string) ([]Summary, error) {
	filter := ""
	if len(spaceIDs) > 0 {
		filter = strings.TrimSpace(spaceIDs[0])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("读取会话目录失败: %w", err)
	}
	var out []Summary
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		c, err := s.readLocked(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue // 损坏文件跳过，不影响列表
		}
		sid := c.SpaceID
		if sid == "" {
			sid = DefaultSpaceID
		}
		if filter != "" && sid != filter {
			continue
		}
		summary := summarize(c)
		summary.SpaceID = sid
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// CountBySpace 统计每个空间下的会话数量（space_id -> count）。
func (s *Store) CountBySpace() (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		c, err := s.readLocked(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		sid := c.SpaceID
		if sid == "" {
			sid = DefaultSpaceID
		}
		counts[sid]++
	}
	return counts, nil
}

// ReassignSpace 把某空间下的全部会话迁移到另一个空间（用于删除空间时回迁到默认空间）。
func (s *Store) ReassignSpace(fromID, toID string) (int, error) {
	if strings.TrimSpace(toID) == "" {
		toID = DefaultSpaceID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(s.dir, e.Name())
		c, err := s.readLocked(p)
		if err != nil {
			continue
		}
		sid := c.SpaceID
		if sid == "" {
			sid = DefaultSpaceID
		}
		if sid != fromID {
			continue
		}
		c.SpaceID = toID
		c.UpdatedAt = time.Now()
		if err := s.writeLocked(c); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// Append 向会话追加消息；首轮用户消息自动生成标题；返回更新后的会话。
func (s *Store) Append(id string, msg Message) (*Conversation, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.readLocked(p)
	if err != nil {
		return nil, err
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = msg.CreatedAt
	if (c.Title == "" || c.Title == "新对话") && msg.Role == "user" {
		c.Title = titleFrom(msg.Content)
	}
	if err := s.writeLocked(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Rename 重命名会话（空标题回退为默认）。
func (s *Store) Rename(id, title string) (*Conversation, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if title == "" {
		title = "未命名对话"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.readLocked(p)
	if err != nil {
		return nil, err
	}
	c.Title = truncate(title, maxTitleRunes)
	c.UpdatedAt = time.Now()
	if err := s.writeLocked(c); err != nil {
		return nil, err
	}
	return c, nil
}

// Delete 删除会话文件；不存在不报错。
func (s *Store) Delete(id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

func (s *Store) readLocked(p string) (*Conversation, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	var c Conversation
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("会话文件损坏: %w", err)
	}
	return &c, nil
}

// writeLocked 原子写入（临时文件 + rename），避免并发读到半截 JSON。
func (s *Store) writeLocked(c *Conversation) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("编码会话失败: %w", err)
	}
	tmp := filepath.Join(s.dir, c.ID+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, c.ID+".json")); err != nil {
		return fmt.Errorf("保存会话失败: %w", err)
	}
	return nil
}

func summarize(c *Conversation) Summary {
	sum := Summary{ID: c.ID, Title: c.Title, Count: len(c.Messages), UpdatedAt: c.UpdatedAt}
	if len(c.Messages) > 0 {
		last := c.Messages[len(c.Messages)-1]
		sum.Preview = truncate(strings.TrimSpace(stripNewlines(last.Content)), 40)
	}
	return sum
}

func titleFrom(content string) string {
	return truncate(strings.TrimSpace(stripNewlines(content)), maxTitleRunes)
}

func stripNewlines(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
