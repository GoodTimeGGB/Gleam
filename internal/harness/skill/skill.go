// Package skill 实现技能系统：
// 技能 = 一组工具调用步骤 + 参数占位符，以 YAML 存储在数据目录 skills/ 下；
// 支持"执行 → 固化 → 一键复用"闭环与版本化统计（运行次数/成功率）。
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gleam/internal/config"
	"gleam/pkg/types"
)

// Skill 技能定义。
type Skill struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Version     int          `json:"version"`
	Params      []string     `json:"params,omitempty"` // 声明的参数名（步骤中用 {{name}} 引用）
	Steps       []types.Step `json:"steps"`
	Runs        int          `json:"runs"`
	Successes   int          `json:"successes"`
	LastUsed    *time.Time   `json:"last_used,omitempty"`
}

// Store 技能库。
type Store struct {
	mu  sync.Mutex
	dir string
}

// Open 打开技能库（目录不存在则创建）。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9_\-\p{Han}]{1,64}$`)

// Validate 校验技能定义。
func (s *Skill) Validate() error {
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("技能名 %q 非法（仅限字母数字下划线连字符与中文，≤64 字符）", s.Name)
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("技能至少需要一个步骤")
	}
	ids := map[string]bool{}
	for i, st := range s.Steps {
		if st.Tool == "" {
			return fmt.Errorf("步骤 %d 缺少 tool", i+1)
		}
		id := st.ID
		if id == "" {
			id = fmt.Sprintf("s%d", i+1)
		}
		if ids[id] {
			return fmt.Errorf("步骤 ID 重复: %s", id)
		}
		ids[id] = true
	}
	return nil
}

// Save 保存技能；已存在则版本号 +1（版本化）。
func (st *Store) Save(s Skill) (int, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	existing, err := st.loadLocked(s.Name)
	if err == nil && existing != nil {
		s.Version = existing.Version + 1
		s.Runs = existing.Runs
		s.Successes = existing.Successes
		s.LastUsed = existing.LastUsed
	} else {
		s.Version = 1
	}
	data, err := marshalSkill(s)
	if err != nil {
		return 0, err
	}
	path := st.pathFor(s.Name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return s.Version, nil
}

// Get 读取技能。
func (st *Store) Get(name string) (*Skill, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.loadLocked(name)
}

func (st *Store) loadLocked(name string) (*Skill, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("技能名 %q 非法", name)
	}
	data, err := os.ReadFile(st.pathFor(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("技能 %q 不存在", name)
		}
		return nil, err
	}
	return unmarshalSkill(data)
}

// Delete 删除技能。
func (st *Store) Delete(name string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !nameRe.MatchString(name) {
		return fmt.Errorf("技能名 %q 非法", name)
	}
	path := st.pathFor(name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("技能 %q 不存在", name)
	}
	return os.Remove(path)
}

// List 列出全部技能。
func (st *Store) List() []Skill {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries, _ := os.ReadDir(st.dir)
	out := make([]Skill, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(st.dir, e.Name()))
		if err != nil {
			continue
		}
		if s, err := unmarshalSkill(data); err == nil {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RecordRun 记录一次运行结果（成功/失败统计）。
func (st *Store) RecordRun(name string, ok bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, err := st.loadLocked(name)
	if err != nil {
		return
	}
	s.Runs++
	if ok {
		s.Successes++
	}
	now := time.Now()
	s.LastUsed = &now
	// 仅更新统计字段，版本号保持不变（版本只在 Save 时递增）
	data, err := marshalSkill(*s)
	if err != nil {
		return
	}
	_ = os.WriteFile(st.pathFor(name), data, 0o644)
}

// ListSummaries 返回摘要（供 std 工具适配）。
func (st *Store) ListSummaries() []struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
	Runs        int    `json:"runs"`
	Successes   int    `json:"successes"`
} {
	list := st.List()
	out := make([]struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     int    `json:"version"`
		Runs        int    `json:"runs"`
		Successes   int    `json:"successes"`
	}, 0, len(list))
	for _, s := range list {
		out = append(out, struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     int    `json:"version"`
			Runs        int    `json:"runs"`
			Successes   int    `json:"successes"`
		}{s.Name, s.Description, s.Version, s.Runs, s.Successes})
	}
	return out
}

// GetParams 返回技能声明的参数名。
func (st *Store) GetParams(name string) ([]string, error) {
	s, err := st.Get(name)
	if err != nil {
		return nil, err
	}
	return s.Params, nil
}

func (st *Store) pathFor(name string) string {
	return filepath.Join(st.dir, name+".yaml")
}

// ---------- YAML 序列化（复用自研 YAML 子集） ----------

func marshalSkill(s Skill) ([]byte, error) {
	root := config.NewYMap()
	root.Set("name", s.Name)
	root.Set("description", s.Description)
	root.Set("version", s.Version)
	if len(s.Params) > 0 {
		params := make([]any, 0, len(s.Params))
		for _, p := range s.Params {
			params = append(params, p)
		}
		root.Set("params", params)
	}
	steps := make([]any, 0, len(s.Steps))
	for _, stp := range s.Steps {
		sm := config.NewYMap()
		sm.Set("id", stp.ID)
		sm.Set("tool", stp.Tool)
		sm.Set("description", stp.Description)
		if len(stp.Args) > 0 {
			sm.Set("args", toYValue(stp.Args))
		}
		if len(stp.DependsOn) > 0 {
			deps := make([]any, 0, len(stp.DependsOn))
			for _, d := range stp.DependsOn {
				deps = append(deps, d)
			}
			sm.Set("depends_on", deps)
		}
		steps = append(steps, sm)
	}
	root.Set("steps", steps)
	root.Set("runs", s.Runs)
	root.Set("successes", s.Successes)
	if s.LastUsed != nil {
		root.Set("last_used", s.LastUsed.Format(time.RFC3339))
	}
	return config.MarshalYAML(root)
}

// toYValue 递归转换任意 JSON 值为 YAML 可序列化结构。
func toYValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := config.NewYMap()
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			m.Set(k, toYValue(t[k]))
		}
		return m
	case []any:
		arr := make([]any, 0, len(t))
		for _, item := range t {
			arr = append(arr, toYValue(item))
		}
		return arr
	default:
		return v
	}
}

func unmarshalSkill(data []byte) (*Skill, error) {
	m, err := config.ParseYAML(string(data))
	if err != nil {
		return nil, fmt.Errorf("解析技能文件失败: %w", err)
	}
	s := &Skill{}
	s.Name, _ = m["name"].(string)
	s.Description, _ = m["description"].(string)
	s.Version = asInt(m["version"])
	s.Runs = asInt(m["runs"])
	s.Successes = asInt(m["successes"])
	if v, ok := m["last_used"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			s.LastUsed = &t
		}
	}
	if arr, ok := m["params"].([]any); ok {
		for _, p := range arr {
			if str, ok := p.(string); ok {
				s.Params = append(s.Params, str)
			}
		}
	}
	if arr, ok := m["steps"].([]any); ok {
		for i, item := range arr {
			sm, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("步骤 %d 格式错误", i+1)
			}
			stp := types.Step{
				ID:          asStr(sm["id"]),
				Tool:        asStr(sm["tool"]),
				Description: asStr(sm["description"]),
			}
			if stp.ID == "" {
				stp.ID = fmt.Sprintf("s%d", i+1)
			}
			if args, ok := sm["args"].(map[string]any); ok {
				stp.Args = args
			}
			if deps, ok := sm["depends_on"].([]any); ok {
				for _, d := range deps {
					if ds, ok := d.(string); ok {
						stp.DependsOn = append(stp.DependsOn, ds)
					}
				}
			}
			s.Steps = append(s.Steps, stp)
		}
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func asStr(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	}
	return 0
}

// SubstituteParams 将步骤参数中的 {{name}} 占位符替换为实际参数值。
// 返回新 map，不改原参数。
func SubstituteParams(args map[string]any, params map[string]string) (map[string]any, error) {
	if len(args) == 0 {
		return args, nil
	}
	out := make(map[string]any, len(args))
	var missing []string
	for k, v := range args {
		out[k] = substValue(v, params, &missing)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("缺少技能参数: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

var phRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_\-]+)\s*\}\}`)

func substValue(v any, params map[string]string, missing *[]string) any {
	switch t := v.(type) {
	case string:
		// 整串恰为一个占位符 → 原样替换（保留类型）
		if m := phRe.FindStringSubmatch(t); m != nil && m[0] == t {
			if val, ok := params[m[1]]; ok {
				return val
			}
			*missing = append(*missing, m[1])
			return t
		}
		return phRe.ReplaceAllStringFunc(t, func(ph string) string {
			m := phRe.FindStringSubmatch(ph)
			if val, ok := params[m[1]]; ok {
				return val
			}
			*missing = append(*missing, m[1])
			return ph
		})
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = substValue(item, params, missing)
		}
		return out
	case []any:
		arr := make([]any, 0, len(t))
		for _, item := range t {
			arr = append(arr, substValue(item, params, missing))
		}
		return arr
	default:
		return v
	}
}
