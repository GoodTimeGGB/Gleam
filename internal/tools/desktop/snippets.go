package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/atomicfile"
	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// SnippetsTool 管理可复用文本片段（参考 Alice 的 Snippets + 快捷键工作流）。
// 片段存储为 JSON 文件（~/.gleam/snippets.json），支持增删查与展开。
type SnippetsTool struct {
	dataDir string
}

func NewSnippets(dataDir string) *SnippetsTool {
	return &SnippetsTool{dataDir: dataDir}
}

func (t *SnippetsTool) Name() string { return "desktop.snippets" }
func (t *SnippetsTool) Description() string {
	return "管理可复用文本片段。action=list 列出全部；action=get 读取片段；action=save 保存/更新；action=delete 删除；action=expand 展开片段内容"
}
func (t *SnippetsTool) Permission() types.Permission { return types.PermissionUserApproved }
func (t *SnippetsTool) Schema() map[string]any {
	return toolutil.Schema("管理文本片段", []string{"action"}, map[string]any{
		"action":      toolutil.SchemaProp("操作：list/get/save/delete/expand", "string"),
		"name":        toolutil.SchemaProp("片段名称（save/get/delete/expand 必填）", "string"),
		"content":     toolutil.SchemaProp("片段内容（save 必填）", "string"),
		"description": toolutil.SchemaProp("片段描述（save 可选）", "string"),
	})
}
func (t *SnippetsTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	action := toolutil.Str(args, "action")
	switch action {
	case "list", "":
		return t.list()
	case "get":
		name, err := toolutil.RequireStr(args, "name")
		if err != nil {
			return nil, err
		}
		return t.get(name)
	case "save":
		name, err := toolutil.RequireStr(args, "name")
		if err != nil {
			return nil, err
		}
		content, err := toolutil.RequireStr(args, "content")
		if err != nil {
			return nil, err
		}
		desc := toolutil.Str(args, "description")
		return t.save(name, content, desc)
	case "delete":
		name, err := toolutil.RequireStr(args, "name")
		if err != nil {
			return nil, err
		}
		return t.delete(name)
	case "expand":
		name, err := toolutil.RequireStr(args, "name")
		if err != nil {
			return nil, err
		}
		return t.expand(name)
	default:
		return nil, fmt.Errorf("未知 action %q", action)
	}
}

// snippet 存储结构。
type snippet struct {
	Name        string    `json:"name"`
	Content     string    `json:"content"`
	Description string    `json:"description,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

func (t *SnippetsTool) path() string {
	return filepath.Join(t.dataDir, "snippets.json")
}

func (t *SnippetsTool) load() (map[string]snippet, error) {
	data, err := os.ReadFile(t.path())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]snippet{}, nil
		}
		return nil, err
	}
	var m map[string]snippet
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (t *SnippetsTool) saveStore(m map[string]snippet) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.dataDir, 0755); err != nil {
		return err
	}
	// 原子写：整张便签表存在一个文件里，写成半截 = 全部便签一起丢
	return atomicfile.Write(t.path(), data, 0o644)
}

func (t *SnippetsTool) list() (any, error) {
	m, err := t.load()
	if err != nil {
		return nil, err
	}
	var names []string
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	result := make([]map[string]any, 0, len(names))
	for _, n := range names {
		s := m[n]
		result = append(result, map[string]any{
			"name":        s.Name,
			"description": s.Description,
			"updated":     s.Updated,
		})
	}
	return map[string]any{"snippets": result, "count": len(result)}, nil
}

func (t *SnippetsTool) get(name string) (any, error) {
	m, err := t.load()
	if err != nil {
		return nil, err
	}
	s, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("片段 %q 不存在", name)
	}
	return map[string]any{
		"name":        s.Name,
		"content":     s.Content,
		"description": s.Description,
		"created":     s.Created,
		"updated":     s.Updated,
	}, nil
}

func (t *SnippetsTool) save(name, content, desc string) (any, error) {
	m, err := t.load()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s, exists := m[name]
	if !exists {
		s.Created = now
	}
	s.Name = name
	s.Content = content
	s.Description = desc
	s.Updated = now
	m[name] = s
	if err := t.saveStore(m); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "name": name}, nil
}

func (t *SnippetsTool) delete(name string) (any, error) {
	m, err := t.load()
	if err != nil {
		return nil, err
	}
	if _, ok := m[name]; !ok {
		return nil, fmt.Errorf("片段 %q 不存在", name)
	}
	delete(m, name)
	if err := t.saveStore(m); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "deleted": name}, nil
}

// expand 展开片段：将内容中的 {{var}} 占位符替换为对应参数（如有）。
func (t *SnippetsTool) expand(name string) (any, error) {
	m, err := t.load()
	if err != nil {
		return nil, err
	}
	s, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("片段 %q 不存在", name)
	}
	content := s.Content
	// 简单展开：移除占位符标记，保留默认值
	content = strings.ReplaceAll(content, "{{date}}", time.Now().Format("2006-01-02"))
	content = strings.ReplaceAll(content, "{{time}}", time.Now().Format("15:04:05"))
	return map[string]any{
		"name":    s.Name,
		"content": content,
	}, nil
}
