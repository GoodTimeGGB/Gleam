package market

import (
	"strings"
	"testing"
)

func TestSearchMCP(t *testing.T) {
	all := SearchMCP("")
	if len(all) < 8 {
		t.Errorf("目录过小: %d", len(all))
	}
	byTag := SearchMCP("git")
	if len(byTag) == 0 || byTag[0].ID != "git" {
		t.Errorf("git 搜索 = %v", byTag)
	}
	byChinese := SearchMCP("文件")
	found := false
	for _, p := range byChinese {
		if p.ID == "filesystem" {
			found = true
		}
	}
	if !found {
		t.Error("中文关键词应命中 filesystem")
	}
	if len(SearchMCP("不存在的关键词xyz")) != 0 {
		t.Error("无关关键词应返回空")
	}
}

func TestSearchSkill(t *testing.T) {
	if len(SearchSkill("")) < 4 {
		t.Error("技能模板过少")
	}
	if len(SearchSkill("速记")) == 0 {
		t.Error("中文搜索应命中 quick-note")
	}
	for _, s := range SearchSkill("") {
		if len(s.Steps) == 0 {
			t.Errorf("模板 %s 无步骤", s.Name)
		}
		for _, st := range s.Steps {
			if st.Tool == "" {
				t.Errorf("模板 %s 步骤缺少 tool", s.Name)
			}
		}
	}
}

func TestBuildCommand(t *testing.T) {
	p := MCPPreset{
		ID: "fs", Command: "npx", BaseArgs: []string{"-y", "@mcp/server", "{path}"},
		Params: []Param{{Key: "path", Label: "目录", Required: true}},
	}
	cmd, args, err := BuildCommand(p, map[string]string{"path": "D:/data"})
	if err != nil || cmd != "npx" || strings.Join(args, " ") != "-y @mcp/server D:/data" {
		t.Fatalf("BuildCommand = %s %v %v", cmd, args, err)
	}
	if _, _, err := BuildCommand(p, map[string]string{}); err == nil {
		t.Error("缺必填参数应报错")
	}
}

func TestFindMCPAndSkill(t *testing.T) {
	if _, err := FindMCP("filesystem"); err != nil {
		t.Error("filesystem 应存在")
	}
	if _, err := FindMCP("nope"); err == nil {
		t.Error("未知预设应报错")
	}
	if _, err := FindSkill("quick-note"); err != nil {
		t.Error("quick-note 应存在")
	}
}
