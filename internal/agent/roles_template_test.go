package agent

import (
	"strings"
	"testing"
	"time"

	"gleam/internal/config"
	"gleam/internal/harness/registry"
	"gleam/internal/llm"
	"gleam/internal/tools/file"
	"gleam/internal/tools/shell"
	"gleam/internal/tools/std"
	"gleam/internal/tools/web"
	"gleam/pkg/types"
)

// realishRegistry 尽量用真实的工具构造器拼一个注册表，用来校验场景模板里写的工具名。
// memory/schedule/skill 那几个要挂适配器（由运行时装配），这里用轻量替身占位——
// 它们的存在只为让"名字对不对"这件事可被检查，不参与任何执行。
func realishRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	file.New(t.TempDir()).RegisterAll(reg)
	reg.MustRegister(shell.New(5 * time.Second))
	reg.MustRegister(web.New())
	reg.MustRegister(std.NewReply())
	for _, name := range []string{
		"memory.save", "memory.search",
		"schedule.create", "schedule.list", "schedule.delete",
		"skill.list", "skill.run",
	} {
		reg.MustRegister(&funcTool{name: name, perm: types.PermissionReadOnly})
	}
	return reg
}

// TestRoleTemplate_EveryRoleIsComplete 场景模板的完整性：
// 一个角色若只写了人设，就等于没写模板——产出格式、核心工具、模型档位都得有。
func TestRoleTemplate_EveryRoleIsComplete(t *testing.T) {
	knownTiers := map[string]bool{
		TierEconomy: true, TierCoding: true, TierOffice: true, TierReasoning: true,
	}
	for _, r := range BuiltinRoles {
		if strings.TrimSpace(r.SystemPrompt) == "" {
			t.Errorf("角色 %s 缺少系统提示词", r.ID)
		}
		if strings.TrimSpace(r.AcceptanceFocus) == "" {
			t.Errorf("角色 %s 缺少验收重点", r.ID)
		}
		if strings.TrimSpace(r.OutputFormat) == "" {
			t.Errorf("角色 %s 缺少产出格式约束", r.ID)
		}
		if !knownTiers[r.ModelTier] {
			t.Errorf("角色 %s 的模型档位 %q 不在已知档位里", r.ID, r.ModelTier)
		}
		// 通用助手不限定工具（什么活都可能接），其余角色必须声明核心工具
		if r.ID != "general" && len(r.Tools) == 0 {
			t.Errorf("角色 %s 未声明核心工具，菜单模式下容易被筛漏", r.ID)
		}
	}
}

// TestRoleTemplate_ToolNamesResolve 模板里写的工具名必须真实存在。
// 拼错一个字母不会报错，只会让"保证附上 schema"这条静默失效——所以要有测试兜住。
func TestRoleTemplate_ToolNamesResolve(t *testing.T) {
	reg := realishRegistry(t)
	for _, r := range BuiltinRoles {
		for _, name := range r.Tools {
			if _, ok := reg.Get(name); !ok {
				t.Errorf("角色 %s 声明的工具 %q 在注册表里不存在", r.ID, name)
			}
		}
	}
}

// TestRoleTemplate_ToolsAreDistinct 角色之间不该雷同到没有区分度，
// 同时单个角色的工具列表里也不能有重复项。
func TestRoleTemplate_ToolsAreDistinct(t *testing.T) {
	seen := map[string]string{} // 工具名 -> 首个声明它的角色
	for _, r := range BuiltinRoles {
		local := map[string]bool{}
		for _, name := range r.Tools {
			if local[name] {
				t.Errorf("角色 %s 的工具列表里 %q 重复", r.ID, name)
			}
			local[name] = true
			if prev, ok := seen[name]; ok {
				t.Logf("工具 %q 同时出现在 %s 与 %s（可接受，但值得留意区分度）", name, prev, r.ID)
			} else {
				seen[name] = r.ID
			}
		}
	}
	if len(seen) < 5 {
		t.Errorf("各角色声明的工具去重后只有 %d 个，模板之间缺少区分度", len(seen))
	}
}

// TestRoleTemplate_OutputFormatReachesPrompt 产出格式必须真的进提示词，且落在稳定段里
// （它不随目标变化，缓存友好）。
func TestRoleTemplate_OutputFormatReachesPrompt(t *testing.T) {
	p := &Planner{Reg: bigRegistry(25), MaxToolSchemas: 12, Role: "coder"}
	got := p.buildSystemPrompt("改个 bug", "D:/ws", nil, nil, "", "")
	want := FindRole("coder").OutputFormat
	if want == "" {
		t.Fatal("coder 角色应有产出格式约束")
	}
	if !strings.Contains(got, want) {
		t.Error("产出格式没有进入规划器提示词")
	}
	if !strings.Contains(got, "## 本场景产出格式") {
		t.Error("产出格式应有独立小标题，便于模型识别")
	}
}

// TestRoleTemplate_RoleToolsAlwaysGetSchema 场景核心工具在菜单模式下必须附完整 schema——
// 否则"这个场景本来就靠这几件家伙吃饭"的工具反而可能没参数可填。
func TestRoleTemplate_RoleToolsAlwaysGetSchema(t *testing.T) {
	reg := realishRegistry(t)
	role := FindRole("coder")
	p := &Planner{Reg: reg, MaxToolSchemas: 3, Role: role.ID, RoleTools: role.Tools}
	sec := p.toolSchemaSection("随便做点什么")

	// 预算比角色工具数还紧时，预算优先（用户显式设的上限不该被模板顶穿），
	// 但角色工具要排在目标快筛的结果之前——它们是"这个场景保证要用"的，
	// 不该被一次可能失手的相关性筛选挤掉。reply 始终占一席（它是收尾步骤）。
	if got := strings.Count(sec, "参数schema:"); got != 3 {
		t.Errorf("仍应只注入 3 个 schema，实际 %d", got)
	}
	if !strings.Contains(sec, "- reply：") {
		t.Error("reply 是收尾步骤，必须始终附完整 schema")
	}
	present := 0
	for _, name := range role.Tools {
		if strings.Contains(sec, "- "+name+"：") {
			present++
		}
	}
	if want := 3 - 1; present < want { // 预算 3 减去 reply 必占的 1 席
		t.Errorf("剩余预算应优先给场景核心工具（至少 %d 个），实际只装下 %d 个", want, present)
	}
	if !strings.Contains(sec, "本场景核心工具") {
		t.Error("应说明这些工具为何被保留，用户才知道它从哪来")
	}
}

// TestRoleTemplate_RoleToolsFitInDefaultBudget 默认预算下（12 个）角色的核心工具应全部装得下。
// 这是"模板声明了就一定可用"这句话的实际含义。
func TestRoleTemplate_RoleToolsFitInDefaultBudget(t *testing.T) {
	reg := realishRegistry(t)
	for _, r := range BuiltinRoles {
		if len(r.Tools) == 0 {
			continue
		}
		p := &Planner{Reg: reg, MaxToolSchemas: 12, Role: r.ID, RoleTools: r.Tools}
		sec := p.toolSchemaSection("随便做点什么")
		for _, name := range r.Tools {
			if !strings.Contains(sec, "- "+name+"：") {
				t.Errorf("角色 %s 的核心工具 %s 在默认预算下未附 schema", r.ID, name)
			}
		}
	}
}

// TestRoleTemplate_GeneralRoleAddsNothing 通用助手不声明核心工具时，不应凭空多出提示文字。
func TestRoleTemplate_GeneralRoleAddsNothing(t *testing.T) {
	reg := realishRegistry(t)
	role := FindRole("")
	if len(role.Tools) != 0 {
		t.Fatalf("通用助手不该声明核心工具，实际 %v", role.Tools)
	}
	p := &Planner{Reg: reg, MaxToolSchemas: 3, Role: role.ID, RoleTools: role.Tools}
	if sec := p.toolSchemaSection("随便做点什么"); strings.Contains(sec, "本场景核心工具") {
		t.Error("没有核心工具时不该出现该说明")
	}
}

// TestTierLLM_FallsBackToMainModel 档位是"可以挑"，不是"必须挑"：
// 没配档位、档位名不存在、档位指向主模型，三种情况都必须安全回退，不能报错也不能空指针。
func TestTierLLM_FallsBackToMainModel(t *testing.T) {
	main := llm.NewMock()
	mk := func(tiers map[string]string) *Agent {
		cfg := config.Default()
		cfg.LLM.Tiers = tiers
		cfg.LLM.Model = "main-model"
		cfg.LLM.Provider = "glm"
		return &Agent{LLM: main, Cfg: cfg}
	}

	if got := mk(nil).TierLLM("coder"); got != main {
		t.Error("未配档位表时应回退主模型")
	}
	if got := mk(map[string]string{"coding": "coder-model"}).TierLLM("general"); got != main {
		t.Error("角色未声明该档位时应回退主模型")
	}
	if got := mk(map[string]string{"coding": "main-model"}).TierLLM("coder"); got != main {
		t.Error("档位指向的就是主模型时，应复用主客户端而不是另建一个")
	}
	if got := mk(map[string]string{"coding": "   "}).TierLLM("coder"); got != main {
		t.Error("档位值为空白时应回退主模型")
	}
}

// TestTierLLM_BuildsClientPerTier 配了档位就真的换模型，且同一档位复用同一个客户端
// （否则每次规划都新建连接池）。
func TestTierLLM_BuildsClientPerTier(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "glm"
	cfg.LLM.BaseURL = "http://127.0.0.1:1"
	cfg.LLM.Model = "main-model"
	cfg.LLM.Tiers = map[string]string{"coding": "coder-model", "office": "writer-model"}
	a := &Agent{LLM: llm.NewMock(), Cfg: cfg}

	coder := a.TierLLM("coder")
	if coder == a.LLM {
		t.Fatal("配了 coding 档位就应换模型")
	}
	if coder.Name() != "coder-model" {
		t.Errorf("应使用 coding 档位指定的模型，实际 %s", coder.Name())
	}
	if again := a.TierLLM("coder"); again != coder {
		t.Error("同一档位应复用同一个客户端")
	}
	if writer := a.TierLLM("writer"); writer == coder || writer.Name() != "writer-model" {
		t.Errorf("不同档位应各自建客户端，实际 %v", writer.Name())
	}

	// 改了档位映射后必须能热生效
	a.Cfg.LLM.Tiers["coding"] = "coder-model-v2"
	a.RebuildTierClients()
	if got := a.TierLLM("coder").Name(); got != "coder-model-v2" {
		t.Errorf("重建后应使用新模型，实际 %s", got)
	}
}

// TestTierLLM_MockProviderNeverBuildsClient 离线自测不该因为配了档位而产生真实网络调用。
func TestTierLLM_MockProviderNeverBuildsClient(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "mock"
	cfg.LLM.Model = "main-model"
	cfg.LLM.Tiers = map[string]string{"coding": "coder-model"}
	main := llm.NewMock()
	a := &Agent{LLM: main, Cfg: cfg}
	if got := a.TierLLM("coder"); got != main {
		t.Error("mock 会话下档位应失效，避免离线自测打网络")
	}
}
