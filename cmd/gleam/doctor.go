package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"gleam/internal/agent"
	"gleam/internal/harness/growth"
)

// cmdDoctor 就绪体检：把"自建 Agent Runtime 常见的九个坑"逐条对照当前运行状态。
//
// 只读：不改配置、不发模型请求、不写文件。适合在升级后、上线前、排障时随手跑一次。
// 加 --json 输出结构化报告，便于接入 CI 或别的看板。
//
// 审批通道用控制台实现（与 gleam goal 一致）——体检结论要反映真实运行环境，
// 不能因为"这是个诊断命令"就假装没有审批通道。
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configPath := fs.String("config", "", "配置文件路径")
	workspace := fs.String("workspace", "", "工作区根目录")
	dataDir := fs.String("data-dir", "", "数据目录")
	mockLLM := fs.Bool("mock-llm", false, "使用 Mock 模型")
	mockScript := fs.String("mock-script", "", "Mock 响应脚本 JSON 路径")
	asJSON := fs.Bool("json", false, "输出结构化 JSON 报告")
	strict := fs.Bool("strict", false, "存在不合格项时以非零退出（供 CI/上线前门禁使用）")
	flagArgs, positionals := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("doctor 不接受位置参数 %q", positionals[0])
	}

	rt, err := buildRuntime(*configPath, *workspace, *dataDir, *mockLLM, *mockScript, newConsoleNotifier())
	if err != nil {
		return err
	}
	defer rt.cleanup()

	rep := rt.agent.Readiness()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return err
		}
		return strictErr(rep, *strict)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "就绪体检（九坑自检）  %s\n\n", rep.GeneratedAt.Format("2006-01-02 15:04"))

	for _, it := range rep.Items {
		fmt.Fprintf(&b, "  %s %d. %s\n", readinessMark(it.Status), it.Index, it.Title)
		fmt.Fprintf(&b, "          %s\n", it.Summary)
		for _, ev := range it.Evidence {
			fmt.Fprintf(&b, "          · %s\n", ev)
		}
		if it.Fix != "" {
			fmt.Fprintf(&b, "          → %s\n", it.Fix)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "合计：%d 通过 / %d 待改进 / %d 不合格 —— %s\n",
		rep.Passed, rep.Warned, rep.Failed, rep.Verdict)

	// 四类状态的落盘清单（Q4）。**报告，不是判定**——所以不参与 --strict、也不进 --json，
	// 理由见 state.go 文件头。放在合计之后：它是"盘上现在有什么"，紧跟体检结论。
	fmt.Fprint(&b, stateReport(rt.cfg.DataDir))

	// 用户信号（P3-4）+ 交付侧一次性（P5）：重试率/中断率是最接近真相的质量指标——
	// 产品指标全是代理，"用户觉得不行又发了一遍"和"用户不等了"不是。
	if g := rt.agent.Growth; g != nil {
		fmt.Fprint(&b, userSignalReport(g.Stats()))
	}

	// 把需要动手的项单独收口，避免报告看完就完了。
	var todo []string
	for _, it := range rep.Items {
		if it.Status != agent.ReadyPass && it.Fix != "" {
			todo = append(todo, fmt.Sprintf("    %s %d. %s → %s", readinessMark(it.Status), it.Index, it.Title, it.Fix))
		}
	}
	if len(todo) > 0 {
		fmt.Fprintf(&b, "\n待处理：\n%s\n", strings.Join(todo, "\n"))
	}

	fmt.Print(b.String())
	return strictErr(rep, *strict)
}

// userSignalMinSample 报比率的最小样本量。
// 3 个任务里 1 个重试 = 33%，那是噪音不是信号——报出来只会让人对指标失去信任，
// 然后连真的信号一起不信。
const userSignalMinSample = 5

// userSignalReport 渲染"用户信号 + 交付侧一次性"那一段；样本不足时返回空串。
//
// 抽成独立函数是为了可测：这段里有两个**不同分母**（用户信号按"完成 + 中断"，
// 交付口径只按走过工作循环的完成任务），而分母写错在终端上看起来一模一样——
// 只有断言文本才能发现。行内重复写分母就是在给这个错误留门。
func userSignalReport(st growth.Stats) string {
	if st.TotalTasks+st.AbortedTasks < userSignalMinSample {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n用户信号（口径：分母 = 完成 %d + 中断 %d）\n", st.TotalTasks, st.AbortedTasks)
	fmt.Fprintf(&b, "  用户重试率 %.0f%%（%d 次）——重试的任务质量打折，原始 trace 可用 gleam eval --emit-case 回流\n",
		st.RetryRate*100, st.RetryCount)
	fmt.Fprintf(&b, "  用户中断率 %.0f%%（%d 次）——中断要修的是等待体验或能力缺口，与失败不同因\n",
		st.AbortRate*100, st.AbortedTasks)
	// 交付侧一次性。口径与上面两个**不同**（只算走过工作循环的完成任务），
	// 所以分母写在行内，不去借上面那句的"完成 + 中断"——共用分母就是口径混账的开始。
	// 分母为零时整段不报：0/0 不是 0%，"没有样本"和"通过率是零"是两件事。
	if st.FirstPassBase > 0 {
		fmt.Fprintf(&b, "  首次验收通过率 %.0f%%（%d/%d，仅工作模式）——返工一次才成也是成功，但不是一次就合格\n",
			st.FirstPassRate*100, st.FirstPassCount, st.FirstPassBase)
		fmt.Fprintf(&b, "  累计返工 %d 轮（含中断前已发生的）——重试三次才凑出来与一次做对，成功率上一样、成本上两回事\n",
			st.ReworksTotal)
	}
	return b.String()
}

// strictErr --strict 时的门禁判定：只卡「不合格」。
//
// 「待改进」不卡——那类项是"机制在但没用起来"（没配档位、还没沉淀技能），
// 属于使用进度而不是缺陷，拿它拦发布只会逼人加 --no-strict 把整个检查废掉。
// 「不合格」是结构性缺口（内核不齐、裸工具、布局退化），那才是真该拦住上线的。
func strictErr(rep agent.ReadinessReport, strict bool) error {
	if !strict || rep.Failed == 0 {
		return nil
	}
	var bad []string
	for _, it := range rep.Items {
		if it.Status == agent.ReadyFail {
			bad = append(bad, fmt.Sprintf("%d. %s", it.Index, it.Title))
		}
	}
	return fmt.Errorf("体检不合格 %d 项：%s（去掉 --strict 可只看报告）", rep.Failed, strings.Join(bad, "；"))
}

// readinessMark 三档结论的终端标记（纯文本，避免控制台字体缺字形）。
func readinessMark(s agent.ReadinessStatus) string {
	switch s {
	case agent.ReadyPass:
		return "[通过]  "
	case agent.ReadyWarn:
		return "[待改进]"
	default:
		return "[不合格]"
	}
}
