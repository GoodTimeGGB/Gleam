package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"gleam/internal/agent"
)

// cmdRules 规则集回查。
//
// 为什么要有这个命令：**"这次产出用的是哪版规则"必须能离线回查。**
// 规则资产化之后，一次产出的行为由三件事共同决定——代码、模型、规则表。
// 前两个都有现成的查看方式（版本、--mock-llm），规则表此前没有：
// 想知道"这个结果是被哪几条规则约束出来的"，只能去读 rules.go 自己在脑子里跑一遍
// 范围匹配。规则一多、变体一多，人脑跑不动。
//
// 两种视图：
//
//	gleam rules                                    列全表：每条规则的落点、条目数、适用范围，以及版本指纹
//	gleam rules --task-mode code --tier coding     列场景：这个组合下**实际生效**与**被挡掉**的规则 ID
//
// 场景视图是日常用得多的那个：它回答"为什么这条没进提示词"——
// 答案通常是被 Scope 挡了，而看代码不容易一眼看出。
//
// 用 --task-mode 而不是 --mode：后者在 gleam goal 里是执行模式（auto|plan_first|
// interactive），同名不同义最容易在复制命令行时踩雷。
//
// 命令**不装配运行时**：规则表是编译期常量，读它不该要求配置、模型、数据目录都就位——
// 排障时往往正是这些坏了才要回头查规则。
func cmdRules(args []string) error {
	fs := flag.NewFlagSet("rules", flag.ContinueOnError)
	mode := fs.String("task-mode", "", "任务模式 work|code|chat（默认 work）")
	tier := fs.String("tier", "", "生效模型档位 economy|coding|office|reasoning")
	role := fs.String("role", "", "角色 ID，如 coder/writer/analyst")
	asJSON := fs.Bool("json", false, "输出结构化 JSON")
	flagArgs, positionals := splitFlagArgs(args, fs)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positionals) > 0 {
		return fmt.Errorf("rules 不接受位置参数 %q", positionals[0])
	}

	m := strings.TrimSpace(*mode)
	if m == "" {
		m = "work" // 与 normalizeTaskMode 的默认一致
	}
	switch m {
	case "work", "code", "chat":
	default:
		return fmt.Errorf("未知任务模式 %q（可选 work|code|chat）", *mode)
	}
	t := strings.TrimSpace(*tier)
	r := strings.TrimSpace(*role)

	// 给了任一场景维度就进场景视图——"全表"是没给任何维度时的默认，
	// 因为多数时候人是带着一个具体场景来问"这次用了哪几条"的。
	scenario := *mode != "" || *tier != "" || *role != ""

	// 档位名写错不会报错，只会静默落到兜底版规则上——那正是最难查的一类问题
	// （现象是"规则没生效"，实际是拼错了档位名），所以这里显式提醒。
	if t != "" && !knownTier(t) {
		fmt.Fprintf(os.Stderr, "[gleam] 注意：档位 %q 不是已知档位名（economy|coding|office|reasoning），它会匹配不到任何按档位条件化的规则\n", t)
	}

	out := rulesOutput{
		Version: agent.RuleSetVersion(),
		Mode:    m,
		Tier:    t,
		Role:    r,
		Rules:   agent.RuleTable(),
	}
	if scenario {
		rs := agent.DescribeRuleSet(m, t, r)
		out.Active, out.Inactive = rs.Active, rs.Inactive
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Print(renderRulesReport(out, scenario))
	return nil
}

// knownTier 已知档位名（与 roles.go 的常量同源，这里只用来提示拼写）。
func knownTier(t string) bool {
	switch t {
	case agent.TierEconomy, agent.TierCoding, agent.TierOffice, agent.TierReasoning:
		return true
	}
	return false
}

// rulesOutput 两种视图共用的输出结构：全表始终给，场景维度按需填。
//
// 不按视图分两种结构，是想让"看全表"和"看场景"拿到的是同一份东西的两半——
// 否则同一个命令的 --json 会随参数改变形状，脚本就得分支处理。
type rulesOutput struct {
	Version  string           `json:"version"`
	Mode     string           `json:"mode"`
	Tier     string           `json:"tier,omitempty"`
	Role     string           `json:"role,omitempty"`
	Active   []string         `json:"active,omitempty"`
	Inactive []string         `json:"inactive,omitempty"`
	Rules    []agent.RuleInfo `json:"rules"`
}

// renderRulesReport 排成人能读的文本。
//
// 对齐列只用 ASCII（槽位、ID、数字）：中文字符在终端里占两列，用 %-Ns 排会歪，
// 所以适用范围这种含中文的字段一律放行尾、不对齐。
func renderRulesReport(o rulesOutput, scenario bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "规则集指纹 %s\n", o.Version)

	if scenario {
		fmt.Fprintf(&b, "场景  模式=%s  档位=%s  角色=%s\n\n", orDash(o.Mode), orDash(o.Tier), orDash(o.Role))
		if o.Mode == "chat" {
			fmt.Fprintf(&b, "生效 %d 条\n", len(o.Active))
			fmt.Fprint(&b, "对话模式直连模型、不经规划，规则表一条都没用上——产出里 rules 为空是正常现象，不是漏记。\n")
			return b.String()
		}
		fmt.Fprintf(&b, "生效 %d 条（按提示词中的出现顺序）：\n", len(o.Active))
		for _, id := range o.Active {
			fmt.Fprintf(&b, "  %-10s %s\n", slotOf(o.Rules, id), id)
		}
		if len(o.Inactive) > 0 {
			fmt.Fprintf(&b, "\n未生效 %d 条（被适用范围挡在门外）：\n", len(o.Inactive))
			for _, id := range o.Inactive {
				fmt.Fprintln(&b, "  "+id)
			}
		}
		fmt.Fprint(&b, "\n这段 ID 列表会写进每次产出的 result.rules 与评测报告；\n回归时先比它——规则改了和代码改坏了，现象是一样的。\n")
		return b.String()
	}

	items, chars := 0, 0
	fmt.Fprintf(&b, "\n%-10s %-18s %4s %6s  %s\n", "槽位", "规则 ID", "条目", "字符", "适用范围（空=不限）")
	for _, r := range o.Rules {
		items += r.Items
		chars += r.Chars
		fmt.Fprintf(&b, "%-10s %-18s %4d %6d  %s\n", r.Slot, r.ID, r.Items, r.Chars, scopeText(r))
	}
	fmt.Fprintf(&b, "\n合计 %d 条规则 / %d 个条目 / %d 字符", len(o.Rules), items, chars)
	fmt.Fprint(&b, "（含各档位变体，单次只会取其中一份）\n")
	fmt.Fprint(&b, "\n加 --task-mode/--tier/--role 看某个场景实际生效的几条。\n")
	return b.String()
}

// slotOf 查某条规则 ID 的落点；同一 ID 的多个变体落点相同，取第一个即可。
func slotOf(rules []agent.RuleInfo, id string) string {
	for _, r := range rules {
		if r.ID == id {
			return r.Slot
		}
	}
	return "-"
}

// scopeText 把适用范围排成一行；空维度省略，全空表示无条件生效。
func scopeText(r agent.RuleInfo) string {
	var parts []string
	if len(r.Modes) > 0 {
		parts = append(parts, "模式 "+strings.Join(r.Modes, "/"))
	}
	if len(r.Tiers) > 0 {
		parts = append(parts, "档位 "+strings.Join(r.Tiers, "/"))
	}
	if len(r.Roles) > 0 {
		parts = append(parts, "角色 "+strings.Join(r.Roles, "/"))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "  ")
}

// orDash 空值占位，避免"档位="让人以为档位是空字符串这一档。
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
