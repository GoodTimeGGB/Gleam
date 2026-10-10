// Package config 提供 Gleam 的配置加载。
// 优先级：默认值 < 配置文件 < 环境变量 < 命令行标志（由调用方覆盖）。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gleam/internal/atomicfile"
	"gleam/pkg/types"
)

// Config 是 Gleam 的顶层配置。
type Config struct {
	LLM              LLMConfig
	Agent            AgentConfig
	Safety           SafetyConfig
	Memory           MemoryConfig
	Scheduler        SchedulerConfig
	Persona          PersonaConfig
	Network          NetworkConfig
	Git              GitConfig
	Worktrees        WorktreeConfig
	MCP              []MCPServerConfig
	DataDir          string   // 数据目录，默认 ~/.gleam
	Workspace        string   // 默认工作区（文件工具的根目录）
	WorkspaceRecents []string // 最近使用的工作区（快速切换）
}

// GitConfig git 动作的偏好。三条都作用于 Gleam 自己的 git.* 工具，
// 不管模型直接通过 shell.exec 拼的命令（那条路不归这里管）。
type GitConfig struct {
	BranchPrefix       string // 由 Gleam 创建的分支统一加这个前缀
	ForcePush          bool   // 推送时用 --force-with-lease
	CommitInstructions string // 生成提交说明时的写法要求
}

// WorktreeConfig「每个任务在自己的 git worktree 里跑」的偏好。
//
// Enabled 默认**关**：打开之后模型写的文件不再落在你的工作区里，而是落在
// `<数据目录>/worktrees/<任务ID>`。这改的是"文件写到哪"这个根本边界，
// 对所有既有安装默认生效等于一次静默的行为变更。
//
// 其余三项都只在 Enabled 之后才有意义，也默认关/保守：FetchBeforeCreate 会出网，
// AutoDelete 会让"能不能还原"依赖"worktree 还在不在"（见 docs/known-limits.md）。
type WorktreeConfig struct {
	Enabled           bool // 总开关：为任务建 worktree，任务在它里面执行
	FetchBeforeCreate bool // 建之前先 fetch（出网，台账记 git.remote）
	AutoDelete        bool // 任务跑完后自动删掉**干净**的 worktree
	MaxCount          int  // 保留上限，超出时清最旧且干净的（0 表示不限制；默认 0）
}

// NetworkConfig 出网方式（目前只作用于访问模型服务的那条路）。
// 零值 "" 等价于 system：老配置不改也照旧走环境变量，行为不漂。
type NetworkConfig struct {
	ProxyMode string // system（跟随 HTTPS_PROXY 等环境变量）| manual（用 ProxyURL）| none（直连，忽略环境变量）
	ProxyURL  string // ProxyMode == manual 时的代理地址
}

// ProxyModeOrDefault 把空值收敛成 system，界面与运行时用同一个口径。
func (n NetworkConfig) ProxyModeOrDefault() string {
	if n.ProxyMode == "" {
		return "system"
	}
	return n.ProxyMode
}

// ModelEntry 是一个可独立调用的模型接入配置。
// 每个条目自带厂商、协议、地址、模型 ID 和密钥，互不依赖——
// 用户可以在 DeepSeek、GLM、OpenAI 之间自由切换，也可以给同一厂商配多个模型。
type ModelEntry struct {
	ID         string  `yaml:"id" json:"id"`                         // 唯一标识（前端生成或用户填），如 "deepseek-chat"
	Name       string  `yaml:"name" json:"name"`                     // 显示名，如 "DeepSeek Chat"
	ProviderID string  `yaml:"provider_id" json:"provider_id"`       // 厂商预设 ID（zhipu/deepseek/openai/…，空为自定义）
	Protocol   string  `yaml:"protocol" json:"protocol"`             // openai_chat | openai_responses | anthropic
	BaseURL    string  `yaml:"base_url" json:"base_url"`             // 显式 API 地址（空则用厂商预设）
	Model      string  `yaml:"model" json:"model"`                   // 模型 ID
	Plan       string  `yaml:"plan" json:"plan"`                     // token | coding | agent
	APIKey     string  `yaml:"api_key" json:"api_key"`               // 独立密钥（空则用全局凭证）
	IsDefault  bool    `yaml:"is_default" json:"is_default"`         // 新对话默认用这个
	IsFast     bool    `yaml:"is_fast" json:"is_fast"`               // 辅助模型（压缩/复核等高频小调用）
}

type LLMConfig struct {
	Provider   string // glm（OpenAI 兼容协议）| mock
	Protocol   string // openai_chat | openai_responses | anthropic（空视为 openai_chat）
	ProviderID string // 厂商预设 ID（zhipu/deepseek/moonshot/qwen/volc/minimax/openai/anthropic/openrouter，空为自定义）
	Plan       string // 套餐：token | coding | agent（决定预设中的官方入口）
	BaseURL    string // 如 https://open.bigmodel.cn/api/paas/v4（显式填写优先于预设）
	Model      string // 如 glm-5.3-flash
	FastModel  string // 可选：辅助调用（上下文压缩/GEO/技能优化）用的便宜模型，空表示都用主模型
	// Tiers 模型档位表：档位名 → 模型 ID，例如 {coding: deepseek-coder, office: glm-5.3-flash}。
	// 场景模板（专家角色）声明自己用哪一档，引擎据此换模型——"同一个 Harness，按场景换模型"，
	// 不被单一模型绑死。档位没配就一律用主模型，所以留空是安全的。
	Tiers  map[string]string
	Models []ModelEntry // 多模型列表：每个条目是独立的模型接入，可在对话中切换
	APIKey string // 生产环境建议经环境变量注入
	// APIKeyScope 是 APIKey 被授权发往的接入主机（见 llm.KeyScope），运行时标记，
	// **不序列化**：覆盖层里根本没有 api_key，这把 key 的落点在凭证文件里自带同一字段。
	// 内存里留它，是为了让"当前生效的 key"始终是单一事实——每次要发请求都去翻磁盘，
	// 就会有人在自己的分支里读 cfg.LLM.APIKey 而忘了问一句"这把是发给谁的"。
	APIKeyScope string
	Temperature float64
	MaxTokens   int
	TimeoutSecs int
	// ContextWindow 模型的上下文窗口（token）。0 = 用内置兜底表，查不到再用默认值
	// （见 llm.ContextWindowFor）。它只影响**占用水位的分母**，不影响实际请求——
	// 但分母错了，界面上那个百分比就是假的，所以宁可让用户能覆盖。
	ContextWindow int
}

// DefaultMaxSteps 单个计划的步骤数上限兜底值。
//
// 32 这个数的依据（实测，不是拍的）：16 条评测用例 × 8 轮 + 6 个宽泛目标 × 3 轮，
// 共 146 次观测，模型自然收敛到的最大步数是 10（宽泛的 broad-refactor），
// 16 条用例里最大只有 7。也就是说原值 12 从未真正触发过，**余量只有 2 步**——
// 一旦某个目标多拆两步就会被硬拒，而拒绝的形态是"计划校验失败"，
// 用户看到的是任务失败而不是"步骤太多"。
//
// 抬到 32 既留足余量，又仍然挡得住真正的失控（自然最大值的 3 倍以上）。
// 注意真正的硬约束往往不是它：宽泛目标下先撞上的是输出 token 预算
// （max_tokens 4096 时 JSON 会被截断成"未闭合"），调这个值之前先确认那一层。
//
// 定义在这里而不是在 planner 里各写一份：原先 config 与 planner 各有一个字面量 12，
// 改一处忘一处就会变成两份边界（planner 那份是 MaxSteps<=0 时的兜底）。
const DefaultMaxSteps = 32

type AgentConfig struct {
	MaxReplans        int  // 反思后自动重规划次数上限
	MaxSteps          int  // 单个计划步骤数上限（规划校验层）；0 表示用 DefaultMaxSteps
	StepTimeoutSecs   int  // 单步工具调用超时
	StepRetries       int  // 步骤超时后的自动重试次数（超时后重试或跳过）
	DoneThreshold     int  // 完成度评分达标线（0-100）
	MaxConcurrency    int  // 并行步骤并发上限
	ReflectEachStep   bool // 是否每步执行后都调用反思器
	ContextCompress   bool // 上下文自动压缩（超出短期窗口的对话摘要存储）
	SkillAutoOptimize bool // 技能运行失败后自动优化参数并保存新版本
	GEOEnabled        bool // 创作产出自动做生成式引擎优化（GEO）分析并给出建议
	DedupeCalls       bool // 低价值调用治理：复用重复的只读调用、跳过已知失败的重复调用
	// 单次任务预算（0 表示不限）：超出时停下并征求用户确认，避免"跑飞"烧钱
	MaxLLMCallsPerTask  int // 模型调用次数上限
	MaxTokensPerTask    int // token 上限
	MaxTaskDurationSecs int // 任务时长上限（秒）
	// StuckThreshold 同一「工具+参数」在多少轮里重复出现即判定原地打转（0 表示不检测）
	StuckThreshold int
	// MaxOutputRunes 单条工具输出注入下游（引用替换/反思/记忆）时的字符预算（0 表示不限）
	MaxOutputRunes int
	// MaxToolSchemas 单次规划最多注入多少个工具的完整 schema；0 表示不限（全量注入）。
	// 工具一多，全量 schema 既烧 token 又让模型挑不清，超限时改为「能力菜单 + 按需筛选」。
	MaxToolSchemas int
	// ChatAcceptance 对话模式回答后由辅助模型做一次独立自检（默认开）。
	// 对话模式不经规划/执行/反思，原本无条件报满分；开启后改为独立判定，
	// 判定失败或不可用时放行，不阻断对话。仅在配了 fast_model 时生效。
	ChatAcceptance bool
}

type SafetyConfig struct {
	Mode                string            // auto | plan_first | interactive
	ApprovalTimeoutSecs int               // 审批等待超时，超时视为拒绝
	TrustedPaths        []string          // 信任路径：中风险文件操作自动放行
	TrustedTools        []string          // 信任工具：直接放行（如 file.list）
	ToolPermissions     map[string]string // 工具权限覆盖：readonly | user_approved | full_access
	AIReview            bool              // 轻量扫描：执行前用辅助模型快筛中高风险动作（仅配了 fast_model 时生效）
	DeepReview          bool              // 深度扫描：执行前把整段计划交给主模型审一遍（每条任务一次）
	AllowPrivateWeb     bool              // 允许 web.fetch 访问本机/内网地址（默认拒绝，防 SSRF）
}

type MemoryConfig struct {
	ShortTermCap int // 短期记忆环缓冲轮数
	VectorDim    int // 自研向量索引维度
	MaxItems     int // 长期记忆条目上限（超出淘汰最旧）
	// ProjectScope 打开后，任务沉淀的记忆带上当时的工作区，检索只回本工作区 + 全局的。
	// 关着 = 全局共享（历史行为）。
	ProjectScope bool
}

type SchedulerConfig struct {
	Enabled bool
}

type PersonaConfig struct {
	Name  string // 助手名称（用于自我介绍）
	Style string // rigorous | gentle | efficient
}

type MCPServerConfig struct {
	Name    string
	Command string
	Args    []string
	// Env 传给子进程的环境变量（在继承的父环境之上追加）。
	// 为什么必须有：官方目录里大量服务器靠 API_KEY 这类变量拿凭据，没有它就只能
	// 把那些条目标成"装不了"——用户看到的目录有一多半点不动。
	// **值是明文存在配置里的**，与 command/args 同一层；密钥类的另存 credentials.json
	// 是下一步的事，这一版不假装做到了。
	Env map[string]string
	// URL 非空 = 远端 streamable-http 服务器（不起进程）；此时 Command/Args/Env 都不用。
	// 远端意味着**一条出网连接**：主机要出现在「连接与出网」台账里（kind=mcp.remote）。
	URL string
	// Headers 远端请求头，凭据（Authorization 之类）走这里。
	// **明文存在配置里**：与 command/args 同一层；密钥挪进 credentials.json 是下一步的事，
	// 这一版不假装做到了。API 一律不回吐这些值（MCPList 只给个数）。
	Headers map[string]string
	Trust   string // readonly | user_approved | full_access，MCP 工具的权限级别
	Enabled bool
}

// Default 返回一份带默认值的配置。
func Default() *Config {
	return &Config{
		LLM: LLMConfig{
			Provider:    "glm",
			BaseURL:     "https://open.bigmodel.cn/api/paas/v4",
			Model:       "glm-5.3-flash",
			Temperature: 0.3,
			MaxTokens:   4096,
			TimeoutSecs: 60,
		},
		Agent: AgentConfig{
			MaxReplans:        2,
			MaxSteps:          DefaultMaxSteps,
			StepTimeoutSecs:   30,
			StepRetries:       1,
			DoneThreshold:     80,
			MaxConcurrency:    8,
			ReflectEachStep:   false,
			ContextCompress:   true,
			SkillAutoOptimize: true,
			GEOEnabled:        true,
			DedupeCalls:       true,
			// 预算熔断默认开启：单任务最多 40 次模型调用 / 30 万 token / 15 分钟，
			// 超出后停下征求用户确认，避免"跑飞"持续烧钱。0 表示不限。
			MaxLLMCallsPerTask:  40,
			MaxTokensPerTask:    300000,
			MaxTaskDurationSecs: 900,
			// 防打转：同一动作在 3 轮里重复出现就停，不再硬耗重规划次数。0 表示不检测。
			StuckThreshold: 3,
			// 大输出预算：单条工具输出注入下游最多带 6000 字，超出按「头+尾」截断。0 表示不限。
			MaxOutputRunes: 6000,
			// 工具数超过 12 个时改为能力菜单（当前内置 21 个，加 MCP 还会更多）。0 表示始终全量。
			MaxToolSchemas: 12,
			// 对话模式回答后做一次独立自检，避免"干活的自己报满分"。配了 fast_model 才生效。
			ChatAcceptance: true,
		},
		Safety: SafetyConfig{
			Mode:                "auto",
			ApprovalTimeoutSecs: 300,
			AIReview:            true,
		},
		Memory: MemoryConfig{
			ShortTermCap: 20,
			VectorDim:    256,
			MaxItems:     5000,
		},
		Scheduler: SchedulerConfig{Enabled: true},
		Persona:   PersonaConfig{Name: "Gleam", Style: "efficient"},
		Git:       GitConfig{BranchPrefix: "gleam/"},
		// worktree 一律默认关，包括数量上限：上限到了就要删东西，
		// 而"删掉你没看过的目录"不该是默认行为。MaxCount 为 0 表示不限制。
		Worktrees: WorktreeConfig{},
	}
}

// Load 读取 YAML 配置文件（可省略），应用默认值与环境变量覆盖。
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
		m, err := ParseYAML(string(data))
		if err != nil {
			return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
		}
		cfg.apply(m)
	}
	cfg.applyEnv()
	if cfg.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cfg.DataDir = filepath.Join(home, ".gleam")
	}
	// 工作区**不在这里**兜底成 CWD：那是 CLI 的便利默认，由 buildRuntime 决定要不要加。
	// 桌面应用反过来要的就是「没选过就不绑定」——把默认塞进 Load 会让上面这句话说不出口。
	return cfg, nil
}

// Apply 把 YAML 解析出的键值映射应用到配置（设置页运行时更新与覆盖层加载共用）。
func (c *Config) Apply(m map[string]any) { c.apply(m) }

// Snapshot 取一份「影响行为的关键配置」快照（字段取舍见 types.ConfigSnapshot 的说明）。
//
// 由 config 提供而不是让调用方自己拼：各拼一份，漏掉一项就是"快照看着有、其实不全"——
// 那种缺失不会报错，只会在复盘时给出一个错的结论。**取快照的唯一正确时机是任务启动时**：
// 任务跑到一半用户改了设置，后半段用的是新配置，而"这次任务是用什么跑的"必须以启动时为准，
// 否则快照描述的是一个从未完整生效过的配置。
//
// Tiers 是**深拷贝**：不拷贝的话快照与活配置共享同一个 map，用户改一次档位就会把
// 历史快照一起改掉——而快照存在的全部意义就是不跟着变。
func (c *Config) Snapshot() types.ConfigSnapshot {
	if c == nil {
		return types.ConfigSnapshot{}
	}
	tiers := make(map[string]string, len(c.LLM.Tiers))
	for k, v := range c.LLM.Tiers {
		tiers[k] = v
	}
	return types.ConfigSnapshot{
		Provider:        c.LLM.Provider,
		Model:           c.LLM.Model,
		FastModel:       c.LLM.FastModel,
		Tiers:           tiers,
		SafetyMode:      c.Safety.Mode,
		DoneThreshold:   c.Agent.DoneThreshold,
		MaxReplans:      c.Agent.MaxReplans,
		MaxSteps:        c.Agent.MaxSteps,
		StepTimeoutSecs: c.Agent.StepTimeoutSecs,
		StepRetries:     c.Agent.StepRetries,
		MaxConcurrency:  c.Agent.MaxConcurrency,
		DedupeCalls:     c.Agent.DedupeCalls,
		MaxOutputRunes:  c.Agent.MaxOutputRunes,
		MaxToolSchemas:  c.Agent.MaxToolSchemas,
		ReflectEachStep: c.Agent.ReflectEachStep,
	}
}

// OverlayFile 数据目录下设置覆盖层的文件名。
const OverlayFile = "settings.yaml"

// SaveOverlay 把可在线修改的设置子集持久化到覆盖层文件。
//
// 覆盖层里**没有 api_key**：settings.yaml 常被顺手截图、贴进 issue、同步进网盘，
// 密钥一旦进去就等于公开。它只住凭证文件（0600，Windows 上再经 DPAPI），
// 且按接入主机绑定，见 `internal/harness/credentials` 与 `internal/agent/llmkey.go`。
//
// ⚠ 这里是**全量快照**：不管用户有没有动过某个字段，都会写进去；MCP 列表更是故意
// "空列表也要写"（否则覆盖不掉 config.yaml 里的旧列表），tiers 也靠空表表达"清空"。
// 加载侧又是"覆盖层赢"，于是有一个不容易察觉的后果：
// **一旦在设置页保存过，所有默认值就被钉死在这份文件里，之后改 Default() 里的任何
// 默认值，对这台安装都不再生效。**
//
// 第八批改 DefaultMaxSteps 12→32 时就撞上了，而且**有两处**会把它压回去：
// 本机覆盖层 `~/.gleam/settings.yaml` 里的 `agent: max_steps: 12`，
// 以及仓库里的 `configs/config.yaml` 里的同一行（示例配置的值应当与 Default() 对齐）。
// 只清覆盖层是**看不出效果的**——主配置那行照样生效（实测 `/api/settings` 仍返回 12），
// 而症状是"删了、重启了、数字没变"，很容易被误判成缓存或没生效。
// 处置：两处都对齐到 32，或删掉覆盖层那一行让它回到默认。
//
// 没有改成"只写非默认字段"——那需要一张例外清单（tiers / mcp / workspace 的零值
// 都带"清空/覆盖"语义，不能省），清单本身比这个坑更脆。所以这里选择把坑写清楚：
// **改默认值之后，要顺手确认已有安装的 settings.yaml 有没有把旧值写进去。**
func (c *Config) SaveOverlay(path string) error {
	root := NewYMap()
	llm := NewYMap()
	// provider 不入覆盖层：避免 mock 会话保存设置后把 mock 写死
	llm.Set("protocol", c.LLM.Protocol)
	llm.Set("provider_id", c.LLM.ProviderID)
	llm.Set("plan", c.LLM.Plan)
	llm.Set("base_url", c.LLM.BaseURL)
	llm.Set("model", c.LLM.Model)
	llm.Set("fast_model", c.LLM.FastModel)
	if len(c.LLM.Tiers) > 0 {
		tiers := NewYMap()
		for name, model := range c.LLM.Tiers {
			tiers.Set(name, model)
		}
		llm.Set("tiers", tiers)
	}
	if len(c.LLM.Models) > 0 {
		modelsArr := make([]any, 0, len(c.LLM.Models))
		for _, m := range c.LLM.Models {
			mm := NewYMap()
			mm.Set("id", m.ID)
			mm.Set("name", m.Name)
			mm.Set("provider_id", m.ProviderID)
			mm.Set("protocol", m.Protocol)
			mm.Set("base_url", m.BaseURL)
			mm.Set("model", m.Model)
			mm.Set("plan", m.Plan)
			if m.APIKey != "" {
				mm.Set("api_key", m.APIKey)
			}
			mm.Set("is_default", m.IsDefault)
			mm.Set("is_fast", m.IsFast)
			modelsArr = append(modelsArr, mm)
		}
		llm.Set("models", modelsArr)
	}
	llm.Set("temperature", c.LLM.Temperature)
	llm.Set("max_tokens", c.LLM.MaxTokens)
	llm.Set("timeout_seconds", c.LLM.TimeoutSecs)
	llm.Set("context_window", c.LLM.ContextWindow)
	root.Set("llm", llm)

	agent := NewYMap()
	agent.Set("max_replans", c.Agent.MaxReplans)
	agent.Set("max_steps", c.Agent.MaxSteps)
	agent.Set("step_timeout_seconds", c.Agent.StepTimeoutSecs)
	agent.Set("step_retries", c.Agent.StepRetries)
	agent.Set("done_threshold", c.Agent.DoneThreshold)
	agent.Set("max_concurrency", c.Agent.MaxConcurrency)
	agent.Set("reflect_each_step", c.Agent.ReflectEachStep)
	agent.Set("context_compress", c.Agent.ContextCompress)
	agent.Set("skill_auto_optimize", c.Agent.SkillAutoOptimize)
	agent.Set("geo_enabled", c.Agent.GEOEnabled)
	agent.Set("dedupe_calls", c.Agent.DedupeCalls)
	agent.Set("max_llm_calls_per_task", c.Agent.MaxLLMCallsPerTask)
	agent.Set("max_tokens_per_task", c.Agent.MaxTokensPerTask)
	agent.Set("max_task_duration_seconds", c.Agent.MaxTaskDurationSecs)
	agent.Set("stuck_threshold", c.Agent.StuckThreshold)
	agent.Set("max_output_runes", c.Agent.MaxOutputRunes)
	agent.Set("max_tool_schemas", c.Agent.MaxToolSchemas)
	agent.Set("chat_acceptance", c.Agent.ChatAcceptance)
	root.Set("agent", agent)

	safety := NewYMap()
	safety.Set("mode", c.Safety.Mode)
	safety.Set("approval_timeout_seconds", c.Safety.ApprovalTimeoutSecs)
	safety.Set("ai_review", c.Safety.AIReview)
	safety.Set("deep_review", c.Safety.DeepReview)
	safety.Set("allow_private_web", c.Safety.AllowPrivateWeb)
	tpMap := NewYMap()
	for name, perm := range c.Safety.ToolPermissions {
		tpMap.Set(name, perm)
	}
	safety.Set("tool_permissions", tpMap)
	root.Set("safety", safety)

	mem := NewYMap()
	mem.Set("short_term_capacity", c.Memory.ShortTermCap)
	mem.Set("vector_dim", c.Memory.VectorDim)
	mem.Set("max_items", c.Memory.MaxItems)
	mem.Set("project_scope", c.Memory.ProjectScope)
	root.Set("memory", mem)

	gitc := NewYMap()
	gitc.Set("branch_prefix", c.Git.BranchPrefix)
	gitc.Set("force_push", c.Git.ForcePush)
	gitc.Set("commit_instructions", c.Git.CommitInstructions)
	root.Set("git", gitc)

	wt := NewYMap()
	wt.Set("enabled", c.Worktrees.Enabled)
	wt.Set("fetch_before_create", c.Worktrees.FetchBeforeCreate)
	wt.Set("auto_delete", c.Worktrees.AutoDelete)
	wt.Set("max_count", c.Worktrees.MaxCount)
	root.Set("worktrees", wt)

	persona := NewYMap()
	persona.Set("name", c.Persona.Name)
	persona.Set("style", c.Persona.Style)
	root.Set("persona", persona)

	sched := NewYMap()
	sched.Set("enabled", c.Scheduler.Enabled)
	root.Set("scheduler", sched)

	netw := NewYMap()
	netw.Set("proxy_mode", c.Network.ProxyModeOrDefault())
	netw.Set("proxy_url", c.Network.ProxyURL)
	root.Set("network", netw)

	// 工作区（桌面端选定的任务文件夹）与最近列表
	root.Set("workspace", c.Workspace)
	wsArr := make([]any, 0, len(c.WorkspaceRecents))
	for _, w := range c.WorkspaceRecents {
		wsArr = append(wsArr, w)
	}
	root.Set("workspace_recents", wsArr)

	// MCP 服务器列表（市场安装/自定义安装/删除均持久化到覆盖层；空列表也要写，覆盖 config.yaml）
	mcpArr := make([]any, 0, len(c.MCP))
	for _, s := range c.MCP {
		sm := NewYMap()
		sm.Set("name", s.Name)
		sm.Set("command", s.Command)
		args := make([]any, 0, len(s.Args))
		for _, a := range s.Args {
			args = append(args, a)
		}
		sm.Set("args", args)
		sm.Set("trust", s.Trust)
		sm.Set("enabled", s.Enabled)
		mcpArr = append(mcpArr, sm)
	}
	root.Set("mcp", mcpArr)

	data, err := MarshalYAML(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o644)
}

// LoadOverlay 若覆盖层文件存在则应用到 cfg（启动时恢复用户在设置页保存的值）。
// 文件不存在不算错误。
func LoadOverlay(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	m, err := ParseYAML(string(data))
	if err != nil {
		// 覆盖层损坏不阻断启动：备份后忽略
		_ = os.Rename(path, path+".corrupt")
		return nil
	}
	cfg.apply(m)
	return nil
}

func (c *Config) apply(m map[string]any) {
	if v, ok := sub(m, "llm"); ok {
		getStr(v, "provider", &c.LLM.Provider)
		getStr(v, "protocol", &c.LLM.Protocol)
		getStr(v, "provider_id", &c.LLM.ProviderID)
		getStr(v, "plan", &c.LLM.Plan)
		getStrAssign(v, "base_url", &c.LLM.BaseURL)
		getStr(v, "model", &c.LLM.Model)
		getStrAssign(v, "fast_model", &c.LLM.FastModel)
		if tiers, ok := v["tiers"].(map[string]any); ok {
			c.LLM.Tiers = map[string]string{}
			for name, model := range tiers {
				name = strings.TrimSpace(name)
				s, _ := model.(string)
				s = strings.TrimSpace(s)
				// 名字或模型为空白的档位直接丢弃：留着只会变成一个"指向空模型"的坑
				if name != "" && s != "" {
					c.LLM.Tiers[name] = s
				}
			}
		}
		if raw, ok := v["models"]; ok {
			if arr, ok := raw.([]any); ok {
				c.LLM.Models = c.LLM.Models[:0]
				for _, item := range arr {
					sm, ok := item.(map[string]any)
					if !ok {
						continue
					}
					m := ModelEntry{}
					getStr(sm, "id", &m.ID)
					getStr(sm, "name", &m.Name)
					getStr(sm, "provider_id", &m.ProviderID)
					getStr(sm, "protocol", &m.Protocol)
					getStrAssign(sm, "base_url", &m.BaseURL)
					getStr(sm, "model", &m.Model)
					getStr(sm, "plan", &m.Plan)
					getStrAssign(sm, "api_key", &m.APIKey)
					getBool(sm, "is_default", &m.IsDefault)
					getBool(sm, "is_fast", &m.IsFast)
					if m.Name != "" && m.Model != "" {
						c.LLM.Models = append(c.LLM.Models, m)
					}
				}
			}
		}
		getStrAssign(v, "api_key", &c.LLM.APIKey)
		getFloat(v, "temperature", &c.LLM.Temperature)
		getInt(v, "max_tokens", &c.LLM.MaxTokens)
		getInt(v, "timeout_seconds", &c.LLM.TimeoutSecs)
		getInt(v, "context_window", &c.LLM.ContextWindow)
	}
	if v, ok := sub(m, "agent"); ok {
		getInt(v, "max_replans", &c.Agent.MaxReplans)
		getInt(v, "max_steps", &c.Agent.MaxSteps)
		getInt(v, "step_timeout_seconds", &c.Agent.StepTimeoutSecs)
		getInt(v, "step_retries", &c.Agent.StepRetries)
		getInt(v, "done_threshold", &c.Agent.DoneThreshold)
		getInt(v, "max_concurrency", &c.Agent.MaxConcurrency)
		getBool(v, "reflect_each_step", &c.Agent.ReflectEachStep)
		getBool(v, "context_compress", &c.Agent.ContextCompress)
		getBool(v, "skill_auto_optimize", &c.Agent.SkillAutoOptimize)
		getBool(v, "geo_enabled", &c.Agent.GEOEnabled)
		getBool(v, "dedupe_calls", &c.Agent.DedupeCalls)
		getInt(v, "max_llm_calls_per_task", &c.Agent.MaxLLMCallsPerTask)
		getInt(v, "max_tokens_per_task", &c.Agent.MaxTokensPerTask)
		getInt(v, "max_task_duration_seconds", &c.Agent.MaxTaskDurationSecs)
		getInt(v, "stuck_threshold", &c.Agent.StuckThreshold)
		getInt(v, "max_output_runes", &c.Agent.MaxOutputRunes)
		getInt(v, "max_tool_schemas", &c.Agent.MaxToolSchemas)
		getBool(v, "chat_acceptance", &c.Agent.ChatAcceptance)
	}
	if v, ok := sub(m, "safety"); ok {
		getStr(v, "mode", &c.Safety.Mode)
		getInt(v, "approval_timeout_seconds", &c.Safety.ApprovalTimeoutSecs)
		getBool(v, "ai_review", &c.Safety.AIReview)
		getBool(v, "deep_review", &c.Safety.DeepReview)
		getBool(v, "allow_private_web", &c.Safety.AllowPrivateWeb)
		getStrs(v, "trusted_paths", &c.Safety.TrustedPaths)
		getStrs(v, "trusted_tools", &c.Safety.TrustedTools)
		if tp, ok := v["tool_permissions"].(map[string]any); ok {
			c.Safety.ToolPermissions = map[string]string{}
			for name, perm := range tp {
				if ps, ok := perm.(string); ok {
					c.Safety.ToolPermissions[name] = ps
				}
			}
		}
	}
	if v, ok := sub(m, "memory"); ok {
		getInt(v, "short_term_capacity", &c.Memory.ShortTermCap)
		getInt(v, "vector_dim", &c.Memory.VectorDim)
		getInt(v, "max_items", &c.Memory.MaxItems)
		getBool(v, "project_scope", &c.Memory.ProjectScope)
	}
	if v, ok := sub(m, "scheduler"); ok {
		getBool(v, "enabled", &c.Scheduler.Enabled)
	}
	if v, ok := sub(m, "persona"); ok {
		getStr(v, "name", &c.Persona.Name)
		getStr(v, "style", &c.Persona.Style)
	}
	if v, ok := sub(m, "git"); ok {
		getStr(v, "branch_prefix", &c.Git.BranchPrefix)
		getBool(v, "force_push", &c.Git.ForcePush)
		getStr(v, "commit_instructions", &c.Git.CommitInstructions)
	}
	if v, ok := sub(m, "worktrees"); ok {
		getBool(v, "enabled", &c.Worktrees.Enabled)
		getBool(v, "fetch_before_create", &c.Worktrees.FetchBeforeCreate)
		getBool(v, "auto_delete", &c.Worktrees.AutoDelete)
		getInt(v, "max_count", &c.Worktrees.MaxCount)
	}
	if v, ok := sub(m, "network"); ok {
		getStr(v, "proxy_mode", &c.Network.ProxyMode)
		getStr(v, "proxy_url", &c.Network.ProxyURL)
	}
	if raw, ok := m["mcp"]; ok {
		if arr, ok := raw.([]any); ok {
			c.MCP = c.MCP[:0]
			for _, item := range arr {
				sm, ok := item.(map[string]any)
				if !ok {
					continue
				}
				srv := MCPServerConfig{Trust: "user_approved", Enabled: true}
				getStr(sm, "name", &srv.Name)
				getStr(sm, "command", &srv.Command)
				getStrs(sm, "args", &srv.Args)
				getStr(sm, "trust", &srv.Trust)
				if b, ok := sm["enabled"].(bool); ok {
					srv.Enabled = b
				}
				if srv.Name != "" && srv.Command != "" {
					c.MCP = append(c.MCP, srv)
				}
			}
		}
	}
	getStr(m, "data_dir", &c.DataDir)
	getStr(m, "workspace", &c.Workspace)
	getStrs(m, "workspace_recents", &c.WorkspaceRecents)
}

func (c *Config) applyEnv() {
	if v := os.Getenv("GLEAM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
	if v := os.Getenv("GLEAM_LLM_BASE_URL"); v != "" {
		c.LLM.BaseURL = v
	}
	if v := os.Getenv("GLEAM_LLM_MODEL"); v != "" {
		c.LLM.Model = v
	}
	if os.Getenv("GLEAM_MOCK_LLM") == "1" {
		c.LLM.Provider = "mock"
	}
	if v := os.Getenv("GLEAM_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("GLEAM_WORKSPACE"); v != "" {
		c.Workspace = v
	}
	if v := os.Getenv("GLEAM_SAFETY_MODE"); v != "" {
		c.Safety.Mode = v
	}
}

// ---------- map 访问助手（容忍 int64/float64/bool 混合） ----------

func sub(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	sm, ok := v.(map[string]any)
	return sm, ok
}

func getStr(m map[string]any, key string, dst *string) {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			*dst = s
		}
	}
}

// getStrAssign 与 getStr 的差别只在"空串也赋值"：
// base_url / fast_model / api_key 带显式清空语义（设置页留空=回落预设/回退主模型/删密钥），
// 用 getStr 的话空串会被跳过，清空静默失效。
func getStrAssign(m map[string]any, key string, dst *string) {
	if v, ok := m[key].(string); ok {
		*dst = v
	}
}

func getInt(m map[string]any, key string, dst *int) {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case int64:
			*dst = int(n)
		case int:
			*dst = n
		case float64:
			*dst = int(n)
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				*dst = i
			}
		}
	}
}

func getFloat(m map[string]any, key string, dst *float64) {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			*dst = n
		case int64:
			*dst = float64(n)
		case int:
			*dst = float64(n)
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
				*dst = f
			}
		}
	}
}

func getBool(m map[string]any, key string, dst *bool) {
	if v, ok := m[key].(bool); ok {
		*dst = v
	}
}

func getStrs(m map[string]any, key string, dst *[]string) {
	v, ok := m[key]
	if !ok {
		return
	}
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok && s != "" {
			*dst = []string{s}
		}
		return
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		*dst = out
	}
}
