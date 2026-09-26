// Package market 内置市场目录：常见 MCP 服务器预设与技能模板，
// 支持关键词搜索与一键安装；全部为本地声明，安装即写配置（自研，零外部依赖）。
// 命令模板中的 {key} 占位符在安装时由用户填写的参数替换。
package market

import (
	"fmt"
	"sort"
	"strings"

	"gleam/pkg/types"
)

// Param MCP 预设的安装参数。
type Param struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPPreset 常见 MCP 服务器预设。
type MCPPreset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Desc     string   `json:"desc"`
	Command  string   `json:"command"`          // npx | uvx
	BaseArgs []string `json:"base_args"`        // 固定参数（含 {key} 占位符）
	Params   []Param  `json:"params,omitempty"` // 安装时需填写的参数
	Trust    string   `json:"trust"`            // readonly | user_approved
	Tags     []string `json:"tags,omitempty"`
}

// SkillPreset 技能模板。
type SkillPreset struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Params      []string     `json:"params,omitempty"`
	Steps       []types.Step `json:"steps"`
	Tags        []string     `json:"tags,omitempty"`
}

// MCPCatalog 常见 MCP 服务器目录（官方与社区广泛使用的服务器；命令依赖本机 npx/uvx）。
var MCPCatalog = []MCPPreset{
	{
		ID: "filesystem", Name: "Filesystem 文件系统", Desc: "让 Gleam 读写指定目录之外的文件（可授予额外工作区）",
		Command: "npx", BaseArgs: []string{"-y", "@modelcontextprotocol/server-filesystem", "{path}"},
		Params: []Param{{Key: "path", Label: "允许访问的根目录", Placeholder: "D:/data", Required: true}},
		Trust:  "user_approved", Tags: []string{"文件", "官方", "filesystem"},
	},
	{
		ID: "fetch", Name: "Fetch 网页抓取", Desc: "抓取网页并转为 Markdown（比内置 web.fetch 更强的站点兼容性）",
		Command: "uvx", BaseArgs: []string{"mcp-server-fetch"},
		Trust: "readonly", Tags: []string{"网页", "官方", "fetch"},
	},
	{
		ID: "memory", Name: "Memory 知识图谱记忆", Desc: "基于知识图谱的长期记忆存储，与内置记忆互补",
		Command: "npx", BaseArgs: []string{"-y", "@modelcontextprotocol/server-memory"},
		Trust: "user_approved", Tags: []string{"记忆", "官方", "memory"},
	},
	{
		ID: "sequential-thinking", Name: "Sequential Thinking 序贯思考", Desc: "为复杂推理提供动态反思与分支思考工具",
		Command: "npx", BaseArgs: []string{"-y", "@modelcontextprotocol/server-sequential-thinking"},
		Trust: "readonly", Tags: []string{"推理", "官方", "thinking"},
	},
	{
		ID: "everything", Name: "Everything 综合演示", Desc: "官方综合演示服务器：回声、加法、资源订阅等，适合体验 MCP",
		Command: "npx", BaseArgs: []string{"-y", "@modelcontextprotocol/server-everything"},
		Trust: "readonly", Tags: []string{"演示", "官方", "everything"},
	},
	{
		ID: "puppeteer", Name: "Puppeteer 浏览器自动化", Desc: "驱动无头浏览器导航、截图与执行 JS",
		Command: "npx", BaseArgs: []string{"-y", "@modelcontextprotocol/server-puppeteer"},
		Trust: "user_approved", Tags: []string{"浏览器", "自动化", "puppeteer"},
	},
	{
		ID: "time", Name: "Time 时间时区", Desc: "获取与转换各时区时间",
		Command: "uvx", BaseArgs: []string{"mcp-server-time"},
		Trust: "readonly", Tags: []string{"时间", "官方", "time"},
	},
	{
		ID: "git", Name: "Git 版本管理", Desc: "读写 Git 仓库：状态、日志、diff、提交等",
		Command: "uvx", BaseArgs: []string{"mcp-server-git", "--repository", "{path}"},
		Params: []Param{{Key: "path", Label: "仓库路径", Placeholder: "D:/PersonalProject/Gleam", Required: true}},
		Trust:  "user_approved", Tags: []string{"git", "版本管理", "开发"},
	},
	{
		ID: "sqlite", Name: "SQLite 数据库", Desc: "查询与更新本地 SQLite 数据库",
		Command: "uvx", BaseArgs: []string{"mcp-server-sqlite", "--db-path", "{db}"},
		Params: []Param{{Key: "db", Label: "数据库文件路径", Placeholder: "D:/data/app.db", Required: true}},
		Trust:  "user_approved", Tags: []string{"数据库", "sqlite", "开发"},
	},
}

// SkillCatalog 技能模板目录（基于内置工具，安装即可运行/继续改造）。
var SkillCatalog = []SkillPreset{
	{
		Name: "quick-note", Description: "速记入库：把一句话存进长期记忆，随时按关键词找回",
		Params: []string{"text"},
		Steps: []types.Step{
			{ID: "s1", Tool: "memory.save", Args: map[string]any{"content": "{{text}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "已记住：{{text}}"}},
		},
		Tags: []string{"记忆", "速记"},
	},
	{
		Name: "dir-snapshot", Description: "目录快照：列出目标目录内容并把快照动作沉淀到记忆",
		Params: []string{"dir"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.list", Args: map[string]any{"path": "{{dir}}"}},
			{ID: "s2", Tool: "memory.save", Args: map[string]any{"content": "已对目录 {{dir}} 生成文件清单快照"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "目录 {{dir}} 快照完成，已写入记忆"}},
		},
		Tags: []string{"文件", "目录"},
	},
	{
		Name: "web-bookmark", Description: "网页收藏：抓取网页内容并把收藏记录存入长期记忆",
		Params: []string{"url"},
		Steps: []types.Step{
			{ID: "s1", Tool: "web.fetch", Args: map[string]any{"url": "{{url}}"}},
			{ID: "s2", Tool: "memory.save", Args: map[string]any{"content": "收藏网页 {{url}}，正文已抓取"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "已收藏 {{url}} 并记录到记忆"}},
		},
		Tags: []string{"网页", "收藏"},
	},
	{
		Name: "daily-note", Description: "工作日志：按日期把日志写入工作区 日志/ 目录",
		Params: []string{"date", "content"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.write", Args: map[string]any{"path": "日志/{{date}}.md", "content": "{{content}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "日志已写入 日志/{{date}}.md"}},
		},
		Tags: []string{"日志", "写作"},
	},
	{
		Name: "project-scaffold", Description: "项目脚手架：一键生成项目目录的 README 与待办清单",
		Params: []string{"name"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.write", Args: map[string]any{"path": "{{name}}/README.md", "content": "# {{name}}\n\n由 Gleam 生成的项目脚手架。"}},
			{ID: "s2", Tool: "file.write", Args: map[string]any{"path": "{{name}}/todo.txt", "content": "TODO\n- 定义目标\n- 拆解任务\n"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "项目 {{name}} 已生成：README.md + todo.txt"}},
		},
		Tags: []string{"脚手架", "项目"},
	},
}

// SearchMCP 关键词搜索 MCP 目录（命中 id/名称/描述/标签；空关键词返回全部）。
func SearchMCP(q string) []MCPPreset {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return append([]MCPPreset(nil), MCPCatalog...)
	}
	var out []MCPPreset
	for _, p := range MCPCatalog {
		if matchKeyword(q, p.ID, p.Name, p.Desc, strings.Join(p.Tags, " ")) {
			out = append(out, p)
		}
	}
	return out
}

// SearchSkill 关键词搜索技能模板。
func SearchSkill(q string) []SkillPreset {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return append([]SkillPreset(nil), SkillCatalog...)
	}
	var out []SkillPreset
	for _, s := range SkillCatalog {
		if matchKeyword(q, s.Name, s.Description, strings.Join(s.Tags, " ")) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FindMCP 按 ID 取 MCP 预设。
func FindMCP(id string) (MCPPreset, error) {
	for _, p := range MCPCatalog {
		if p.ID == id {
			return p, nil
		}
	}
	return MCPPreset{}, fmt.Errorf("市场目录中不存在 MCP 预设 %q", id)
}

// FindSkill 按名取技能模板。
func FindSkill(name string) (SkillPreset, error) {
	for _, s := range SkillCatalog {
		if s.Name == name {
			return s, nil
		}
	}
	return SkillPreset{}, fmt.Errorf("市场目录中不存在技能模板 %q", name)
}

// BuildCommand 用参数替换命令模板中的 {key} 占位符，返回最终 args。
func BuildCommand(p MCPPreset, values map[string]string) (string, []string, error) {
	args := make([]string, 0, len(p.BaseArgs))
	for _, a := range p.BaseArgs {
		replaced := a
		for k, v := range values {
			replaced = strings.ReplaceAll(replaced, "{"+k+"}", v)
		}
		if strings.Contains(replaced, "{") && strings.Contains(replaced, "}") {
			// 仍有未填参数：确认是否为必填
			for _, pm := range p.Params {
				if strings.Contains(a, "{"+pm.Key+"}") && pm.Required && strings.TrimSpace(values[pm.Key]) == "" {
					return "", nil, fmt.Errorf("缺少必填参数 %q（%s）", pm.Key, pm.Label)
				}
			}
			return "", nil, fmt.Errorf("参数不完整: %s", replaced)
		}
		args = append(args, replaced)
	}
	for _, pm := range p.Params {
		if pm.Required && strings.TrimSpace(values[pm.Key]) == "" {
			return "", nil, fmt.Errorf("缺少必填参数 %q（%s）", pm.Key, pm.Label)
		}
	}
	return p.Command, args, nil
}

func matchKeyword(q string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}
