package market

import "gleam/pkg/types"

// extraSkills 技能目录的第二批：让"更多种类"真的成立。
//
// 两条写法并存，都是**能跑**的：
//   - **步骤式**：每步调一个内置工具（file.* / memory.* / web.fetch …），确定性、可审计；
//   - **提示词式**：一步 `prompt.run` 把技能正文交给模型。这一步的权限与模型调用同级
//     （只读放行），理由见 internal/tools/prompt。
//
// 为什么不留 placeholder 提示词：技能被装进本机后就是可编辑的文件，写得含糊等于把
// "怎么用"留给用户猜。这里的提示词都写了**约束**（不许编造、不确定要标注），
// 因为这些约束正是质量差异所在。
//
// 用 init 追加而不是把目录拆成两半：`SkillCatalog` 仍是那一个 owner
// （search/find 都读它），第二批只是内容变多，不该多引一个"来源"概念。
func init() {
	SkillCatalog = append(SkillCatalog, extraSkills...)
}

var extraSkills = []SkillPreset{
	{
		Name: "readme-writer", Category: "文档写作",
		Description: "读一眼目录结构，写出这个项目的 README（用途 / 安装 / 用法 / 目录说明）",
		Params:      []string{"dir"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.list", Args: map[string]any{"path": "{{dir}}"}},
			{ID: "s2", Tool: "prompt.run", Args: map[string]any{
				"system": "你写 README，只依据给出的文件清单，不臆造未列出的功能或命令。",
				"prompt": "下面是一个目录的文件清单。写一份 README：\n" +
					"1. 一句话说明这个项目是做什么的（依据文件名与结构推断，拿不准就写得更保守）；\n" +
					"2. 安装与运行（没有线索就写「待补充」，不要编）；\n" +
					"3. 目录结构说明（按顶层条目逐条）。\n用 Markdown。",
				"input": "{{s1.entries}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "file.write", Args: map[string]any{"path": "{{dir}}/README.md", "content": "{{s2.text}}"}, DependsOn: []string{"s2"}},
			{ID: "s4", Tool: "reply", Args: map[string]any{"text": "README 已写入 {{dir}}/README.md"}, DependsOn: []string{"s3"}},
		},
		Tags: []string{"文档", "README", "项目"},
	},
	{
		Name: "doc-polish", Category: "文档写作",
		Description: "把草稿润色成成稿：去冗余、补层次，不新增原文没有的事实",
		Params:      []string{"draft"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你是中文技术编辑。只动表达，不添加原文没有的事实。",
				"prompt": "把下面的草稿改写成成稿：\n1. 去掉重复与口号式表述；\n2. 给段落一个能读的层次；\n" +
					"3. 保留全部事实与原意。\n直接给出成稿，不要解释你改了什么。",
				"input": "{{draft}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"写作", "润色", "编辑"},
	},
	{
		Name: "changelog-writer", Category: "文档写作",
		Description: "把一份改动清单写成给人的变更日志（分类、只写用户看得见的变化）",
		Params:      []string{"changes"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你写面向用户的变更日志：只写用户能感知的变化，内部重构不影响行为就不写。",
				"prompt": "把下面的改动清单整理成变更日志：按「新增 / 修复 / 调整」分组，每条一句话，动词开头，不出现文件路径。",
				"input":  "{{changes}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"变更日志", "发布", "文档"},
	},
	{
		Name: "todo-extract", Category: "效率办公",
		Description: "从一段杂乱的文字里抽出待办项，标出负责人与期限（原文有才标）",
		Params:      []string{"text"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你从文字里抽待办。原文没写负责人或期限就不要编，留空。",
				"prompt": "从下面的文字里抽出待办项，输出 Markdown 清单：每条形如「- [ ] 事项（负责人：X；期限：Y）」，" +
					"没有负责人/期限的就不写那一段。只输出清单。",
				"input": "{{text}}"}},
			{ID: "s2", Tool: "memory.save", Args: map[string]any{"content": "待办清单：{{s1.text}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"待办", "清单", "整理"},
	},
	{
		Name: "meeting-notes", Category: "效率办公",
		Description: "把会议速记整理成「结论 / 待办 / 悬而未决」三栏",
		Params:      []string{"notes"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你整理会议记录。区分「已达成的结论」与「只是讨论过」，不要替会议做决定。",
				"prompt": "把下面的速记整理成三栏：\n## 结论（会上明确同意的）\n## 待办（谁做什么，有就写）\n" +
					"## 悬而未决（讨论了但没定）\n没有内容的栏写「无」。只输出这三栏。",
				"input": "{{notes}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"会议", "纪要", "整理"},
	},
	{
		Name: "daily-brief", Category: "效率办公",
		Description: "把工作区的文件与近期记忆汇总成一份开工早报",
		Params:      []string{"dir"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.list", Args: map[string]any{"path": "{{dir}}"}},
			{ID: "s2", Tool: "memory.search", Args: map[string]any{"query": "待办"}},
			{ID: "s3", Tool: "prompt.run", Args: map[string]any{
				"system": "你写简短早报。只依据给的材料，素材不足就直说，不要凑。",
				"prompt": "依据下面的目录清单与记忆检索结果，写一份不超过 8 行的早报：今天可能要先看的文件、" +
					"记忆里提到的待办。",
				"input": "目录：{{s1.entries}}\n记忆：{{s2.hits}}"}, DependsOn: []string{"s1", "s2"}},
			{ID: "s4", Tool: "reply", Args: map[string]any{"text": "{{s3.text}}"}, DependsOn: []string{"s3"}},
		},
		Tags: []string{"早报", "汇总", "工作区"},
	},
	{
		Name: "commit-message", Category: "代码开发",
		Description: "读一份改动说明，写一条讲清「为什么」的提交说明",
		Params:      []string{"changes"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你写提交说明。标题不超过 50 字、动词开头，说清「为什么」多于「改了什么」。",
				"prompt": "依据下面的改动说明写一条提交说明：第一行标题，空一行后是正文。只输出提交说明本身。",
				"input":  "{{changes}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"git", "提交", "开发"},
	},
	{
		Name: "code-explain", Category: "代码开发",
		Description: "解释一个文件在做什么：入口、数据流、外部依赖、容易看错的点",
		Params:      []string{"path"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.read", Args: map[string]any{"path": "{{path}}"}},
			{ID: "s2", Tool: "prompt.run", Args: map[string]any{
				"system": "你解释代码给接手的人听。不确定的地方说「看起来像是」，不要编造行为。",
				"prompt": "解释这份代码：\n1. 它是干什么的（两三句）；\n2. 入口与主要数据流；\n" +
					"3. 依赖了什么外部东西；\n4. 容易看错或改坏的地方。",
				"input": "{{s1.content}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "{{s2.text}}"}, DependsOn: []string{"s2"}},
		},
		Tags: []string{"代码", "解释", "接手"},
	},
	{
		Name: "test-case-draft", Category: "代码开发",
		Description: "给一个文件写测试用例草稿（正常路径 / 边界 / 该失败的情况）",
		Params:      []string{"path"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.read", Args: map[string]any{"path": "{{path}}"}},
			{ID: "s2", Tool: "prompt.run", Args: map[string]any{
				"system": "你写测试用例。只依据给出的代码；测不到的地方要说明，不要假装覆盖。",
				"prompt": "为这份代码写测试用例草稿，分三组：正常路径 / 边界 / 应当失败的情况。" +
					"每条写清输入与预期结果。最后列出「这份代码里我读不出怎么测的地方」。",
				"input": "{{s1.content}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "{{s2.text}}"}, DependsOn: []string{"s2"}},
		},
		Tags: []string{"测试", "用例", "开发"},
	},
	{
		Name: "diff-summary", Category: "代码评审",
		Description: "把一份 diff 总结成「改了什么 / 可能影响什么 / 哪里要人再看一眼」",
		Params:      []string{"diff"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你审 diff。区分「看得出的结论」与「需要确认的猜测」，后者必须明说。",
				"prompt": "读这份 diff，输出：\n## 改了什么（按意图分组，不逐行复述）\n## 可能影响（行为、接口、数据）\n" +
					"## 要人再看一眼的地方（含为什么）\n拿不准的写进第三栏，不要塞进第一栏。",
				"input": "{{diff}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"评审", "diff", "代码"},
	},
	{
		Name: "review-checklist", Category: "代码评审",
		Description: "按固定清单过一遍改动：正确性 / 边界 / 错误处理 / 可读性 / 测试",
		Params:      []string{"diff"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你按清单评审，每项只给「过 / 有问题 / 无法判断」三态，不许含糊。",
				"prompt": "对下面的改动逐项给结论：\n1. 正确性\n2. 边界（空、超大、并发、异常输入）\n" +
					"3. 错误处理（失败时会发生什么）\n4. 可读性\n5. 测试（有没有对应的验证）\n" +
					"每项格式 `- [结论] 一句话理由`。最后给「必须改的」清单，只放真的必须改的。",
				"input": "{{diff}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"评审", "清单", "质量"},
	},
	{
		Name: "secret-scan-notes", Category: "安全与测试",
		Description: "在工作区里找可疑的明文凭据，给出位置与处置建议",
		Params:      []string{"dir"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.search", Args: map[string]any{"path": "{{dir}}", "query": "api_key"}},
			{ID: "s2", Tool: "file.search", Args: map[string]any{"path": "{{dir}}", "query": "token"}},
			{ID: "s3", Tool: "prompt.run", Args: map[string]any{
				"system": "你做凭据排查。**不要在回答里复述密钥原文**，只说位置与处置建议。",
				"prompt": "下面是两轮关键词搜索的结果。列出可疑的明文凭据：位置（文件:行）+ 类型 + " +
					"处置建议（改环境变量 / 移入密钥库 / 轮换）。原文里的密钥值一律用 ███ 代替。",
				"input": "api_key：{{s1.matches}}\ntoken：{{s2.matches}}"}, DependsOn: []string{"s1", "s2"}},
			{ID: "s4", Tool: "reply", Args: map[string]any{"text": "{{s3.text}}"}, DependsOn: []string{"s3"}},
		},
		Tags: []string{"安全", "凭据", "排查"},
	},
	{
		Name: "hardening-review", Category: "安全与测试",
		Description: "按常见配置错误过一遍：凭据 / 权限 / 输入校验 / 日志泄露",
		Params:      []string{"material"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你做加固评审。只报材料里看得出的问题；看不出的说「材料里看不到」。",
				"prompt": "评审下面这份材料，按四类给 `问题 → 影响 → 怎么改`：\n1. 凭据与密钥的管理\n" +
					"2. 权限与最小授权\n3. 外部输入的校验\n4. 日志与错误信息是否泄露敏感内容\n" +
					"某一类没看出问题就明确写「这一类没看出问题」。",
				"input": "{{material}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"安全", "加固", "评审"},
	},
	{
		Name: "csv-profile", Category: "数据分析",
		Description: "给一份 CSV 做字段画像：类型 / 空值 / 异常值 / 可疑列",
		Params:      []string{"path"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.read", Args: map[string]any{"path": "{{path}}"}},
			{ID: "s2", Tool: "prompt.run", Args: map[string]any{
				"system": "你做数据画像。只看给出的内容；被截断或看不出结论时要明说。",
				"prompt": "读这份 CSV 的内容（可能只是样本），逐列给出：字段名 / 看起来的类型 / 空值情况 / " +
					"异常或可疑值 / 能否直接用来做统计。最后列「要拿到完整文件才能回答的问题」。",
				"input": "{{s1.content}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "{{s2.text}}"}, DependsOn: []string{"s2"}},
		},
		Tags: []string{"数据", "CSV", "画像"},
	},
	{
		Name: "deploy-checklist", Category: "运维部署",
		Description: "部署前检查：回滚路径 / 配置差异 / 依赖 / 看什么指标 / 通知谁",
		Params:      []string{"target"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你写部署前检查清单。重点是「出事了怎么退回去」，不是罗列步骤。",
				"prompt": "为下面这个部署目标写可勾选的检查清单：\n1. 回滚路径（怎么退、要多久、谁有权退）\n" +
					"2. 配置与环境差异\n3. 依赖是否就绪\n4. 上线后看什么指标、看多久\n5. 出问题通知谁\n" +
					"做不到的条目写「缺」并说明。",
				"input": "{{target}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"部署", "上线", "检查清单"},
	},
	{
		Name: "topic-brief", Category: "知识研究",
		Description: "抓一个网址，整理成「要点 / 依据 / 它没说的」",
		Params:      []string{"url"},
		Steps: []types.Step{
			{ID: "s1", Tool: "web.fetch", Args: map[string]any{"url": "{{url}}"}},
			{ID: "s2", Tool: "prompt.run", Args: map[string]any{
				"system": "你写材料摘要。只写原文有的；推断要标注为推断。",
				"prompt": "把这份抓取到的网页内容整理成三部分：\n## 要点（不超过 6 条）\n" +
					"## 依据（每条要点对应的原文线索）\n## 它没说的（明显该讲而没讲的问题）\n" +
					"若抓到的其实是导航页或错误页，直接说明「这篇不是正文」。",
				"input": "{{s1.text}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "memory.save", Args: map[string]any{"content": "已整理 {{url}} 的要点"}, DependsOn: []string{"s2"}},
			{ID: "s4", Tool: "reply", Args: map[string]any{"text": "{{s2.text}}"}, DependsOn: []string{"s2"}},
		},
		Tags: []string{"研究", "摘要", "网页"},
	},
	{
		Name: "source-digest", Category: "知识研究",
		Description: "把长文摘成要点，并标出哪些结论缺证据",
		Params:      []string{"text"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你做摘要。不补原文没有的结论；证据不足的要标出来。",
				"prompt": "把下面这篇长文摘成：\n## 核心结论（不超过 5 条）\n## 支撑它们的证据（原文里的）\n" +
					"## 缺证据的判断（原文说了但没有依据的）\n只输出这三部分。",
				"input": "{{text}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"摘要", "长文", "证据"},
	},
	{
		Name: "ui-copy-review", Category: "设计",
		Description: "评审界面文案：歧义 / 术语不一致 / 按钮写成状态",
		Params:      []string{"copy"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你评审界面文案。判据是「用户读第一遍会不会误解」，不是文采。",
				"prompt": "评审下面这些界面文案：逐条给出问题类型（歧义 / 术语不一致 / 按钮写成状态 / 过长 / " +
					"语气不当）+ 改法。最后给一张「术语对照表」：同一个概念在文里出现了几种说法。",
				"input": "{{copy}}"}},
			{ID: "s2", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"设计", "文案", "评审"},
	},
	{
		Name: "context-handoff", Category: "工作流",
		Description: "把当前上下文整理成交接说明（含未决问题与各自的取舍）",
		Params:      []string{"material"},
		Steps: []types.Step{
			{ID: "s1", Tool: "prompt.run", Args: map[string]any{
				"system": "你写交接说明。目标是让对方不用回头翻聊天记录就能接手。",
				"prompt": "依据下面的材料写交接说明：\n## 现在到哪一步了\n## 已经定了的（含为什么）\n" +
					"## 还没定的（含各自的取舍）\n## 下一步建议与它的风险\n不确定的标注「待确认」。",
				"input": "{{material}}"}},
			{ID: "s2", Tool: "memory.save", Args: map[string]any{"content": "交接说明：{{s1.text}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "reply", Args: map[string]any{"text": "{{s1.text}}"}, DependsOn: []string{"s1"}},
		},
		Tags: []string{"交接", "上下文", "协作"},
	},
	{
		Name: "memory-distill", Category: "工作流",
		Description: "把记忆里相关的条目归纳成要点，写回长期记忆",
		Params:      []string{"topic"},
		Steps: []types.Step{
			{ID: "s1", Tool: "memory.search", Args: map[string]any{"query": "{{topic}}"}},
			{ID: "s2", Tool: "prompt.run", Args: map[string]any{
				"system": "你归纳笔记。只依据给出的条目；相互矛盾的地方要指出来，不要抹平。",
				"prompt": "把下面这些记忆条目归纳成要点。相互矛盾的条目单独列一节「说法不一致」，不要替它们下判断。",
				"input":  "{{s1.hits}}"}, DependsOn: []string{"s1"}},
			{ID: "s3", Tool: "memory.save", Args: map[string]any{"content": "关于 {{topic}} 的归纳：{{s2.text}}"}, DependsOn: []string{"s2"}},
			{ID: "s4", Tool: "reply", Args: map[string]any{"text": "{{s2.text}}"}, DependsOn: []string{"s2"}},
		},
		Tags: []string{"记忆", "归纳", "整理"},
	},
}
