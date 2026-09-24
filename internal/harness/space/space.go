// Package space 管理「微光空间」：每个空间绑定一个本地工作文件夹，
// 对话按空间隔离分组，切换空间即切换任务工作区。
package space

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultID 是首次使用自动创建的「默认空间」固定 ID，
// 也是删除其它空间时对话回迁的落点。
const DefaultID = "default"

// ErrDefaultSpace 表示对默认空间执行了不被允许的操作（如删除）。
var ErrDefaultSpace = errors.New("默认空间不能删除")

// ErrInvalidID 空间 id 不在合法形态内。id 会被拼进文件路径，
// `..`、`\`（Windows 分隔符）、`state`（激活态保留名）等必须一刀挡掉——
// 2026-09-24 接口走查实测 `DELETE /api/spaces/..%5cmark` 曾可穿越删掉数据目录文件。
// ErrNotFound 空间不存在（与 IO/损坏错误区分，处理器据此回 404 而非谎报）。
var (
	ErrInvalidID = errors.New("非法空间 ID")
	ErrNotFound  = errors.New("空间不存在")
)

// spaceIDPattern 是存储自己生成的 id 形态：default 或 sp_+12位十六进制。
var spaceIDPattern = regexp.MustCompile(`^(default|sp_[0-9a-f]{12})$`)

// Space 一个微光空间的元数据。
type Space struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path,omitempty"` // 绑定的工作文件夹，空表示跟随全局工作区
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 运行期填充，不持久化
	ConversationCount int `json:"conversation_count,omitempty"`
}

// Store 空间存储：dataDir/spaces/{id}.json + state.json。
type Store struct {
	dir string
	mu  sync.Mutex
}

type stateFile struct {
	ActiveID string `json:"active_id"`
}

// Open 打开（必要时初始化）空间存储。
// initialWorkspace 用于首次创建默认空间时绑定当前启动工作区。
func Open(dataDir, initialWorkspace string) (*Store, error) {
	dir := filepath.Join(dataDir, "spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}

	list, err := s.listLocked()
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		now := time.Now().UTC()
		def := Space{
			ID:        DefaultID,
			Name:      "默认空间",
			Path:      strings.TrimSpace(initialWorkspace),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.writeLocked(def); err != nil {
			return nil, err
		}
		if err := s.writeStateLocked(DefaultID); err != nil {
			return nil, err
		}
	} else if _, err := s.ActiveID(); err != nil {
		// state 丢失或指向已删空间时，回落到默认空间（否则取第一个合法 id 的空间）
		fallback := DefaultID
		if _, err := os.Stat(s.pathLocked(DefaultID)); err != nil {
			fallback = ""
			for _, sp := range list {
				if spaceIDPattern.MatchString(sp.ID) {
					fallback = sp.ID
					break
				}
			}
			if fallback == "" {
				return nil, fmt.Errorf("spaces: 无合法空间可回落")
			}
		}
		_ = s.writeStateLocked(fallback)
	}
	return s, nil
}

// pathLocked 仅用于自家生成的必然合法的 id（default / newID），不做二次校验。
func (s *Store) pathLocked(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) path(id string) (string, error) {
	if !spaceIDPattern.MatchString(id) {
		return "", ErrInvalidID
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func (s *Store) statePath() string {
	return filepath.Join(s.dir, "state.json")
}

// writeAtomic tmp+rename 落盘：半截 JSON 会让空间在下次启动时凭空消失。
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) writeLocked(sp Space) error {
	sp.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(sp, "", "  ")
	if err != nil {
		return err
	}
	path, err := s.path(sp.ID)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func (s *Store) writeStateLocked(id string) error {
	data, err := json.MarshalIndent(stateFile{ActiveID: id}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.statePath(), data)
}

func (s *Store) readOne(path string) (Space, error) {
	var sp Space
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sp, ErrNotFound
		}
		return sp, err
	}
	if err := json.Unmarshal(data, &sp); err != nil {
		return sp, err
	}
	// 不校验内容归属的话，任何合法 JSON（比如 state.json）都会被当成"一个空间"读写。
	if !spaceIDPattern.MatchString(sp.ID) {
		return sp, ErrInvalidID
	}
	return sp, nil
}

func (s *Store) listLocked() ([]Space, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]Space, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == "state.json" {
			continue
		}
		sp, err := s.readOne(filepath.Join(s.dir, name))
		if err != nil || sp.ID == "" {
			continue
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].ID == DefaultID) != (out[j].ID == DefaultID) {
			return out[i].ID == DefaultID // 默认空间永远在最前
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// List 返回全部空间（默认空间置顶，其余按最近更新倒序）。
func (s *Store) List() ([]Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// Get 读取单个空间。
func (s *Store) Get(id string) (Space, error) {
	if strings.TrimSpace(id) == "" {
		id = DefaultID
	}
	path, err := s.path(id)
	if err != nil {
		return Space{}, err
	}
	return s.readOne(path)
}

// ActiveID 返回当前激活空间 ID。
func (s *Store) ActiveID() (string, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		return "", err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil || strings.TrimSpace(st.ActiveID) == "" {
		return "", fmt.Errorf("active space 未设置")
	}
	if _, err := s.path(st.ActiveID); err != nil { // state 内容不可信，先验形再 Stat
		return "", fmt.Errorf("active space 不存在")
	}
	if _, err := os.Stat(s.pathLocked(st.ActiveID)); err != nil {
		return "", fmt.Errorf("active space 不存在")
	}
	return st.ActiveID, nil
}

// SetActive 切换当前激活空间。
func (s *Store) SetActive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("空间不存在: %w", ErrNotFound)
	}
	return s.writeStateLocked(id)
}

// Create 新建空间。name 为空时用工作文件夹名或占位名。
func (s *Store) Create(name, path string) (Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := newID()
	if err != nil {
		return Space{}, err
	}
	now := time.Now().UTC()
	sp := Space{
		ID:        id,
		Name:      strings.TrimSpace(name),
		Path:      strings.TrimSpace(path),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if sp.Name == "" {
		if sp.Path != "" {
			sp.Name = filepath.Base(sp.Path)
		} else {
			sp.Name = "未命名空间"
		}
	}
	if err := s.writeLocked(sp); err != nil {
		return Space{}, err
	}
	return sp, nil
}

// Rename 修改空间名称。
func (s *Store) Rename(id, name string) (Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(id)
	if err != nil {
		return Space{}, err
	}
	sp, err := s.readOne(path)
	if err != nil {
		return Space{}, err
	}
	if strings.TrimSpace(name) == "" {
		return Space{}, fmt.Errorf("名称不能为空")
	}
	sp.Name = strings.TrimSpace(name)
	if err := s.writeLocked(sp); err != nil {
		return Space{}, err
	}
	return sp, nil
}

// SetPath 更新空间绑定的工作文件夹。
func (s *Store) SetPath(id, path string) (Space, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.path(id)
	if err != nil {
		return Space{}, err
	}
	sp, err := s.readOne(p)
	if err != nil {
		return Space{}, err
	}
	sp.Path = strings.TrimSpace(path)
	if err := s.writeLocked(sp); err != nil {
		return Space{}, err
	}
	return sp, nil
}

// Delete 删除非默认空间。默认空间不可删除。
func (s *Store) Delete(id string) error {
	if id == DefaultID {
		return ErrDefaultSpace
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if _, err := s.readOne(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	// 若删的是当前激活空间，回落默认空间
	if active, err := s.ActiveID(); err == nil && active == id {
		if _, err := os.Stat(s.pathLocked(DefaultID)); err == nil {
			_ = s.writeStateLocked(DefaultID)
		}
	}
	return nil
}

func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sp_" + hex.EncodeToString(b[:]), nil
}
