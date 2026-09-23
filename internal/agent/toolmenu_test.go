package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gleam/internal/harness/registry"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// toolSections 把「能力菜单」与「本次可用工具 schema」两段拼起来。
// 代码里把它们拆开，是为了让不随目标变化的能力菜单落进提示词的稳定段
// （见 buildSystemPrompt 的缓存布局注释）；但断言"模型最终看到什么"时，两段是一体的。
func toolSections(p *Planner, goal string) string {
	return p.toolMenuSection() + p.toolSchemaSection(goal)
}

// menuRegistry 造一批工具，名称带序号便于断言（reply 固定存在）。
func menuRegistry(n int) *registry.Registry {
	r := registry.New()
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) {
			return map[string]any{"text": args["text"]}, nil
		}})
	for i := 0; i < n; i++ {
		name := "tool" + string(rune('a'+i%26))
		if i >= 26 {
			name = "toolx" + string(rune('a'+i%26))
		}
		r.MustRegister(&funcTool{name: name, perm: types.PermissionReadOnly,
			fn: func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil }})
	}
	return r
}

// fakeTool 带描述的工具，用于关键词打分测试。
type fakeTool struct {
	name string
	desc string
}

func (f *fakeTool) Name() string                                         { return f.name }
func (f *fakeTool) Description() string                                  { return f.desc }
func (f *fakeTool) Schema() map[string]any                               { return map[string]any{"type": "object"} }
func (f *fakeTool) Permission() types.Permission                         { return types.PermissionReadOnly }
func (f *fakeTool) Execute(context.Context, map[string]any) (any, error) { return "ok", nil }

func TestToolMenu_UnderThresholdKeepsFullSchemas(t *testing.T) {
	p := &Planner{Reg: menuRegistry(4), MaxToolSchemas: 12}
	sec := toolSections(p, "随便做点什么")
	if strings.Contains(sec, "能力菜单") {
		t.Error("工具数在上限内不应切换为菜单模式")
	}
	if got := strings.Count(sec, "参数schema:"); got != 5 { // reply + 4
		t.Errorf("应全量注入 5 个 schema，实际 %d", got)
	}
}

func TestToolMenu_OverThresholdUsesMenu(t *testing.T) {
	p := &Planner{Reg: menuRegistry(20), MaxToolSchemas: 5}
	sec := toolSections(p, "随便做点什么")
	if !strings.Contains(sec, "能力菜单") {
		t.Error("工具超限时应切换为能力菜单")
	}
	if got := strings.Count(sec, "参数schema:"); got != 5 {
		t.Errorf("只应注入 5 个完整 schema，实际 %d", got)
	}
	if !strings.Contains(sec, "共 21 个工具") {
		t.Error("菜单应说明工具总数，让模型知道还有别的能力")
	}
}

func TestToolMenu_AlwaysKeepsReply(t *testing.T) {
	p := &Planner{Reg: menuRegistry(20), MaxToolSchemas: 5}
	sec := toolSections(p, "做点跟文件无关的事")
	// reply 是几乎所有计划的收尾步骤，必须始终可用
	if !strings.Contains(sec, "- reply：") {
		t.Error("reply 必须始终附完整 schema")
	}
}

func TestToolMenu_NoToolLeftBehindWhenLimitLarge(t *testing.T) {
	p := &Planner{Reg: menuRegistry(20), MaxToolSchemas: 100}
	sec := toolSections(p, "x")
	if got := strings.Count(sec, "参数schema:"); got != 21 {
		t.Errorf("上限足够大时应全量注入，实际 %d", got)
	}
}

// TestToolMenu_ForceToolsAlwaysIncluded 本任务此前用过的工具必须始终带上 schema——
// 菜单模式有漏筛风险，用过的工具下一轮多半还得用，不能让它靠猜参数。
func TestToolMenu_ForceToolsAlwaysIncluded(t *testing.T) {
	r := menuRegistry(20)
	r.MustRegister(&fakeTool{name: "web.fetch", desc: "抓取网页内容"})
	r.MustRegister(&fakeTool{name: "file.read", desc: "读取文件内容"})
	p := &Planner{Reg: r, MaxToolSchemas: 5, ForceTools: []string{"web.fetch", "file.read"}}
	sec := toolSections(p, "做点完全不相关的事")
	if !strings.Contains(sec, "- web.fetch：") || !strings.Contains(sec, "- file.read：") {
		t.Error("强制保留的工具必须附完整 schema")
	}
	if !strings.Contains(sec, "本任务此前已用过") {
		t.Error("应说明这些工具为何被保留，便于模型理解")
	}
}

// TestToolMenu_ForceToolsRespectLimit 强制保留也不能突破上限以外的顺序：上限内先满足强制项。
func TestToolMenu_ForceToolsRespectLimit(t *testing.T) {
	r := menuRegistry(20)
	r.MustRegister(&fakeTool{name: "web.fetch", desc: "抓取网页内容"})
	p := &Planner{Reg: r, MaxToolSchemas: 3, ForceTools: []string{"web.fetch"}}
	sec := toolSections(p, "x")
	if got := strings.Count(sec, "参数schema:"); got != 3 {
		t.Errorf("仍应只注入 3 个 schema，实际 %d", got)
	}
	if !strings.Contains(sec, "- web.fetch：") {
		t.Error("上限紧张时也应优先满足强制项")
	}
}

// TestToolMenu_ShrinksPrompt 菜单模式要真的省下提示词体积（这才是它的意义）。
func TestToolMenu_ShrinksPrompt(t *testing.T) {
	reg := menuRegistry(20)
	full := toolSections(&Planner{Reg: reg, MaxToolSchemas: 0}, "读文件")
	menu := toolSections(&Planner{Reg: reg, MaxToolSchemas: 5}, "读文件")
	if len(menu) >= len(full) {
		t.Fatalf("菜单模式应更短：menu=%d full=%d", len(menu), len(full))
	}
	saved := 100 - len(menu)*100/len(full)
	t.Logf("提示词体积：全量 %d 字符 → 菜单 %d 字符，省下 %d%%", len(full), len(menu), saved)
	if saved < 30 {
		t.Errorf("节省幅度过小（%d%%），菜单模式的收益不明显", saved)
	}
}

func TestScoreToolNames_RelevantFirst(t *testing.T) {
	r := registry.New()
	r.MustRegister(&fakeTool{name: "file.read", desc: "读取文件内容"})
	r.MustRegister(&fakeTool{name: "file.write", desc: "写入文件内容"})
	r.MustRegister(&fakeTool{name: "web.fetch", desc: "抓取网页内容"})
	r.MustRegister(&fakeTool{name: "schedule.create", desc: "创建定时任务"})
	tools := r.List()

	names := scoreToolNames("帮我读取一下项目里的配置文件", tools, 2)
	if len(names) == 0 {
		t.Fatal("应至少打分命中一个工具")
	}
	for _, n := range names {
		if n == "schedule.create" {
			t.Errorf("定时任务不该被选中：%v", names)
		}
	}
	found := false
	for _, n := range names {
		if strings.HasPrefix(n, "file.") {
			found = true
		}
	}
	if !found {
		t.Errorf("文件类工具应优先命中，实际 %v", names)
	}
}

func TestScoreToolNames_EmptyGoalFallsBack(t *testing.T) {
	r := registry.New()
	r.MustRegister(&fakeTool{name: "file.read", desc: "读取文件"})
	if got := scoreToolNames("", r.List(), 3); len(got) != 0 {
		t.Errorf("目标为空时不做猜测，实际 %v", got)
	}
}

// TestToolMenu_HelperScreeningUsed 配了辅助模型时用它快筛，省主模型的 token。
func TestToolMenu_HelperScreeningUsed(t *testing.T) {
	helper := llm.NewMock()
	helper.Enqueue("tool_pick", `["web.fetch","file.read"]`)
	r := menuRegistry(20)
	r.MustRegister(&fakeTool{name: "web.fetch", desc: "抓取网页内容"})
	r.MustRegister(&fakeTool{name: "file.read", desc: "读取文件内容"})
	p := &Planner{Reg: r, MaxToolSchemas: 5, Helper: helper}
	sec := toolSections(p, "去网上查点资料")
	if !strings.Contains(sec, "- web.fetch：") || !strings.Contains(sec, "- file.read：") {
		t.Error("应采用辅助模型快筛出的工具")
	}
	if len(helper.Calls) != 1 {
		t.Errorf("应只做一次快筛调用，实际 %d", len(helper.Calls))
	}
}

// TestToolMenu_HelperFailureFallsBackToLocal 快筛失败时不能让规划器无工具可用。
func TestToolMenu_HelperFailureFallsBackToLocal(t *testing.T) {
	helper := llm.NewMock()
	helper.FailNext(errScreeningDown)
	p := &Planner{Reg: menuRegistry(20), MaxToolSchemas: 5, Helper: helper}
	sec := toolSections(p, "读取配置")
	if got := strings.Count(sec, "参数schema:"); got != 5 {
		t.Errorf("快筛失败应退回本地打分并补满 5 个，实际 %d", got)
	}
}

var errScreeningDown = &screeningError{}

type screeningError struct{}

func (e *screeningError) Error() string { return "辅助模型不可用" }

// TestToolMenu_SectionIsValidJSONSchemas 注入的 schema 必须是可解析的 JSON，不能拼坏。
func TestToolMenu_SectionIsValidJSONSchemas(t *testing.T) {
	p := &Planner{Reg: menuRegistry(20), MaxToolSchemas: 5}
	sec := toolSections(p, "x")
	for _, line := range strings.Split(sec, "\n") {
		i := strings.Index(line, "参数schema: ")
		if i < 0 {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[i+len("参数schema: "):])), &v); err != nil {
			t.Fatalf("schema 不是合法 JSON: %v\n%s", err, line)
		}
	}
}
