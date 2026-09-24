package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gleam/internal/agent"
	"gleam/internal/eval"
	"gleam/pkg/types"
)

// cmdEval 提示词与行为回归评测。
//
// 存在的理由：提示词的稳定段被服务端前缀缓存折扣过，"给提示词做减法"的收益不在省钱，
// 而在减少注意力稀释——**这件事没法用"省了多少字节"论证，只能靠评测回答"删了会不会变差"**。
// 没有评测就删规则，是赌博不是工程。
//
// 三个深度由浅入深：
//
//	select  只跑工具筛选（本地关键词打分），零模型调用、确定性、可离线进 CI
//	plan    跑到计划，断言计划里的工具与结构（需要模型）
//	full    跑完整目标（规划+执行+反思），会真的动工具（需要模型）
//
// 默认 select：它是唯一不花钱、不联网、结果可复现的深度，也是提示词与工具描述改动
// 最容易悄悄弄坏的那一层——把某个工具的说明改短到丢了关键词，相关目标就再也选不到它，
// 而计划看起来仍然"正常"。plan / full 留给人手动跑，或在配了真实模型的环境里跑。
func cmdEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	depth := fs.String("depth", string(eval.DepthSelect), "评测深度 select|plan|full")
	tier := fs.String("tier", "", "模拟生效模型档位（只改提示词构建，不切换模型）")
	casesPath := fs.String("cases", "", "自定义用例集 JSON（默认用内置用例）")
	layer := fs.String("layer", "", "只跑指定分层的用例 smoke|regression|edge|adversarial|holdout（默认全部）")
	emitCase := fs.String("emit-case", "", "把指定 taskID 的失败任务转成用例草稿（badcase 回流；读取 <data-dir>/tasks/<taskID>.json）")
	asJSON := fs.Bool("json", false, "输出结构化 JSON 报告")
	repeat := fs.Int("repeat", 1, "每条用例重跑遍数（>1 时额外报出可重复性：通过率回答「对不对」，可重复性回答「稳不稳」）")
	save := fs.String("save", "", "把本次报告写成基线文件")
	baseline := fs.String("baseline", "", "与基线文件对比并报出回归")
	strict := fs.Bool("strict", false, "有回归则以非零退出（未给基线时：有用例未过即非零）")
	flagArgs, positionals := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("eval 不接受位置参数 %q", positionals[0])
	}
	if *repeat < 1 {
		return fmt.Errorf("--repeat 至少为 1（当前 %d）", *repeat)
	}

	d := eval.Depth(*depth)
	if !eval.ValidDepth(d) {
		return fmt.Errorf("未知深度 %q（可选 select|plan|full）", *depth)
	}

	// full 深度会真的执行工具（写文件、写记忆）。默认把它关进临时目录，
	// 免得一次评测把用户的工作区和长期记忆搅了——那个代价比评测本身大得多。
	// 用户显式传了 --workspace / --data-dir 就尊重用户的选择。
	var tempDirs []string
	if d == eval.DepthFull {
		if *workspace == "" {
			dir, err := os.MkdirTemp("", "gleam-eval-ws-")
			if err != nil {
				return fmt.Errorf("创建临时工作区失败: %w", err)
			}
			*workspace, tempDirs = dir, append(tempDirs, dir)
		}
		if *dataDir == "" {
			dir, err := os.MkdirTemp("", "gleam-eval-data-")
			if err != nil {
				return fmt.Errorf("创建临时数据目录失败: %w", err)
			}
			*dataDir, tempDirs = dir, append(tempDirs, dir)
		}
	}
	defer func() {
		for _, dir := range tempDirs {
			_ = os.RemoveAll(dir)
		}
	}()

	cases, err := loadEvalCases(*casesPath)
	if err != nil {
		return err
	}

	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, agent.NopNotifier{})
	if err != nil {
		return err
	}
	defer rt.cleanup()

	// badcase 回流（P3-3）：失败任务 → 用例草稿。期望留占位由人工补——
	// 机器不知道"应该是什么样"，只有知道这条任务为什么失败的人知道。
	if *emitCase != "" {
		return emitCaseDraft(rt.cfg.DataDir, *emitCase)
	}

	cases = eval.FilterLayer(cases, eval.Layer(*layer))
	if len(cases) == 0 {
		return fmt.Errorf("分层 %q 下没有用例（内置用例分层：smoke / regression / holdout）", *layer)
	}

	if d != eval.DepthSelect && !*asJSON {
		if rt.cfg.LLM.Provider == "mock" {
			fmt.Fprintln(os.Stderr, "[gleam] 注意：当前是 Mock 模型，plan/full 深度只能验证管道通畅，验证不了质量。")
		} else if rt.cfg.LLM.APIKey == "" {
			fmt.Fprintln(os.Stderr, "[gleam] 注意：没有可用的模型 API Key，plan/full 深度的用例会因调用失败而未过。")
		}
	}

	runner := &eval.Runner{
		Agent:  rt.agent,
		Depth:  d,
		CWD:    *workspace,
		Tier:   *tier,
		Repeat: *repeat,
		OnCase: func(res eval.CaseResult) {
			if *asJSON {
				return // JSON 模式下别把过程混进输出
			}
			// 保留池不逐条播报——它是"没被盯着调的样本"，看了单条就会忍不住调它
			if res.Holdout {
				return
			}
			mark := evalMark(res)
			// 不稳的用例单独标出来：它的绿可能只是掷硬币掷出来的，
			// 与"稳定通过"不该长得一样。
			if !res.Stable {
				mark += " [不稳]"
			}
			fmt.Fprintf(os.Stderr, "  %s %s\n", mark, res.ID)
		},
	}
	rep := runner.Run(context.Background(), cases)

	// 先存基线（不带 diff，基线文件里不该留"与上一次对比"的结果），再对比。
	if *save != "" {
		if err := eval.SaveBaseline(*save, rep); err != nil {
			return err
		}
		if !*asJSON {
			fmt.Fprintf(os.Stderr, "[gleam] 已写入基线 %s\n", *save)
		}
	}

	hasBaseline := *baseline != ""
	if hasBaseline {
		base, err := eval.LoadBaseline(*baseline)
		if err != nil {
			return fmt.Errorf("读取基线失败: %w", err)
		}
		diff, err := eval.Compare(rep, base)
		if err != nil {
			return err
		}
		rep.Diff = diff
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
	} else {
		fmt.Print(renderEvalReport(rep, len(cases)))
	}

	return evalGateErr(rep, *strict, hasBaseline)
}

// loadEvalCases 取用例集：给了文件就用文件，否则用内置。
func loadEvalCases(path string) ([]eval.Case, error) {
	if path != "" {
		return eval.LoadCases(path)
	}
	return eval.BuiltinCases()
}

// emitCaseDraft 把一条失败任务转成用例草稿（badcase 回流，P3-3 / 站点 H4、H5）。
//
// 只填机器知道的（goal、实际用到的工具），期望行为留占位由人工补——
// 站点 H4 的警告必须落在草稿上：只有工程问题进回归集；"换更强模型会不会消失"
// 是区分研究问题与工程问题的第一问，写进 note 让回流的人先回答它再入库。
func emitCaseDraft(dataDir, taskID string) error {
	if strings.TrimSpace(taskID) == "" || strings.ContainsAny(taskID, `/\`) {
		return fmt.Errorf("非法任务 ID: %q", taskID)
	}
	path := filepath.Join(dataDir, "tasks", taskID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取任务记录失败: %w（任务归档由 gleam goal / serve 自动写入）", err)
	}
	var g types.GoalResult
	if err := json.Unmarshal(b, &g); err != nil {
		return fmt.Errorf("解析任务记录失败: %w", err)
	}

	draft := eval.Case{
		ID:       "bc-" + taskID,
		Goal:     g.Goal,
		Role:     "", // 占位：这条任务当时用的专家角色，人工确认后填
		TaskMode: "",
		Note: fmt.Sprintf(
			"badcase 回流自任务 %s（状态 %s，trace %s）。【先回答】：换更强模型这个问题会不会消失？会→研究问题，不进回归集；不会→工程问题，补齐期望后入库。期望行为写成可判定的结构（要什么/不要什么），不要写期望文本。",
			taskID, g.Status, g.TraceID),
	}
	// 实际用到的工具：失败的证据先留下来，期望从"曾选到什么"开始反推
	seen := map[string]bool{}
	for _, st := range g.Steps {
		if st.Tool == "" || seen[st.Tool] {
			continue
		}
		seen[st.Tool] = true
		draft.WantTools = append(draft.WantTools, st.Tool)
	}

	out, err := json.MarshalIndent(map[string]any{
		"note":       "用例草稿——期望字段补齐、删掉本说明后并入用例集",
		"case":       draft,
		"task_id":    taskID,
		"status":     string(g.Status),
		"error":      g.Error,
		"trace_id":   g.TraceID,
		"ruleset":    g.RuleSet,
		"steps_hint": "原始步骤与失败归因见 " + path + "（gleam replay " + taskID + " 可回放）",
	}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	fmt.Fprintf(os.Stderr, "[gleam] 已生成用例草稿 bc-%s：补齐期望后并入用例集；只有工程问题进回归集。\n", taskID)
	return nil
}

// evalGateErr --strict 时的门禁判定。
//
// 给了基线就只卡「回归」（基线绿、现在红）——那是改动弄坏了东西的硬证据；
// 没给基线才退而卡「有用例未过」。之所以不一律卡未过：用例集里允许存在
// 已知未过的用例（记录待修问题），一律卡会逼人把 --strict 整个去掉，门禁就废了。
func evalGateErr(rep eval.Report, strict, hasBaseline bool) error {
	if !strict {
		return nil
	}
	if hasBaseline {
		if rep.Diff.Regressed() {
			return fmt.Errorf("评测出现回归 %d 项：%s（详见上方对比；确认是有意为之可去掉 --strict）",
				len(rep.Diff.Broke), strings.Join(rep.Diff.Broke, "、"))
		}
		return nil
	}
	if rep.Failed > 0 {
		return fmt.Errorf("评测 %d 项未过（%d/%d 通过）；想只卡回归请加 --baseline 指定基线文件",
			rep.Failed, rep.Passed, rep.Total)
	}
	return nil
}

// renderEvalReport 把报告排成人能读的文本。
//
// 未过项连同逐条断言的实测值一起打出来：评测的价值在于"差在哪"，
// 只报通过率会让人回头再手工复现一遍，那就白跑了。
func renderEvalReport(rep eval.Report, total int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "提示词与行为回归评测  %s 深度  %s\n",
		rep.Depth, rep.GeneratedAt.Format("2006-01-02 15:04"))

	model := rep.Model
	if rep.Depth == eval.DepthSelect && rep.LocalSelect {
		model += "（本地关键词筛选，未调用模型）"
	}
	fmt.Fprintf(&b, "用例 %d 条  模型 %s\n", total, model)
	if rep.Tier != "" {
		fmt.Fprintf(&b, "模拟生效档位：%s（只改提示词构建，不切换模型）\n", rep.Tier)
	}
	fmt.Fprintln(&b)

	for _, c := range rep.Cases {
		// 保留池只进聚合、不展示单条结果：单条被反复看，就会被反复调（H13）
		if c.Holdout {
			continue
		}
		mark := evalMark(c)
		if !c.Stable {
			mark += " [不稳]"
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, c.ID)
		// 不稳的用例必须自己解释自己：抖的是哪一部分（通过与否 / 步数 / 用了哪些工具）。
		// 不写这一行，读报告的人只能自己再手工重跑几遍，那就白跑了。
		if !c.Stable {
			fmt.Fprintf(&b, "          重跑 %d 遍得到 %d 种结果：%s\n",
				c.Runs, len(c.Variants), strings.Join(c.Variants, "；"))
		}
		if c.Passed {
			continue
		}
		fmt.Fprintf(&b, "          目标：%s\n", c.Goal)
		for _, ck := range c.Checks {
			if ck.Passed {
				continue
			}
			line := "          · " + ck.Name
			if ck.Detail != "" {
				line += " —— " + ck.Detail
			}
			fmt.Fprintln(&b, line)
		}
		if c.Error != "" {
			fmt.Fprintf(&b, "          · 执行错误：%s\n", c.Error)
		}
		if c.Note != "" {
			fmt.Fprintf(&b, "          （这条在守什么：%s）\n", c.Note)
		}
	}

	fmt.Fprintf(&b, "\n合计：%d 通过 / %d 未过", rep.Passed, rep.Failed)
	if rep.Known > 0 {
		fmt.Fprintf(&b, " / %d 已知问题", rep.Known)
	}
	fmt.Fprintf(&b, "（提示词合计 %d 字符）\n", rep.PromptChars)
	// 用量紧跟在提示词字符数后面，**同一段呈现**：一个是成本代理、一个是成本本体。
	// 分开写会让人只看到其中一个，然后拿它当全部。
	switch {
	case rep.Usage != nil:
		fmt.Fprintf(&b, "用量：%d 次模型调用，%d token（输入 %d / 输出 %d",
			rep.Usage.LLMCalls, rep.Usage.TotalTokens(), rep.Usage.PromptTokens, rep.Usage.CompletionTokens)
		if rep.Usage.CachedTokens > 0 {
			fmt.Fprintf(&b, "，缓存命中 %d", rep.Usage.CachedTokens)
		}
		fmt.Fprintf(&b, "），%d 次工具调用", rep.Usage.ToolCalls)
		if rep.Usage.EstimatedCalls > 0 {
			// 估出来的值必须自己说自己是估的——不说，这行 token 看起来像实测值。
			fmt.Fprintf(&b, "；其中 %d 次调用的 token 为估算值", rep.Usage.EstimatedCalls)
		}
		fmt.Fprintln(&b)
		if rep.RepeatN > 1 {
			fmt.Fprintf(&b, "  含 --repeat %d 的每一遍：重跑也真的调了模型，所以一并计入\n", rep.RepeatN)
		}
	case rep.Depth == eval.DepthPlan:
		// plan 深度**会**调模型（规划那一次），但它的调用不经过 RunGoal、没有 taskID 登记，
		// 因此归集不到用例上。这是缺口，不是 0——不写这一句，读的人会把
		// "没有用量这一行"当成"这批评测没花钱"。
		fmt.Fprintln(&b, "用量：未计量（plan 深度的规划调用不经过 RunGoal，没有归集到用例上）")
	}
	if rep.HoldoutTotal > 0 {
		fmt.Fprintf(&b, "保留池：%d/%d 通过（%d 条，单条结果不展示——防止调参过拟合到看得见的用例）\n",
			rep.HoldoutPassed, rep.HoldoutTotal, rep.HoldoutTotal)
	}
	// 可重复性单独一行，且明确写出它与通过率的关系——两个数不能互相替代。
	if rep.Repeatability != nil {
		fmt.Fprintf(&b, "可重复性：%.0f%%（%d/%d 条用例重跑 %d 遍结果完全一致）\n",
			*rep.Repeatability*100, rep.StableCases, rep.Total, rep.RepeatN)
		if len(rep.UnstableCases) > 0 {
			fmt.Fprintf(&b, "  ⚠ 不稳的用例：%s\n", strings.Join(rep.UnstableCases, "、"))
			fmt.Fprintln(&b, "  不稳要么是断言太细（该放宽），要么是提示词有歧义（该修）；"+
				"在它变稳之前，这条用例的绿不能当作改动有效的证据。")
		} else {
			fmt.Fprintln(&b, "  通过率回答「对不对」，可重复性回答「稳不稳」——两者都达标，结论才可归因。")
		}
	}
	// 指标口径三要素：分子（Passed）、分母（Attempted，不含不可解）、边界（深度）。
	// 分母不说清楚，"通过率 80%"与"可解样本通过率 60%"能差出二十个百分点。
	if rep.Unsolvable > 0 {
		rate := 0.0
		if rep.Attempted > 0 {
			rate = float64(rep.Passed) / float64(rep.Attempted) * 100
		}
		urate := 0.0
		if rep.Unsolvable > 0 {
			urate = float64(rep.UnsolvableHandled) / float64(rep.Unsolvable) * 100
		}
		fmt.Fprintf(&b, "口径：可解样本通过率 %.0f%%（%d/%d，分母不含 %d 条不可解用例）；不可解处理率 %.0f%%（%d/%d，识别为不可解、不硬猜）\n",
			rate, rep.Passed, rep.Attempted, rep.Unsolvable, urate, rep.UnsolvableHandled, rep.Unsolvable)
	}
	if len(rep.FailureBreakdown) > 0 {
		fmt.Fprintf(&b, "失败归因分布（过程失败，含被重规划救回的）：")
		kinds := make([]string, 0, len(rep.FailureBreakdown))
		for k := range rep.FailureBreakdown {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			fmt.Fprintf(&b, " %s=%d", k, rep.FailureBreakdown[k])
		}
		fmt.Fprintln(&b)
	}
	if rep.RetryOK > 0 {
		fmt.Fprintf(&b, "重试后才成功的步骤：%d 个（这些不是\"一次就对\"，是隐患位置）\n", rep.RetryOK)
	}

	if len(rep.Resolved) > 0 {
		fmt.Fprintf(&b, "\n✓ 下列用例标着「已知问题」但本次通过了，根因多半已修，可以摘掉标记：%s\n",
			strings.Join(rep.Resolved, "、"))
	}

	if rep.Diff != nil {
		fmt.Fprintf(&b, "\n与基线（%s）对比：\n", rep.Diff.BaselineAt.Format("2006-01-02 15:04"))
		for _, line := range rep.Diff.Describe() {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return b.String()
}

// evalMark 终端标记（纯文本，避免控制台字体缺字形）。
// 已知问题单独一档：它也是红的，但红的原因已经查清并记在用例里，不该与回归混为一谈。
func evalMark(c eval.CaseResult) string {
	switch {
	case c.Passed:
		return "[通过]"
	case c.Known:
		return "[已知]"
	default:
		return "[未过]"
	}
}
