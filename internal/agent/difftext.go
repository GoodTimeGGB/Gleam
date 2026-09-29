package agent

import (
	"strconv"
	"strings"
)

// 行级文本对比（改动清单的"对比"视图用）。
//
// **为什么自己写而不引库、也不调 git**：零第三方依赖是产品前提，而工作区未必是
// git 仓库；一条"看 diff"的功能如果建立在"恰好装了 git 且恰好是仓库"上，
// 它在非仓库工作区里的表现是**按钮按了没反应**——本仓库最忌讳的那类静默失效。
//
// **为什么放在 agent 包**：它的唯一读者就是这里的 diff/还原门面。单独开一个
// `internal/difftext` 顶层包，得先回答"还有谁会用"——目前没有，
// 而 AGENTS.md 的布局表也不该为一段 60 行的纯函数多挂一行。
//
// 只处理**行**：字符级对比要在超长行上算等价类，代价与收益都不划算，
// 而"哪几行加了、哪几行删了"已经是用户看 diff 时问的第一个问题。
const (
	// diffMaxSideLines 单侧参与对比的行数上限。超了不硬算，老实报"太大只给摘要"。
	diffMaxSideLines = 2000
	// diffCellBudget 动态规划表的格子预算。
	//
	// LCS 是 O(n×m) 的乘积复杂度，光限行数挡不住"两边各 2000 行但完全不同"这种
	// 最坏形状（400 万格 ≈ 十几 MB）。所以再设一道乘积闸：到顶就降级。
	diffCellBudget = 400_000
	// diffMaxLines 返回给界面的行数上限（两端各留一半，中间给一句省略说明）。
	diffMaxLines = 400
)

// 行类别。界面按它上色，所以取值是契约的一部分。
const (
	DiffCtx = "ctx" // 上下文行（两边都有）
	DiffAdd = "add" // 只有后侧有
	DiffDel = "del" // 只有前侧有
)

// DiffLine 一行对比结果。
type DiffLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	No   int    `json:"no"` // 该行的编号：前侧行用 before_no，后侧行用 after_no
}

// DiffResult 一次前后对比。
type DiffResult struct {
	Lines     []DiffLine `json:"lines"`
	Added     int        `json:"added"`
	Deleted   int        `json:"deleted"`
	Truncated bool       `json:"truncated"` // 规模超限，只给了部分（或只给了计数）
	Note      string     `json:"note,omitempty"`
}

// splitLines 按行切开，并统一去掉行尾的 \r。
//
// 为什么去 \r：Windows 文件常是 CRLF，同一份内容换过一次行尾（编辑器设置、
// git autocrlf）会让**每一行**都变成"删了又加"。那种 diff 是噪音，比不显示更坏。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1] // 结尾换行不是"多了一个空行"
	}
	return parts
}

// DiffText 算 before -> after 的行级差异。
//
// 先掐头去尾（公共前后缀），中间再做 LCS——真实改动几乎都是"改了几行"，
// 掐完剩不下多少，于是常见的几百行文件只需要几百格。
func DiffText(before, after string) DiffResult {
	a, b := splitLines(before), splitLines(after)
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	midA, midB := a[head:len(a)-tail], b[head:len(b)-tail]

	// 两侧逐行完全一致（只差行尾风格也算）：给一份**空结果**，不给三行上下文。
	// 否则界面上会出现"有内容、零增删"的对比——用户会读成"这里改了但没列出来"，
	// 而正确的答复是"这一份没有变化"。
	if len(midA) == 0 && len(midB) == 0 {
		return DiffResult{Note: "前后内容逐行一致"}
	}
	res := DiffResult{}
	if len(midA) > diffMaxSideLines || len(midB) > diffMaxSideLines ||
		len(midA)*max(len(midB), 1) > diffCellBudget {
		// 算不动：老老实实给计数，别给一份看着完整其实缺了半截的 diff。
		res.Truncated = true
		res.Added, res.Deleted = len(midB), len(midA)
		res.Note = "改动规模超过行级对比上限，只给出增删行数"
		return res
	}
	// 公共前后缀各留几行当上下文——一点上下文都不给的 diff 很难读。
	for i := max(head-3, 0); i < head; i++ {
		res.Lines = append(res.Lines, DiffLine{Kind: DiffCtx, Text: a[i], No: i + 1})
	}
	res.Lines = append(res.Lines, lcsLines(midA, midB, head)...)
	for k := 0; k < min(tail, 3); k++ {
		j := len(b) - tail + k
		res.Lines = append(res.Lines, DiffLine{Kind: DiffCtx, Text: b[j], No: j + 1})
	}
	for _, l := range res.Lines {
		switch l.Kind {
		case DiffAdd:
			res.Added++
		case DiffDel:
			res.Deleted++
		}
	}
	if len(res.Lines) > diffMaxLines {
		res.Lines = clampLines(res.Lines, diffMaxLines)
		res.Truncated = true
		res.Note = "改动较多，只列出前 " + strconv.Itoa(diffMaxLines) + " 行"
	}
	return res
}

// lcsLines 对掐掉公共前后缀后的中段做 LCS，产出 add/del 行。
//
// `offset` 是中段在原文件里的起始下标，行号要按原文件算——用户拿行号去编辑器里找东西。
func lcsLines(a, b []string, offset int) []DiffLine {
	m, n := len(a), len(b)
	if m == 0 {
		out := make([]DiffLine, 0, n)
		for j := 0; j < n; j++ {
			out = append(out, DiffLine{Kind: DiffAdd, Text: b[j], No: offset + j + 1})
		}
		return out
	}
	if n == 0 {
		out := make([]DiffLine, 0, m)
		for i := 0; i < m; i++ {
			out = append(out, DiffLine{Kind: DiffDel, Text: a[i], No: offset + i + 1})
		}
		return out
	}
	// dp[i][j] = a[i:] 与 b[j:] 的最长公共子序列长度。一维滚动省内存，
	// 但回溯需要整张表，所以这里按行存 int32（上限已被 diffCellBudget 钳住）。
	table := make([]int32, (m+1)*(n+1))
	at := func(i, j int) *int32 { return &table[i*(n+1)+j] }
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				*at(i, j) = *at(i+1, j+1) + 1
			} else if x, y := *at(i+1, j), *at(i, j+1); x >= y {
				*at(i, j) = x
			} else {
				*at(i, j) = y
			}
		}
	}
	var out []DiffLine
	i, j := 0, 0
	for i < m && j < n {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Kind: DiffCtx, Text: a[i], No: offset + i + 1})
			i, j = i+1, j+1
		case *at(i+1, j) >= *at(i, j+1):
			out = append(out, DiffLine{Kind: DiffDel, Text: a[i], No: offset + i + 1})
			i++
		default:
			out = append(out, DiffLine{Kind: DiffAdd, Text: b[j], No: offset + j + 1})
			j++
		}
	}
	for ; i < m; i++ {
		out = append(out, DiffLine{Kind: DiffDel, Text: a[i], No: offset + i + 1})
	}
	for ; j < n; j++ {
		out = append(out, DiffLine{Kind: DiffAdd, Text: b[j], No: offset + j + 1})
	}
	return out
}

// clampLines 保留前 keep 行，并在末尾补一句省略说明。
//
// 刻意只截尾不掐中间：中间省略会让行号看起来不连续，而"看起来不连续"会被读成
// 对比算错了。要截就截得明明白白。
func clampLines(lines []DiffLine, keep int) []DiffLine {
	out := append([]DiffLine(nil), lines[:keep]...)
	out = append(out, DiffLine{Kind: DiffCtx, Text: "…… 其余行省略", No: 0})
	return out
}
