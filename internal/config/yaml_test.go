package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseYAML_ConfigSample(t *testing.T) {
	src := `
llm:
  provider: glm
  base_url: https://open.bigmodel.cn/api/paas/v4
  model: "glm-5.3-flash"
  temperature: 0.3
  max_tokens: 4096

safety:
  mode: auto
  trusted_tools:
    - file.list
    - file.read
  trusted_paths: []

mcp:
  - name: fs
    command: npx
    args: ["-y", "@server/fs", "D:/data"]
    trust: user_approved
    enabled: true
  - name: calc
    command: python
    args: ["calc.py"]

persona:
  style: efficient
`
	m, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	llm, ok := sub(m, "llm")
	if !ok {
		t.Fatal("缺少 llm 节")
	}
	if llm["provider"] != "glm" {
		t.Errorf("provider = %v", llm["provider"])
	}
	if llm["model"] != "glm-5.3-flash" {
		t.Errorf("model = %v", llm["model"])
	}
	if llm["temperature"] != 0.3 {
		t.Errorf("temperature = %v", llm["temperature"])
	}
	if llm["max_tokens"] != int64(4096) {
		t.Errorf("max_tokens = %v (%T)", llm["max_tokens"], llm["max_tokens"])
	}
	safety, _ := sub(m, "safety")
	tools, ok := safety["trusted_tools"].([]any)
	if !ok || len(tools) != 2 || tools[0] != "file.list" {
		t.Errorf("trusted_tools = %v", safety["trusted_tools"])
	}
	if arr, ok := safety["trusted_paths"].([]any); !ok || len(arr) != 0 {
		t.Errorf("trusted_paths = %v", safety["trusted_paths"])
	}
	servers, ok := m["mcp"].([]any)
	if !ok || len(servers) != 2 {
		t.Fatalf("mcp = %v", m["mcp"])
	}
	first := servers[0].(map[string]any)
	if first["name"] != "fs" || first["command"] != "npx" {
		t.Errorf("mcp[0] = %v", first)
	}
	if args, ok := first["args"].([]any); !ok || len(args) != 3 || args[2] != "D:/data" {
		t.Errorf("mcp[0].args = %v", first["args"])
	}
}

func TestParseYAML_SkillLike(t *testing.T) {
	src := `
name: archive-desktop
description: 整理桌面文件
version: 3
params:
  - src_dir
  - dst_dir
steps:
  - id: s1
    tool: file.list
    description: 列出目录
    args:
      path: "{{src_dir}}"
  - id: s2
    tool: file.move
    depends_on: [s1]
    args:
      src: "{{src_dir}}/a.txt"
      dst: "{{dst_dir}}/a.txt"
runs: 12
successes: 10
`
	m, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m["name"] != "archive-desktop" || m["version"] != int64(3) {
		t.Fatalf("标量字段错误: %v", m)
	}
	params := m["params"].([]any)
	if len(params) != 2 || params[0] != "src_dir" {
		t.Errorf("params = %v", params)
	}
	steps := m["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("steps = %v", steps)
	}
	s1 := steps[0].(map[string]any)
	if s1["tool"] != "file.list" {
		t.Errorf("s1.tool = %v", s1["tool"])
	}
	args := s1["args"].(map[string]any)
	if args["path"] != "{{src_dir}}" {
		t.Errorf("s1.args.path = %v", args["path"])
	}
	s2 := steps[1].(map[string]any)
	deps := s2["depends_on"].([]any)
	if len(deps) != 1 || deps[0] != "s1" {
		t.Errorf("s2.depends_on = %v", s2["depends_on"])
	}
}

func TestParseYAML_Errors(t *testing.T) {
	cases := []struct{ name, src string }{
		{"tab缩进", "a:\n\tb: 1\n"},
		{"根为列表", "- a\n- b\n"},
		{"未闭合引号", `a: "abc`},
		{"意外的缩进", "a: 1\n  b: 2\n"},
	}
	for _, c := range cases {
		if _, err := ParseYAML(c.src); err == nil {
			t.Errorf("%s: 期望报错但成功了", c.name)
		}
	}
}

func TestParseYAML_Scalars(t *testing.T) {
	m, err := ParseYAML("a: true\nb: null\nc: 42\nd: -3.5\ne: '单引号 ''x'''\nf: \"转义\\n字符\"\ng: 带空格的值 # 注释\nh: []\ni: [1, two, \"3\"]")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if m["a"] != true || m["b"] != nil {
		t.Errorf("bool/null: %v %v", m["a"], m["b"])
	}
	if m["c"] != int64(42) || m["d"] != -3.5 {
		t.Errorf("数字: %v %v", m["c"], m["d"])
	}
	if m["e"] != "单引号 'x'" {
		t.Errorf("单引号: %v", m["e"])
	}
	if m["f"] != "转义\n字符" {
		t.Errorf("双引号转义: %q", m["f"])
	}
	if m["g"] != "带空格的值" {
		t.Errorf("注释剥离: %v", m["g"])
	}
	if arr := m["h"]; arr == nil {
		t.Errorf("空列表应为 [] 而非 nil: %v", arr)
	}
	if arr := m["i"].([]any); len(arr) != 3 || arr[1] != "two" {
		t.Errorf("内联列表: %v", m["i"])
	}
}

func TestMarshalYAML_RoundTrip(t *testing.T) {
	root := NewYMap()
	root.Set("name", "demo skill")
	root.Set("version", 2)
	root.Set("params", []any{"p1", "p2"})
	steps := []any{}
	sm := NewYMap()
	sm.Set("id", "s1")
	sm.Set("tool", "file.write")
	smArgs := NewYMap()
	smArgs.Set("path", "{{p1}}/out.txt")
	smArgs.Set("content", "line1\nline2")
	smArgs.Set("n", 3)
	sm.Set("args", smArgs)
	steps = append(steps, sm)
	root.Set("steps", steps)

	data, err := MarshalYAML(root)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	m, err := ParseYAML(string(data))
	if err != nil {
		t.Fatalf("回读解析失败: %v\n---\n%s", err, data)
	}
	if m["name"] != "demo skill" || m["version"] != int64(2) {
		t.Errorf("标量不匹配: %v", m)
	}
	s1 := m["steps"].([]any)[0].(map[string]any)
	args := s1["args"].(map[string]any)
	if args["path"] != "{{p1}}/out.txt" {
		t.Errorf("path = %v", args["path"])
	}
	if args["content"] != "line1\nline2" {
		t.Errorf("content = %q", args["content"])
	}
	if args["n"] != int64(3) {
		t.Errorf("n = %v", args["n"])
	}
}

func TestParseYAML_CommentEdgeCases(t *testing.T) {
	src := "" +
		"a:                        # 值仅为注释，后面是嵌套列表\n" +
		"  - x # 行内注释\n" +
		"  - y\n" +
		"b: []       # 空列表带注释\n" +
		"c: 5  # 数字带尾注释\n" +
		"d: plain#非注释井号\n" +
		"e: \"引号内 # 保留\"  # 尾注释\n"
	m, err := ParseYAML(src)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if arr, ok := m["a"].([]any); !ok || len(arr) != 2 || arr[0] != "x" || arr[1] != "y" {
		t.Errorf("a = %v", m["a"])
	}
	if arr, ok := m["b"].([]any); !ok || len(arr) != 0 {
		t.Errorf("b = %v", m["b"])
	}
	if m["c"] != int64(5) {
		t.Errorf("c = %v (%T)", m["c"], m["c"])
	}
	if m["d"] != "plain#非注释井号" {
		t.Errorf("d = %v", m["d"])
	}
	if m["e"] != "引号内 # 保留" {
		t.Errorf("e = %q", m["e"])
	}
}

// TestLoad_ProjectConfig 防回归：仓库自带的示例配置必须始终可被解析。
func TestLoad_ProjectConfig(t *testing.T) {
	if _, err := os.Stat("../../configs/config.yaml"); err != nil {
		t.Skip("示例配置不存在")
	}
	cfg, err := Load("../../configs/config.yaml")
	if err != nil {
		t.Fatalf("示例配置解析失败: %v", err)
	}
	if len(cfg.Safety.TrustedTools) == 0 {
		t.Error("trusted_tools 应解析为列表")
	}
	if cfg.LLM.Provider != "glm" || cfg.Persona.Style != "efficient" {
		t.Errorf("字段错误: provider=%s style=%s", cfg.LLM.Provider, cfg.Persona.Style)
	}
}

func TestLoad_ConfigFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
llm:
  provider: glm
  model: test-model
  api_key: from-file
safety:
  mode: interactive
persona:
  style: gentle
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEAM_API_KEY", "from-env")
	t.Setenv("GLEAM_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("GLEAM_WORKSPACE", dir)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.LLM.Model != "test-model" {
		t.Errorf("model = %v", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "from-env" {
		t.Errorf("APIKey 环境变量应覆盖文件: %v", cfg.LLM.APIKey)
	}
	if cfg.Safety.Mode != "interactive" {
		t.Errorf("mode = %v", cfg.Safety.Mode)
	}
	if cfg.Persona.Style != "gentle" {
		t.Errorf("style = %v", cfg.Persona.Style)
	}
	if cfg.DataDir != filepath.Join(dir, "data") {
		t.Errorf("data_dir = %v", cfg.DataDir)
	}
	if !strings.HasSuffix(cfg.Workspace, dir[len(filepath.VolumeName(dir)):]) && cfg.Workspace != dir {
		t.Errorf("workspace = %v", cfg.Workspace)
	}
	if cfg.Memory.ShortTermCap != 20 {
		t.Errorf("默认值丢失: %v", cfg.Memory.ShortTermCap)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("期望报错")
	}
}
