package agent

import (
	"strings"
	"testing"

	"gleam/internal/harness/memory"
	"gleam/internal/harness/registry"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// bigRegistry 造一个工具数超过菜单阈值的注册表，逼规划器走"能力菜单"分支——
// 这是默认路径（内置工具就有 19 个），也是最容易把稳定段打散的分支。
func bigRegistry(n int) *registry.Registry {
	r := registry.New()
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly})
	r.MustRegister(&funcTool{name: "schedule.create", perm: types.PermissionReadOnly})
	for i := 0; i < n; i++ {
		name := "tool" + string(rune('a'+i%26))
		if i >= 26 {
			name = "toolx" + string(rune('a'+i%26))
		}
		r.MustRegister(&funcTool{name: name, perm: types.PermissionReadOnly})
	}
	return r
}

// TestPromptCache_StablePrefixSurvivesGoalChange 提示词布局的核心契约：
// 同一角色/模式/风格下，换一个目标也必须保住一大段逐字节相同的前缀。
//
// 这条不是审美问题——服务端前缀缓存只认"逐字节相同的最长公共前缀"，
// 一旦有易变内容（工作目录、摘要、历史对话）夹在稳定内容中间，
// 从它往后全部作废。这个测试就是那个约束的可执行版本。
func TestPromptCache_StablePrefixSurvivesGoalChange(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12, Role: "analyst", TaskMode: "work", Style: "efficient"}
	a := p.buildSystemPrompt("把销售数据整理成表格并汇总", "D:/ws", nil, nil, "", "")
	b := p.buildSystemPrompt("帮我写一封给客户的道歉邮件", "D:/ws", nil, nil, "", "")

	stable, _, _ := llm.SplitCacheBoundary(a, b)
	n := llm.StablePrefixLen(a, b)

	// 稳定段必须包含"大头"：输出契约（格式 + 规则 + 验收标准）与能力菜单
	for _, want := range []string{
		"## 输出格式",
		"## 规划规则",
		"## 验收标准（acceptance）",
		"## 能力菜单",
		"## 定时/周期任务",
		"## 专家角色：数据分析师",
	} {
		if !strings.Contains(stable, want) {
			t.Errorf("稳定段应覆盖 %q，实际公共前缀只有 %d 字", want, n)
		}
	}
	// 目标本身不能混进稳定段
	if strings.Contains(stable, "把销售数据整理成表格") || strings.Contains(stable, "道歉邮件") {
		t.Error("目标属于易变内容，不该出现在公共前缀里")
	}
	if n < llm.CacheFriendlyMinChars {
		t.Errorf("公共前缀只有 %d 字，低于值得缓存的量级（%d）——布局被易变内容打断了",
			n, llm.CacheFriendlyMinChars)
	}
	t.Logf("换目标后公共前缀 %d 字（提示词全长 %d / %d）", n, len([]rune(a)), len([]rune(b)))
}

// TestPromptCache_VolatileContentNeverBreaksPrefix 易变内容（工作目录、摘要、记忆、历史、反馈）
// 无论怎么变，都不得侵入稳定段——它们必须整段沉在稳定段之后。
//
// 注意这里比的是"两份易变内容不同的提示词"，而不是"有易变内容 vs 没有"：
// 后者天然会短掉末尾那行输出提醒，比出来的是提醒的长度，说明不了问题。
func TestPromptCache_VolatileContentNeverBreaksPrefix(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12, Role: "coder", TaskMode: "code", Style: "efficient"}
	recentA := []memory.Turn{{Role: "user", Content: "上次聊到执行器"}}
	recentB := []memory.Turn{{Role: "user", Content: "另一次完全不同的对话"}, {Role: "assistant", Content: "好的"}}
	a := p.buildSystemPrompt("重构执行器", "D:/ws", recentA, []memory.Hit{{Content: "偏好最小 diff"}}, "摘要甲", "")
	b := p.buildSystemPrompt("重构执行器", "E:/另一个工作区", recentB, nil, "摘要乙", "上次计划无效，请修正")

	stable, _, _ := llm.SplitCacheBoundary(a, b)
	if !strings.Contains(stable, "## 输出格式") || !strings.Contains(stable, "## 编程模式准则") {
		t.Errorf("稳定段被易变内容打断了，实际前缀：%.60s", stable)
	}
	// 段落标题本身可以是公共的（两边都有"## 上下文"），但标题下的**取值**必须落在稳定段之外。
	// 这正是缓存失效的典型原因：夹在中间的一个工作目录就能废掉它后面的全部内容。
	for _, volatile := range []string{
		"D:/ws", "E:/另一个工作区",
		"摘要甲", "摘要乙",
		"上次聊到执行器", "另一次完全不同的对话",
		"偏好最小 diff", "上次计划无效",
	} {
		if strings.Contains(stable, volatile) {
			t.Errorf("易变内容 %q 混进了公共前缀", volatile)
		}
	}
	if n := llm.StablePrefixLen(a, b); n < llm.CacheFriendlyMinChars {
		t.Errorf("公共前缀只有 %d 字，低于值得缓存的量级（%d）", n, llm.CacheFriendlyMinChars)
	}
}

// TestPromptCache_SchemaSectionStaysVolatile 工具 schema 段会随任务历史变化
// （上一轮用过的工具会被追加进来），它必须整段落在稳定段之外：
// 把它算进稳定段，只会让缓存频繁失效，比不缓存还糟。
func TestPromptCache_SchemaSectionStaysVolatile(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12, Role: "coder"}
	a := p.buildSystemPrompt("做点什么", "D:/ws", nil, nil, "", "")
	q := *p
	q.ForceTools = []string{"reply"} // 模拟"本任务上一轮用过 reply"
	b := q.buildSystemPrompt("做点什么", "D:/ws", nil, nil, "", "")

	stable, _, _ := llm.SplitCacheBoundary(a, b)
	if !strings.Contains(stable, "## 能力菜单") {
		t.Error("能力菜单不随任务历史变化，应留在稳定段里")
	}
	if strings.Contains(stable, "本任务此前已用过") {
		t.Error("随任务历史变化的 schema 段不该混进稳定段")
	}
}

// TestPromptCache_FullInjectionIsGoalIndependent 全量注入模式下（MaxToolSchemas<=0），
// 工具 schema 段**不随目标变化**——换目标必须得到逐字节相同的提示词。
//
// 为什么单独守这一条：菜单模式下 schema 段按目标筛选，是提示词的第一个易变段，
// 因此可缓存前缀被截在它之前。实测（内置 23 个工具，空载）：
//
//	菜单模式(max_tool_schemas=12)：总长 4861，可缓存前缀 2409（49.6%）
//	全量注入(max_tool_schemas=0) ：总长 6833，可缓存前缀 6833（100%）
//
// 即全量注入用 +40.6% 的提示词换来了整段可缓存。这条测试守的是那个前提：
// 一旦有人在全量分支里加进任何随目标变化的内容（例如"按目标排序"），
// 100% 可缓存立刻退化成菜单模式那样的半截前缀，而提示词总长看起来仍然正常。
func TestPromptCache_FullInjectionIsGoalIndependent(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 0, Role: "general", TaskMode: "work", Style: "efficient"}
	a := p.buildSystemPrompt("把销售数据整理成表格并汇总", "D:/ws", nil, nil, "", "")
	b := p.buildSystemPrompt("帮我写一封给客户的道歉邮件", "D:/ws", nil, nil, "", "")

	if a != b {
		n := llm.StablePrefixLen(a, b)
		t.Errorf("全量注入下换目标应得到完全相同的提示词，实际公共前缀只有 %d 字（总长 %d/%d）——"+
			"schema 段混进了随目标变化的内容，可缓存前缀被打断",
			n, len([]rune(a)), len([]rune(b)))
	}
	// 全量分支下不该再有"能力菜单"：schema 本来就全给，再列一遍菜单纯属重复。
	if strings.Contains(a, "## 能力菜单") {
		t.Error("全量注入时不应再输出能力菜单（重复内容，且会把稳定段撑长）")
	}
}

// TestPromptCache_IdentityComesFirst 稳定段必须从第一个字符就开始，不能被任何易变内容顶掉。
func TestPromptCache_IdentityComesFirst(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12, Role: "writer"}
	got := p.buildSystemPrompt("随便写点什么", "D:/ws", nil, nil, "摘要", "反馈")
	if !strings.HasPrefix(got, promptIdentity) {
		t.Errorf("提示词应以稳定的身份段开头，实际开头是：%.60s", got)
	}
}

// TestPromptCache_OutputReminderAtEnd 输出格式约束虽然前移进了稳定段，
// 但末尾必须留一行提醒——否则"只输出 JSON"这条最容易违反的约束离生成点太远。
func TestPromptCache_OutputReminderAtEnd(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12}
	got := strings.TrimSpace(p.buildSystemPrompt("做点什么", "D:/ws", nil, nil, "", ""))
	if !strings.HasSuffix(got, "不要任何其他文字、不要代码块。") {
		t.Errorf("末尾应保留输出格式提醒，实际结尾：%.80s", got[len(got)-80:])
	}
}

// TestPromptCache_ReflectorStableInstructionFirst 反思器同理：
// 判定指令（同一任务内不变）在前，逐次变化的执行摘要沉底。
func TestPromptCache_ReflectorStableInstructionFirst(t *testing.T) {
	acc := []string{"文件里有 3 行", "合计正确"}
	execA := &Result{Total: 1, Succeeded: 1, Steps: []types.StepResult{{StepID: "s1", Tool: "file.read", Status: types.StepSucceeded}}}
	execB := &Result{Total: 2, Succeeded: 2, Steps: []types.StepResult{{StepID: "s1", Tool: "shell.exec", Status: types.StepSucceeded}}}
	a := reflectorSystemPrompt(true, buildDigest("目标甲", execA, 1, acc, nil))
	b := reflectorSystemPrompt(true, buildDigest("目标乙", execB, 2, acc, nil))

	stable, restA, _ := llm.SplitCacheBoundary(a, b)
	if !strings.Contains(stable, "## 安全约定") || !strings.Contains(stable, "## 判定要求") {
		t.Errorf("反思器的稳定段应包含安全约定与判定指令，实际前缀：%.80s", stable)
	}
	// 判到标题行为止是对的（标题本身两边都有），但其后的目标文本与执行结果必须全在稳定段之外
	if strings.Contains(stable, "目标甲") || strings.Contains(stable, "目标乙") {
		t.Error("目标文本属于易变内容，不该出现在公共前缀里")
	}
	if strings.Contains(stable, "file.read") || strings.Contains(stable, "shell.exec") {
		t.Error("执行结果属于易变内容，不该出现在公共前缀里")
	}
	if !strings.Contains(restA, "file.read") {
		t.Error("易变段应包含本次的执行结果")
	}
	if n := llm.StablePrefixLen(a, b); n < 400 {
		t.Errorf("反思器公共前缀只有 %d 字，稳定段偏短", n)
	}
}
