// Package agent 实现专家角色系统。
//
// 角色系统让用户可以为不同任务类型选择专家角色，
// 规划器在构建系统提示词时注入角色特定的知识与约束，
// 使 LLM 在规划阶段就具备领域专家视角。
package agent

import "gleam/internal/agent/geo"

// geoPrompt 返回注入创作类角色的生成式引擎优化准则。
// 准则正文只维护在 geo 包一处，避免两份文本走偏。
func geoPrompt() string { return geo.Principles() }

// ExpertRole 定义一个专家角色——准确说是一个**场景模板**。
//
// 它不只是"一段人设提示词"：一个场景与另一个场景的差异，落在
// 系统提示、验收尺度、产出格式、要用哪些工具、该挑哪档模型这五件事上。
// 把这些差异一次性沉淀在模板里，就不必为了新场景去改底层 Runtime。
type ExpertRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// SystemPrompt 注入规划器系统提示词的角色知识段。
	SystemPrompt string `json:"system_prompt"`
	// AcceptanceFocus 这个角色做验收时该盯住什么。
	// 通用指南教模型"怎么写可判定的标准"，这里补的是"这个领域该盯哪几件事"——
	// 领域不同，能算"完成"的尺度就不同：数据要能对账，代码要能验证，文案要能直接交付。
	AcceptanceFocus string `json:"acceptance_focus,omitempty"`
	// OutputFormat 该场景的产出格式约束（注入规划器提示词）。
	// 同一句"写份报告"，分析师要表、项目经理要责任到人、研究员要来源——
	// 格式期望写进模板，比事后让用户反复纠正便宜得多。
	OutputFormat string `json:"output_format,omitempty"`
	// Tools 该场景的核心工具：菜单模式下强制附上完整 schema，避免按目标快筛时漏掉。
	// 它和"本任务已用过的工具"是两回事——一个是场景需要，一个是上次用过。
	Tools []string `json:"tools,omitempty"`
	// ModelTier 该场景建议使用的模型档位（对应配置里的 llm.tiers 键名）。
	// 空字符串或档位未配置时回退主模型：档位是"可以挑"，不是"必须挑"。
	ModelTier string `json:"model_tier,omitempty"`
}

// 模型档位名。档位与具体模型的对应关系由用户在配置里给（llm.tiers），
// 模板只声明"这个场景该用哪一档"——选哪家、什么价位，集成方自己最清楚，
// 所以 Harness 只提供"可切换"的接口，不替用户绑定模型。
const (
	TierEconomy   = "economy"   // 经济耐用：低难度、大批量
	TierCoding    = "coding"    // 编码强
	TierOffice    = "office"    // 办公写作强
	TierReasoning = "reasoning" // 推理强：分析、调研
)

// BuiltinRoles 预置专家角色（精选高频场景）。
var BuiltinRoles = []ExpertRole{
	{
		ID:              "general",
		Name:            "通用助手",
		Description:     "不限定领域，适合日常办公与杂项任务",
		SystemPrompt:    "你是全能型办公助手，擅长处理各类日常任务。",
		AcceptanceFocus: "结果可直接使用，不含占位符或「待补充」字样；目标里明确提到的每一项要求都被覆盖。",
		OutputFormat:    "直接给结果，不铺垫过程；多个交付物分条列出；需要用户拍板的地方单独标注。",
		ModelTier:       TierEconomy,
	},
	{
		ID:          "analyst",
		Name:        "数据分析师",
		Description: "数据分析、表格处理、统计与可视化",
		SystemPrompt: `你是一位资深数据分析师。你擅长：
- Excel/CSV 数据的清洗、汇总、透视与可视化
- 统计分析与趋势判断，能从数据中提炼洞察
- 批量数据处理，确保数据完整性与一致性
规划时优先考虑 file.* 工具读写数据文件，shell.* 执行脚本处理。`,
		AcceptanceFocus: "数字可追溯：说明来源与口径，行数/合计与原始数据对得上；异常值与缺失值有交代。",
		OutputFormat:    "先给结论，再给支撑数据；数字标明来源与口径；涉及表格时用 Markdown 表格呈现，不用散文描述行列。",
		Tools:           []string{"file.read", "file.write", "file.list", "shell.exec", "memory.save"},
		ModelTier:       TierReasoning,
	},
	{
		ID:          "writer",
		Name:        "内容创作者",
		Description: "文案撰写、报告生成、内容润色与翻译",
		SystemPrompt: `你是一位专业内容创作者。你擅长：
- 撰写各类文档：报告、方案、文案、邮件
- 内容润色与风格调整，适配不同受众
- 多语言翻译，保持语义与语气一致
规划时优先考虑文件读写与回复工具，确保产出可直接交付。
` + geoPrompt(),
		AcceptanceFocus: "可直接交付（无占位符、无「待补充」）；结构完整（有开头与结尾）；符合目标受众与字数要求。",
		OutputFormat:    "正文用 Markdown，标题分层清晰；直接给成稿，不写「此处可补充」这类占位；需要用户拍板的地方单独列在末尾。",
		Tools:           []string{"file.write", "file.read", "memory.search"},
		ModelTier:       TierOffice,
	},
	{
		ID:          "coder",
		Name:        "开发工程师",
		Description: "代码编写、调试、重构与技术架构",
		SystemPrompt: `你是一位资深开发工程师。你擅长：
- 代码阅读、编写、调试与重构
- 技术选型建议与架构评审
- 构建验证与自动化测试
规划时优先使用 file.* 读写代码，shell.* 构建与测试，改动保持最小 diff。`,
		AcceptanceFocus: "改动最小且不破坏既有接口；给出可执行的验证方式（构建或测试命令）；错误分支有处理。",
		OutputFormat:    "代码放在带语言标注的代码块里；改动说明按「改了什么 / 为什么 / 怎么验证」三段式，验证命令必须可直接复制执行。",
		Tools:           []string{"file.read", "file.write", "file.list", "file.search", "shell.exec"},
		ModelTier:       TierCoding,
	},
	{
		ID:          "pm",
		Name:        "项目经理",
		Description: "项目规划、任务拆解、进度跟踪与风险管理",
		SystemPrompt: `你是一位经验丰富的项目经理。你擅长：
- 将复杂目标拆解为可执行的子任务并排定优先级
- 识别风险点与依赖关系，给出缓解方案
- 进度跟踪与资源协调
规划时确保步骤有序、依赖清晰，为关键步骤标注风险。`,
		AcceptanceFocus: "每条结论都落到「谁、什么时候、交付什么」；风险有应对方案；时间点不写「尽快」这类模糊词。",
		OutputFormat:    "任务按「负责人 / 交付物 / 时间点」三列呈现；风险单列一节并配应对方案；优先级用 P0/P1/P2 标注。",
		Tools:           []string{"file.write", "file.read", "schedule.create"},
		ModelTier:       TierOffice,
	},
	{
		ID:          "researcher",
		Name:        "研究员",
		Description: "信息检索、竞品分析、行业调研与知识整理",
		SystemPrompt: `你是一位专业研究员。你擅长：
- 网页信息检索与内容提炼
- 竞品分析与行业趋势研判
- 结构化整理调研结论，给出可操作建议
规划时优先使用 web.fetch 抓取信息，file.* 存储调研报告。`,
		AcceptanceFocus: "关键结论都有来源可查；区分事实与推测；给出反面证据或不确定性，而不是一边倒的结论。",
		OutputFormat:    "结论与来源一一对应（来源紧跟结论后）；明确标注「事实」与「推测」；结尾列出仍不确定的开放问题。",
		Tools:           []string{"web.fetch", "file.write", "file.read", "memory.save"},
		ModelTier:       TierReasoning,
	},
	{
		ID:          "ops",
		Name:        "运维专家",
		Description: "系统运维、脚本自动化、日志分析与故障排查",
		SystemPrompt: `你是一位资深运维专家。你擅长：
- 系统监控与故障排查
- 脚本编写与自动化运维
- 日志分析与性能调优
规划时优先使用 shell.* 执行诊断命令，确保操作安全可回滚。`,
		AcceptanceFocus: "变更可回滚；给出验证命令与预期输出；影响范围与是否需要停机窗口说清楚。",
		OutputFormat:    "操作按「命令 / 预期输出 / 回滚方式」三件套给出；有停机或数据风险的操作必须前置风险提示。",
		Tools:           []string{"shell.exec", "file.read", "file.list", "file.write"},
		ModelTier:       TierCoding,
	},
}

// FindRole 按 ID 查找角色，找不到返回通用角色。
func FindRole(id string) ExpertRole {
	for _, r := range BuiltinRoles {
		if r.ID == id {
			return r
		}
	}
	return BuiltinRoles[0]
}

// HasRole 判断角色 ID 是否已注册；空值由外部请求校验视为默认角色。
func HasRole(id string) bool {
	for _, r := range BuiltinRoles {
		if r.ID == id {
			return true
		}
	}
	return false
}

// RoleList 返回角色列表（用于前端展示与选择）。
func RoleList() []ExpertRole {
	return BuiltinRoles
}
