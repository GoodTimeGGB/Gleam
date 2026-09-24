// Command gleam 是 Gleam 桌面智能体的主入口。
//
// 用法：
//
//	gleam app      [--addr ADDR] [--mock-llm] [--no-browser]           # 桌面端应用模式（默认窗口）
//	gleam serve    [--config FILE] [--mock-llm] [--mock-script FILE]  # JSON-RPC stdio 服务（编辑器插件接入）
//	gleam goal "…" [--mode auto] [--mock-llm] [--workspace DIR]       # 命令行执行一个目标
//	gleam webui    [--addr ADDR]                                       # Web UI 服务（不自动开窗口）
//	gleam tools                                                        # 列出已注册工具
//	gleam rules    [--task-mode work|code|chat] [--tier T] [--role R]   # 规则集回查（版本指纹 / 场景生效规则）
//	gleam skills list|show NAME                                        # 技能管理
//	gleam schedule list                                                # 定时任务
//	gleam memory search Q | memory save TEXT                           # 记忆检索/写入
//	gleam eval     [--depth select|plan|full] [--baseline FILE]        # 提示词/行为回归评测
//	gleam mcp-fake-server                                              # 内置 MCP 测试服务器（自测用）
//	gleam version
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/agent"
	"gleam/internal/agent/geo"
	"gleam/internal/config"
	"gleam/internal/harness/auth"
	"gleam/internal/harness/conversation"
	"gleam/internal/harness/credentials"
	"gleam/internal/harness/growth"
	"gleam/internal/harness/memory"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/harness/scheduler"
	"gleam/internal/harness/skill"
	"gleam/internal/harness/space"
	"gleam/internal/llm"
	"gleam/internal/server"
	"gleam/internal/tools/desktop"
	"gleam/internal/tools/file"
	"gleam/internal/tools/mcp"
	"gleam/internal/tools/shell"
	"gleam/internal/tools/std"
	"gleam/internal/tools/web"
	"gleam/pkg/types"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	} else if !hasConsole() {
		// windowsgui 桌面版双击启动（无控制台、无参数）→ 直接进入应用模式
		cmd = "app"
	}
	var err error
	switch cmd {
	case "app":
		err = cmdApp(args)
	case "serve":
		err = cmdServe(args)
	case "webui":
		err = cmdWebUI(args)
	case "goal":
		err = cmdGoal(args)
	case "tools":
		err = cmdTools(args)
	case "rules":
		err = cmdRules(args)
	case "skills":
		err = cmdSkills(args)
	case "schedule":
		err = cmdSchedule(args)
	case "memory":
		err = cmdMemory(args)
	case "pending":
		err = cmdPending(args)
	case "doctor":
		err = cmdDoctor(args)
	case "replay":
		err = cmdReplay(args)
	case "eval":
		err = cmdEval(args)
	case "mcp-fake-server":
		err = runMCPFakeServer()
	case "version":
		fmt.Printf("gleam %s (go agent, local-first)\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", cmd)
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		// --help 是正常请求：flag 包已打印用法，这里安静退 0，别报「错误: flag: help requested」
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Gleam（微光）—— 本地优先的桌面 AI 智能体

用法:
  gleam app      [flags]              桌面端应用模式：引擎 + 应用窗口（关闭窗口即退出）
  gleam serve    [flags]              启动 JSON-RPC stdio 服务（编辑器插件接入入口）
  gleam webui    [flags]              启动 Web UI 服务（默认 127.0.0.1:8787，不开窗口）
  gleam goal "目标" [flags]            在命令行执行一个目标
  gleam tools                          列出全部已注册工具
  gleam rules   [flags]                规则集回查：版本指纹、每条规则的适用范围、某场景实际生效的规则
  gleam skills list                    列出技能
  gleam skills show NAME               查看技能详情
  gleam schedule list                  列出定时任务
  gleam memory search QUERY            检索长期记忆
  gleam memory save TEXT               写入长期记忆
  gleam pending [--clear]              列出/清理「等待审批中」的任务（进程重启后仍可见）
  gleam doctor [flags]                 就绪体检（九坑自检）
  gleam replay <taskID> [flags]        回放历史任务：当时的计划与逐步结果；--rerun 用原计划真实重跑
  gleam eval   [flags]                 提示词/行为回归评测（默认 select 深度，离线可跑）
  gleam version                        版本信息

Flags:
  --config FILE      配置文件（默认尝试 configs/config.yaml）
  --workspace DIR    工作区根目录（文件工具边界）
  --data-dir DIR     数据目录（默认 ~/.gleam）
  --mock-llm         使用内置 Mock 模型（离线自测）
  --mock-script FILE Mock 响应脚本 JSON
  --mode MODE        auto | plan_first | interactive
  --json             仅 gleam doctor / gleam eval / gleam rules：输出结构化 JSON 报告
  --strict           仅 gleam doctor / gleam eval：存在不合格项（或评测回归）时以非零退出
  --depth DEPTH      仅 gleam eval：select | plan | full（默认 select）
  --tier NAME        仅 gleam eval / gleam rules：模型档位（eval 里为模拟，rules 里为查询场景）
  --task-mode MODE   仅 gleam rules：work | code | chat（默认 work）
  --role ID          仅 gleam rules：角色 ID，配合 --task-mode/--tier 查询场景
  --cases FILE       仅 gleam eval：自定义用例集 JSON
  --layer LAYER      仅 gleam eval：只跑指定分层 smoke|regression|edge|adversarial|holdout
  --emit-case ID     仅 gleam eval：把失败任务转成用例草稿（badcase 回流，需 --data-dir）
  --save FILE        仅 gleam eval：把本次报告写成基线
  --baseline FILE    仅 gleam eval：与基线对比并报出回归
`)
}

// ---------- 公共装配 ----------

type runtime struct {
	cfg     *config.Config
	client  llm.Client
	reg     *registry.Registry
	mem     *memory.Manager
	gate    *safety.Gate
	skills  *skill.Store
	sched   *scheduler.Scheduler
	agent   *agent.Agent
	cleanup func()
	growth  *growth.Log
}

// buildRuntime 装配全部子系统。
func buildRuntime(configPath, workspace, dataDir string, mockLLM bool, mockScript string, notifier agent.Notifier) (*runtime, error) {
	if configPath == "" {
		if _, err := os.Stat("configs/config.yaml"); err == nil {
			configPath = "configs/config.yaml"
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if workspace != "" {
		cfg.Workspace = workspace
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	// 设置覆盖层（设置页保存的用户偏好，优先于 config.yaml；命令行标志在其后仍可覆盖）
	_ = config.LoadOverlay(cfg, filepath.Join(cfg.DataDir, config.OverlayFile))

	// 本地凭证（LLM API Key / 云端会话），独立 0600 文件；环境变量优先级更高，不覆盖
	credStore, err := credentials.Open(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("打开本地凭证失败: %w", err)
	}
	if cfg.LLM.APIKey == "" {
		if k := credStore.GetLLMAPIKey(); k != "" {
			cfg.LLM.APIKey = k
		}
	}

	if mockLLM {
		cfg.LLM.Provider = "mock"
	}
	if cfg.Safety.Mode == "" {
		cfg.Safety.Mode = "auto"
	}
	agent.DataDir(cfg)

	// LLM 客户端（协议工厂：openai_chat / openai_responses / anthropic；厂商套餐解析官方入口）
	var client llm.Client
	if cfg.LLM.Provider == "mock" {
		m := llm.NewMock()
		if mockScript != "" {
			scripts, err := llm.LoadScripts(mockScript)
			if err != nil {
				return nil, err
			}
			m.Apply(scripts)
		}
		client = m
	} else {
		baseURL, model, protocol := llm.ResolvePreset(cfg.LLM.ProviderID, cfg.LLM.Plan, cfg.LLM.BaseURL, cfg.LLM.Model)
		if protocol == "" {
			protocol = cfg.LLM.Protocol
		}
		if protocol == "" {
			protocol = llm.ProtocolOpenAIChat
		}
		cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.Protocol = baseURL, model, protocol
		client = llm.New(protocol, baseURL, cfg.LLM.APIKey, model,
			cfg.LLM.Temperature, cfg.LLM.MaxTokens, cfg.LLM.TimeoutSecs)
	}

	// 记忆
	mem, err := memory.Open(cfg.DataDir, cfg.Memory.ShortTermCap, cfg.Memory.VectorDim, cfg.Memory.MaxItems)
	if err != nil {
		return nil, fmt.Errorf("打开记忆失败: %w", err)
	}

	// 技能
	// 成长日志（参考阿布自进化能力）
	growthLog, err := growth.Open(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("打开成长日志失败: %w", err)
	}

	// 多会话持久化（左侧会话列表）
	convoStore, err := conversation.Open(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("打开会话存储失败: %w", err)
	}

	// 微光空间（按工作文件夹隔离分组对话），首次自动创建默认空间
	spaceStore, err := space.Open(cfg.DataDir, cfg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("打开空间存储失败: %w", err)
	}

	skills, err := skill.Open(filepath.Join(cfg.DataDir, "skills"))
	if err != nil {
		return nil, fmt.Errorf("打开技能库失败: %w", err)
	}

	// 注册表与内置工具
	reg := registry.New()
	fileTools := file.New(cfg.Workspace)
	fileTools.RegisterAll(reg)
	// shell.exec 的工作目录限制在工作区内（P5-1），与文件工具同一套边界
	reg.MustRegister(shell.NewWithRoots(time.Duration(cfg.Agent.StepTimeoutSecs)*time.Second, cfg.Workspace))
	webTool := web.New()
	// web.fetch 默认拒绝本机/内网地址（防提示词注入把 Agent 当 SSRF 跳板）；
	// 本地优先的场景（读本机 dev server、内网 wiki）用配置显式放宽。
	webTool.AllowPrivate = cfg.Safety.AllowPrivateWeb
	reg.MustRegister(webTool)
	// 桌面集成工具（剪贴板/截屏/通知/片段——参考 Alice 与 WorkBuddy）
	// 剪贴板按副作用拆成读、写两个工具：Permission() 不接受参数，
	// 混装两种等级时声明哪一种都是错的（写会绕过审批闸门）。
	reg.MustRegister(desktop.NewClipboardRead())
	reg.MustRegister(desktop.NewClipboardWrite())
	reg.MustRegister(desktop.NewScreenshot())
	reg.MustRegister(desktop.NewNotify())
	reg.MustRegister(desktop.NewSnippets(cfg.DataDir))
	reg.MustRegister(std.NewReply())
	// 记忆/调度/技能适配器在 agent 创建后补充

	// 调度器
	var sched *scheduler.Scheduler
	if cfg.Scheduler.Enabled {
		sched, err = scheduler.Open(filepath.Join(cfg.DataDir, "schedules.json"), nil)
		if err != nil {
			return nil, fmt.Errorf("打开调度器失败: %w", err)
		}
	}

	// 安全门控
	gate := safety.New(cfg.Safety.Mode, cfg.Safety.TrustedTools, cfg.Safety.TrustedPaths,
		[]string{cfg.Workspace}, time.Duration(cfg.Safety.ApprovalTimeoutSecs)*time.Second)
	// 审计全量落盘（P5-2）：内存环只够复盘最近 200 条，重启即失忆。
	// 追加式 JSONL——审计的价值在于"发生过什么"，重写会把它变成"当前状态"。
	gate.SetAuditPath(filepath.Join(cfg.DataDir, auditFile))
	// 数据出网留痕（P1-3）：本地优先的产品必须能自证"什么数据出了本机"。
	// 两条出网路径都接上——模型调用（提示词）与 web.fetch（抓取目标）。
	// 只记主机名与字节量，内容留在本机；见 safety.AuditEntry.Egress 的边界说明。
	llm.SetEgressHook(func(host string, nbytes int) { gate.RecordEgress("llm", host, nbytes) })
	webTool.OnEgress = func(host string, nbytes int) { gate.RecordEgress("web.fetch", host, nbytes) }
	// 等待审批状态落盘（P4-2）：进程若在等待审批期间退出，重启后能列出卡住的任务。
	gate.SetPendingPath(filepath.Join(cfg.DataDir, pendingFile))
	if len(cfg.Safety.ToolPermissions) > 0 {
		overrides := map[string]types.Permission{}
		for name, perm := range cfg.Safety.ToolPermissions {
			overrides[name] = permFromStr(perm)
		}
		gate.SetToolPermissions(overrides)
	}

	a := agent.New(cfg, client, reg, mem, gate, skills, sched, notifier)
	a.MCP = agent.NewMCPManager()
	a.FileTools = fileTools
	a.Growth = growthLog
	a.RebuildFastClient() // 可选：辅助调用走便宜模型（cfg.LLM.FastModel）
	// GEO 分析历史（创作产出自动分析留档，供 GEO 板块回看）
	if geoStore, err := geo.Open(filepath.Join(cfg.DataDir, "geo_history.json")); err == nil {
		a.GEO = geoStore
	}
	a.Convos = convoStore
	a.Spaces = spaceStore
	a.Creds = credStore
	a.Auth = auth.New(credStore)

	// 依赖 Agent 的适配器工具
	reg.MustRegister(std.NewMemSave(&memAdapter{m: mem}))
	reg.MustRegister(std.NewMemSearch(&memAdapter{m: mem}))
	reg.MustRegister(std.NewMemDelete(&memAdapter{m: mem}))
	if sched != nil {
		reg.MustRegister(std.NewScheduleCreate(&schedAdapter{a: a}))
		reg.MustRegister(std.NewScheduleList(&schedAdapter{a: a}))
		reg.MustRegister(std.NewScheduleDelete(&schedAdapter{a: a}))
	}
	reg.MustRegister(std.NewSkillList(&skillAdapter{s: skills}))
	reg.MustRegister(std.NewSkillRun(&skillAdapter{s: skills}, a))

	// MCP 连接器（尽力而为）
	if len(cfg.MCP) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		mcpClients := mcp.RegisterAll(ctx, reg, toMCPCfgs(cfg.MCP), func(f string, xs ...any) {
			fmt.Fprintf(os.Stderr, "[mcp] "+f+"\n", xs...)
		})
		cancel()
		a.MCP.AddAll(mcpClients)
	}

	// 调度器启动：到点自动执行目标
	cleanup := func() {
		a.MCP.CloseAll()
		if sched != nil {
			sched.Stop()
		}
		_ = mem.Close()
	}
	if sched != nil {
		sched.Start()
	}

	return &runtime{cfg: cfg, client: client, reg: reg, mem: mem, gate: gate, skills: skills, sched: sched, agent: a, cleanup: cleanup, growth: growthLog}, nil
}

// sysNotify 发系统通知。抽成变量是为了两件事：
//   - **测试能替换它**（否则跑测试会在开发机上真的弹窗）；
//   - **无头/自动化环境能关掉它**（GLEAM_NO_SYS_NOTIFY=1）：关掉时不是静默丢弃，
//     而是把"本来要发什么"打到 stderr——否则冒烟测试里"通知发了没有"就无从断言，
//     而"断言不了"最后会变成"没人验证过"。
var sysNotify = func(title, message string) error {
	if os.Getenv("GLEAM_NO_SYS_NOTIFY") != "" {
		fmt.Fprintf(os.Stderr, "[schedule] 系统通知（已禁用，仅打印）：%s — %s\n", title, message)
		return nil
	}
	return desktop.Notify(title, message)
}

// notifyScheduledDone 定时任务跑完之后的**送达**。
//
// 抽成独立函数而不是内联在 fire 闭包里，是为了能测"接线"：
// 单测 `ShouldNotify` 只能证明**判据**对，证明不了**它真的被调用了**——
// 而本仓库栽过四次的恰好是这一类（判据对、线没接）。这里可以注入假的系统通知
// 与假的事件出口，断言"该响的时候两条都响了、该静默的时候两条都没响"。
func notifyScheduledDone(j scheduler.Job, res *types.GoalResult, sink agent.Notifier) {
	if res == nil {
		return
	}
	ev := types.TaskDoneEvent{
		TaskID: res.TaskID,
		Origin: fmt.Sprintf("定时任务「%s」", j.Name),
		Result: *res,
	}
	if !j.ShouldNotify(ev.Succeeded()) {
		fmt.Fprintf(os.Stderr, "[schedule] %s（按通知策略静默）\n", ev.Line())
		return
	}
	if err := sysNotify(ev.Title(), ev.Line()); err != nil {
		// 通知送不出去不影响任务本身，但不能装作没发生——否则"没收到通知"
		// 会被误读成"任务没跑"。
		fmt.Fprintf(os.Stderr, "[schedule] 系统通知未送达（%v）\n", err)
	}
	if sink != nil {
		sink.OnTaskDone(ev)
	}
}

// runGoalFor 是 fire 路径调目标执行器的入口。
//
// 抽成变量是为了能测「接线」：真跑一次 RunGoal 要 LLM 加整套装配，而这里要验的是
// **通知有没有接上**，不是规划器好不好。用真 RunGoal 会把两个问题搅在一起，
// 而且模型一不稳测试就红——那会让人把"接线断了"读成"模型抽风"，然后去查错的地方。
var runGoalFor = func(a *agent.Agent, ctx context.Context, req types.GoalRequest) *types.GoalResult {
	return a.RunGoal(ctx, req)
}

// fireScheduledJob 定时任务真正跑一次。
//
// 定时任务的**结果必须送达**。跑完之后把 GoalResult 丢掉，等于要求用户一直盯着——
// 而定时任务的整个价值就是不用盯着；更坏的是**失败也静默**：一个每天 9 点的巡检
// 连坏一周，用户会以为一切正常。
//
// **为什么在这里（而不是绑定时）读 notifier**：本函数在 notifier 装配之前就被绑定
// （`app` 模式里 `bindSchedulerFire` 在 `webui.NewServer` 之前），绑定时拿到的是
// 装配期的 nil——原来的 `_ = notifier` 就是这个顺序问题被绕过时留下的痕迹。
// 必须在**跑的时候**惰性读 `rt.agent.Notifier`，那时它已经被换成真正的宿主。
func fireScheduledJob(rt *runtime, j scheduler.Job) {
	mode := j.Mode
	if mode == "" {
		mode = "auto"
	}
	fmt.Fprintf(os.Stderr, "[schedule] 触发任务 %q: %s\n", j.Name, types.Shorten(j.Goal, 60))
	res := runGoalFor(rt.agent, context.Background(), types.GoalRequest{Goal: j.Goal, Mode: mode})
	notifyScheduledDone(j, res, rt.agent.Notifier)
}

// bindSchedulerFire 调度器 fire 延迟绑定（避免装配顺序问题）：装配完成后设置。
func bindSchedulerFire(rt *runtime) {
	if rt.sched == nil {
		return
	}
	rt.sched.SetFire(func(j scheduler.Job) { fireScheduledJob(rt, j) })
}

// ---------- serve ----------

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, nil)
	if err != nil {
		return err
	}
	defer rt.cleanup()
	bindSchedulerFire(rt)
	fmt.Fprintf(os.Stderr, "[gleam] 服务已启动 (model=%s, tools=%d, mode=%s)——等待 JSON-RPC 输入\n",
		rt.agent.LLM.Name(), rt.reg.Len(), rt.cfg.Safety.Mode)
	return server.RunStdio(rt.agent)
}

// ---------- goal ----------

// splitFlagArgs 把参数重排为「标志在前、位置参数在后」：
// Go flag 包遇到首个非标志参数即停止解析，这里允许用户以任意顺序书写。
// 取值判断直接查询 FlagSet（布尔型标志不带独立取值）。
func splitFlagArgs(args []string, fs *flag.FlagSet) (flagArgs, positionals []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flagArgs = append(flagArgs, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			if f := fs.Lookup(name); f != nil {
				if bv, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bv.IsBoolFlag() {
					continue // 布尔标志不吞下一个参数
				}
				if i+1 < len(args) {
					flagArgs = append(flagArgs, args[i+1])
					i++
				}
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return flagArgs, positionals
}

func cmdGoal(args []string) error {
	fs := flag.NewFlagSet("goal", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	mode := fs.String("mode", "auto", "执行模式 auto|plan_first|interactive")
	flagArgs, positionals := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	rest := append(fs.Args(), positionals...)
	if len(rest) == 0 {
		fs.PrintDefaults()
		return fmt.Errorf(`用法: gleam goal "目标" [--mode auto|plan_first|interactive]
      [--workspace DIR] [--data-dir DIR] [--config FILE] [--mock-llm] [--mock-script FILE]`)
	}
	goal := strings.Join(rest, " ")

	notifier := newConsoleNotifier()
	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, notifier)
	if err != nil {
		return err
	}
	defer rt.cleanup()
	bindSchedulerFire(rt)

	result := rt.agent.RunGoal(context.Background(), types.GoalRequest{Goal: goal, Mode: *mode})
	// 归档到 tasks/<id>.json（与 serve 模式同一落点）：ExecutedPlan 随结果落盘，
	// gleam replay 才有凭据回放"当时到底打算怎么做、做成了什么样"。
	if dir := filepath.Join(rt.cfg.DataDir, "tasks"); dir != "" {
		if b, err := json.MarshalIndent(result, "", " "); err == nil {
			if err := os.MkdirAll(dir, 0o755); err == nil {
				if err := os.WriteFile(filepath.Join(dir, result.TaskID+".json"), b, 0o644); err == nil {
					// 终态快照已经写下，运行中的步骤日志就是冗余的——删掉。
					// runs/ 里因此只留下**没跑完**的运行，正好是唯一需要它的那批。
					agent.DiscardRunLog(rt.cfg.DataDir, result.TaskID)
					fmt.Fprintf(os.Stderr, "[gleam] 任务已存档：gleam replay %s 可回放（trace_id %s）\n", result.TaskID, result.TraceID)
				}
			}
		}
	}
	// 结果摘要到 stdout（可管道），过程在 stderr
	fmt.Println(result.Summary)
	if os.Getenv("GLEAM_DEBUG") != "" {
		for _, st := range result.Steps {
			fmt.Fprintf(os.Stderr, "  [%s] %s (%s) %s\n", st.Status, st.StepID, st.Tool, st.Error)
		}
	}
	switch result.Status {
	case types.GoalSuccess:
		if result.RetryOK() > 0 {
			fmt.Fprintf(os.Stderr, "[gleam] %d 个步骤经历重试后才成功（GLEAM_DEBUG 可查看明细）\n", result.RetryOK())
		}
		return nil
	case types.GoalPartial:
		fmt.Fprintf(os.Stderr, "[gleam] 部分完成（完成度 %d/100）\n", result.Score)
		printFailureBreakdown(result)
		return nil
	default:
		fmt.Fprintf(os.Stderr, "[gleam] %s: %s（完成度 %d/100）\n", result.Status, result.Error, result.Score)
		printFailureBreakdown(result)
		return fmt.Errorf("目标未完成")
	}
}

// printFailureBreakdown 失败时的归因分布：失败率只回答"坏了多少"，
// 这一行回答"该先修哪一层"——参数错改参数、业务拒绝换方案、权限不足别重试。
func printFailureBreakdown(result *types.GoalResult) {
	if len(result.FailureBreakdown) == 0 {
		return
	}
	kinds := make([]types.ErrorKind, 0, len(result.FailureBreakdown))
	for k := range result.FailureBreakdown {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	parts := make([]string, 0, len(kinds))
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%s=%d", k, result.FailureBreakdown[k]))
	}
	fmt.Fprintf(os.Stderr, "[gleam] 失败归因：%s（trace_id %s）\n", strings.Join(parts, " "), result.TraceID)
}

// ---------- tools / skills / schedule / memory ----------

func cmdTools(args []string) error {
	rt, err := buildRuntime("", "", "", false, "", agent.NopNotifier{})
	if err != nil {
		return err
	}
	defer rt.cleanup()
	for _, t := range rt.reg.List() {
		fmt.Printf("%-24s %-13s %s\n", t.Name(), t.Permission(), t.Description())
	}
	return nil
}

func cmdSkills(args []string) error {
	sub := "list"
	var rest []string
	if len(args) > 0 {
		sub = args[0]
		rest = args[1:]
	}
	rt, err := buildRuntime("", "", "", false, "", agent.NopNotifier{})
	if err != nil {
		return err
	}
	defer rt.cleanup()
	switch sub {
	case "list":
		for _, s := range rt.skills.List() {
			fmt.Printf("%-24s v%-2d 运行 %d 次（成功 %d） %s\n", s.Name, s.Version, s.Runs, s.Successes, s.Description)
		}
	case "show":
		if len(rest) == 0 {
			return fmt.Errorf("用法: gleam skills show NAME")
		}
		s, err := rt.skills.Get(rest[0])
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(b))
	default:
		return fmt.Errorf("未知子命令 %q", sub)
	}
	return nil
}

func cmdSchedule(args []string) error {
	rt, err := buildRuntime("", "", "", false, "", agent.NopNotifier{})
	if err != nil {
		return err
	}
	defer rt.cleanup()
	if rt.sched == nil {
		// Scheduler can be disabled in configuration; listing should remain a
		// harmless read operation instead of dereferencing a nil scheduler.
		return nil
	}
	for _, j := range rt.sched.ListJobs() {
		next := "-"
		if j.NextRun != nil {
			next = j.NextRun.Format("01-02 15:04")
		}
		state := "启用"
		if !j.Enabled {
			state = "停用"
		}
		fmt.Printf("%-20s %-6s next=%s goal=%s\n", j.Name, state, next, types.Shorten(j.Goal, 40))
	}
	return nil
}

func cmdMemory(args []string) error {
	sub := "search"
	var rest []string
	if len(args) > 0 {
		sub = args[0]
		rest = args[1:]
	}
	rt, err := buildRuntime("", "", "", false, "", agent.NopNotifier{})
	if err != nil {
		return err
	}
	defer rt.cleanup()
	switch sub {
	case "save":
		if len(rest) == 0 {
			return fmt.Errorf("用法: gleam memory save TEXT")
		}
		id, err := rt.mem.Remember(strings.Join(rest, " "), nil)
		if err != nil {
			return err
		}
		fmt.Println("已保存:", id)
	case "search":
		if len(rest) == 0 {
			return fmt.Errorf("用法: gleam memory search QUERY")
		}
		for _, h := range rt.mem.Relevant(strings.Join(rest, " "), 5) {
			fmt.Printf("%.3f  %s  %s\n", h.Score, h.ID[:8], types.Shorten(h.Content, 100))
		}
	default:
		return fmt.Errorf("未知子命令 %q", sub)
	}
	return nil
}

// ---------- 控制台 Notifier ----------

type consoleNotifier struct {
	stdin *bufio.Reader
}

func newConsoleNotifier() *consoleNotifier { return &consoleNotifier{stdin: bufio.NewReader(os.Stdin)} }

func (c *consoleNotifier) OnProgress(ev types.ProgressEvent) {
	if ev.Kind == "llm" {
		fmt.Fprintf(os.Stderr, "\r[gleam %3d%%] %s", ev.Progress, types.Shorten(ev.Message, 100))
		return
	}
	fmt.Fprintf(os.Stderr, "\n[gleam %3d%%] %s\n", ev.Progress, ev.Message)
}

func (c *consoleNotifier) OnApproval(req types.ApprovalRequest) types.ApprovalResponse {
	fmt.Fprintf(os.Stderr, "\n⚠ 需要你的批准（风险: %s）%s\n", req.Risk, req.Reason)
	for _, line := range req.Plan {
		fmt.Fprintf(os.Stderr, "  - %s\n", line)
	}
	fmt.Fprintf(os.Stderr, "允许执行吗? [y/N]（%v 秒内未答复将自动拒绝）> ", int(gApprovalTimeout().Seconds()))
	// 带超时的读取
	ch := make(chan string, 1)
	go func() {
		line, err := c.stdin.ReadString('\n')
		if err != nil {
			ch <- ""
			return
		}
		ch <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ch:
		if line == "y" || line == "Y" || line == "yes" {
			return types.ApprovalResponse{Approved: true}
		}
		return types.ApprovalResponse{Approved: false, Note: "用户拒绝"}
	case <-time.After(gApprovalTimeout()):
		fmt.Fprintln(os.Stderr, "\n（超时，自动拒绝）")
		return types.ApprovalResponse{Approved: false, Note: "审批超时"}
	}
}

func (c *consoleNotifier) OnSuggestion(taskID, text string) {
	fmt.Fprintf(os.Stderr, "\n💡 建议：%s\n", text)
}

func (c *consoleNotifier) OnSuggestSkill(taskID string, draft types.SkillDraft) {
	fmt.Fprintf(os.Stderr, "\n💡 本次任务可固化为技能 %q（含 %d 个步骤）。在 JSON-RPC 中响应 agent/suggest_skill 并调用 skills/save 即可保存。\n",
		draft.Name, len(draft.Steps))
}

func (c *consoleNotifier) OnTaskDone(ev types.TaskDoneEvent) {
	fmt.Fprintf(os.Stderr, "\n🔔 %s：%s\n", ev.Title(), ev.Line())
}

// gApprovalTimeout / s.AgentGateApprovalTimeout 由装配时的配置决定；
// 控制台简化为环境变量或默认值。
func gApprovalTimeout() time.Duration {
	if v := os.Getenv("GLEAM_APPROVAL_TIMEOUT_SEC"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Minute
}

// ---------- 适配器 ----------

type memAdapter struct{ m *memory.Manager }

func (a *memAdapter) Remember(content string, tags []string) (string, error) {
	return a.m.Remember(content, tags)
}
func (a *memAdapter) Search(query string, k int) []std.SearchHit {
	hits := a.m.Relevant(query, k)
	out := make([]std.SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, std.SearchHit{ID: h.ID, Content: h.Content, Tags: h.Tags, Score: h.Score})
	}
	return out
}
func (a *memAdapter) Count() int { return a.m.Long.Count() }
func (a *memAdapter) SoftDelete(id string) bool {
	ok := a.m.Long.SoftDelete(id)
	if ok {
		_ = a.m.Long.Flush()
	}
	return ok
}

type schedAdapter struct{ a *agent.Agent }

func (s *schedAdapter) AddJob(name, cron string, intervalSec int, goal, mode string) (std.JobDef, error) {
	j, err := s.a.Sched.AddJob(name, cron, intervalSec, goal, mode)
	if err != nil {
		return std.JobDef{}, err
	}
	return toJobDef(j), nil
}

func (s *schedAdapter) AddJobDetailed(name, cron string, intervalSec int, goal, mode, whenText, scheduleText string) (std.JobDef, error) {
	j, err := s.a.Sched.AddJobDetailed(name, cron, intervalSec, goal, mode, whenText, scheduleText)
	if err != nil {
		return std.JobDef{}, err
	}
	return toJobDef(j), nil
}
func (s *schedAdapter) DeleteJob(name string) (bool, error) { return s.a.Sched.DeleteJob(name) }
func (s *schedAdapter) ListJobs() []std.JobDef {
	jobs := s.a.Sched.ListJobs()
	out := make([]std.JobDef, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toJobDef(j))
	}
	return out
}

func toJobDef(j scheduler.Job) std.JobDef {
	d := std.JobDef{
		Name: j.Name, Cron: j.Cron, IntervalSec: j.IntervalSec,
		Goal: j.Goal, Mode: j.Mode, Enabled: j.Enabled,
		WhenText: j.WhenText, ScheduleText: j.ScheduleText,
	}
	if j.NextRun != nil {
		d.NextRun = j.NextRun.Format(time.RFC3339)
	}
	if j.LastRun != nil {
		d.LastRun = j.LastRun.Format(time.RFC3339)
	}
	return d
}

type skillAdapter struct{ s *skill.Store }

func (a *skillAdapter) ListSummaries() []std.SkillSummary {
	list := a.s.List()
	out := make([]std.SkillSummary, 0, len(list))
	for _, s := range list {
		out = append(out, std.SkillSummary{Name: s.Name, Description: s.Description, Version: s.Version, Runs: s.Runs, Successes: s.Successes})
	}
	return out
}
func (a *skillAdapter) GetParams(name string) ([]string, error) { return a.s.GetParams(name) }

// permFromStr 权限字符串解析（设置页/配置文件共用）。
func permFromStr(s string) types.Permission {
	switch s {
	case "readonly":
		return types.PermissionReadOnly
	case "full_access":
		return types.PermissionFullAccess
	default:
		return types.PermissionUserApproved
	}
}

func toMCPCfgs(in []config.MCPServerConfig) []mcp.ServerConfig {
	out := make([]mcp.ServerConfig, 0, len(in))
	for _, s := range in {
		out = append(out, mcp.ServerConfig{Name: s.Name, Command: s.Command, Args: s.Args, Trust: s.Trust, Enabled: s.Enabled})
	}
	return out
}
