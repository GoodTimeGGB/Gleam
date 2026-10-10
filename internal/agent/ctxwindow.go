// ctxwindow.go 上下文窗口的占用读数与自动压缩阈值。
//
// 与 memory 那套"短期窗口轮数"是两件事，刻意分开：
//   - memory.fill_pct 回答"对话攒了多少轮"，上限是配置的轮数容量，跟模型无关；
//   - 这里的 window_pct 回答"**下一次请求大概要送多少 token**，占模型窗口的几成"。
//
// 两套口径各有各的 owner，界面上也各自标注——把两个百分比塞进一个数字里，
// 下一个人就没法回答"这个 36% 到底是按什么算的"。
//
// 占用取的是**实测值**：最近一次模型调用实际报回来的 prompt tokens（厂商没报时
// 由 llm.ReportUsage 按文本估算，并标 Estimated）。这是这台机器上唯一一个
// "上下文到底有多大"的真实读数——猜出来的水位只会让人做错决定。
package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gleam/internal/harness/memory"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// 自动压缩的三个阈值（百分比，写死）。
//
// 为什么不做成可调：这两个数回答的是"什么时候该动用户的上下文"，而它一旦可调，
// 用户就要先理解"任务中"和"任务结束"是两套阈值——调出一个永远不会触发的组合太容易了。
// 写死 + 界面写清，比多一个旋钮更有用。
const (
	// compressAtEndPct 任务**已经结束**时的压缩线：到这一步才有必要为下一次开场做准备
	// （汇总 + 收紧）。低于它就不压——为了省一点而下一次开场就多花一次摘要调用，不值。
	compressAtEndPct = 90
	// compressMidPct 任务**还没结束**时的压缩线：这里更急，压完还要接着把任务做完。
	compressMidPct = 96
	// relaxPct 松开的线：压缩过之后水位掉回这里以下，就恢复正常的携带轮数。
	// 有回差（96 → 70）是刻意的：没有回差就会在阈值附近来回抖，每一轮都压一次。
	relaxPct = 70
)

// notePromptSize 记下最近一次请求的输入规模。**不看 taskID**：上下文窗口是这台机器上
// 所有任务共用的那一份（memory 的短窗口就是共享的），所以读数也不该按任务分家。
func (a *Agent) notePromptSize(u llm.Usage) {
	if u.PromptTokens <= 0 {
		return
	}
	a.ctxMu.Lock()
	a.lastPromptTokens = u.PromptTokens
	a.lastPromptEstimated = u.Estimated
	a.ctxMu.Unlock()
}

// lastPrompt 取最近一次请求的输入规模；还没有过调用时返回 0。
func (a *Agent) lastPrompt() (int, bool) {
	a.ctxMu.Lock()
	defer a.ctxMu.Unlock()
	return a.lastPromptTokens, a.lastPromptEstimated
}

// contextWindow 这次要拿多大的分母：手填 > 内置表 > 兜底默认。
// 三个来源都会带出去给界面标注——同一个百分比，"按你填的数算的"和"按兜底值算的"不是一个可信度。
func (a *Agent) contextWindow() (tokens int, source, note string) {
	if a.Cfg != nil && a.Cfg.LLM.ContextWindow > 0 {
		return a.Cfg.LLM.ContextWindow, llm.SourceManual, "按设置里选的窗口大小算"
	}
	model := ""
	if a.Cfg != nil {
		model = a.Cfg.LLM.Model
	}
	return llm.ContextWindowFor(model)
}

// windowPct 占用水位：最近一次输入规模 / 模型窗口，0~100（向上取整到整数）。
//
// 向上取整而不是四舍五入：96% 这条线是"再不做就来不及了"，把 95.6% 说成 96% 只是早了
// 0.4% 触发一次压缩；反过来说成 95% 则可能整整一轮都不动——两个方向的代价不对称。
func windowPct(lastTokens, windowTokens int) int {
	if windowTokens <= 0 || lastTokens <= 0 {
		return 0
	}
	p := (100*lastTokens + windowTokens - 1) / windowTokens
	if p > 100 {
		return 100
	}
	return p
}

// windowReading 一次取齐"占用水位"要用到的全部事实（供 ContextView 与阈值判定共用）。
type windowReading struct {
	LastTokens int    `json:"prompt_tokens"`    // 最近一次请求的输入 token
	Estimated  bool   `json:"prompt_estimated"` // 它是估算的还是厂商报的
	Window     int    `json:"window_tokens"`    // 分母
	Source     string `json:"window_source"`    // manual | table | default
	Note       string `json:"window_note"`      // 分母是怎么来的（人能读的一句）
	Pct        int    `json:"window_pct"`       // 占用百分比
	CarryTurns int    `json:"carry_turns"`      // 下一轮会带几轮最近对话
}

func (a *Agent) windowReading() windowReading {
	last, est := a.lastPrompt()
	win, src, note := a.contextWindow()
	rd := windowReading{LastTokens: last, Estimated: est, Window: win, Source: src, Note: note, Pct: windowPct(last, win)}
	if a.Mem != nil {
		rd.CarryTurns = a.Mem.CarryTurns()
	}
	return rd
}

// compressAfterTask 任务结束时的处置。**两件事分开办，因为它们回答的是两个问题**：
//
//  1. 汇总溢出轮：只要还有溢出就做。溢出轮早就被挤出提示了，不汇总它们等于把
//     "更早的上下文"永久丢掉——这与水位无关，是"满了不会直接丢"那条承诺的兑现；
//  2. 水位到 90% 时再收紧携带轮数：**这一步才真的把下一次请求压下来**。
//
// 返回（是否汇总了、是否收紧了），供进度里如实说一句。
func (a *Agent) compressAfterTask(ctx context.Context, taskID string) (compressed, tightened bool) {
	if !a.compressionOn() {
		return false, false
	}
	rd := a.windowReading()
	return a.applyCompression(ctx, taskID, rd.Pct >= compressAtEndPct)
}

// compressMidTask 任务进行中：只在水位越过 96% 时动手，压完继续把任务做完。
// 掉回 70% 以下就松开携带轮数——有回差是刻意的，没有回差会在阈值附近每轮抖一次。
func (a *Agent) compressMidTask(ctx context.Context, taskID string) (compressed, tightened bool) {
	if !a.compressionOn() {
		return false, false
	}
	if a.windowReading().Pct < compressMidPct {
		a.Mem.SetCarryTurns(memory.CarryFull)
		return false, false
	}
	return a.applyCompression(ctx, taskID, true)
}

// compressionOn 自动压缩开着、而且记忆系统在。
func (a *Agent) compressionOn() bool {
	return a.Cfg != nil && a.Cfg.Agent.ContextCompress && a.Mem != nil
}

// contextActionText 压缩/收紧之后给用户的一句人话。**别只报"已压缩"**：
// 用户真正想知道的是"我的上下文现在紧不紧、下一轮会少带多少"。
// mid=true 表示这是任务进行中（还没结束）触发的那一次。
func (a *Agent) contextActionText(compressed, tightened, mid bool) string {
	rd := a.windowReading()
	var b strings.Builder
	switch {
	case compressed && tightened:
		b.WriteString("已把早期上下文汇总成摘要，并把下一轮携带的对话收紧")
	case compressed:
		b.WriteString("已把早期上下文汇总成摘要")
	case tightened:
		b.WriteString("已把下一轮携带的对话收紧")
	}
	fmt.Fprintf(&b, "（窗口占用 %d%%，窗口 %s，下一轮带 %d 轮最近对话）",
		rd.Pct, humanTokens(rd.Window), rd.CarryTurns)
	if mid {
		b.WriteString("；任务继续")
	}
	return b.String()
}

// notifyContextAction 把上面那句话报到进度里。阶段用 done：它发生在任务跑完之后。
func (a *Agent) notifyContextAction(compressed, tightened, mid bool) {
	if a.Notifier == nil {
		return
	}
	phase := "done"
	if mid {
		phase = "context"
	}
	a.Notifier.OnProgress(types.ProgressEvent{
		Phase: phase, Message: a.contextActionText(compressed, tightened, mid), Progress: 100, Kind: "info",
	})
}

// humanTokens 窗口大小说人话：128000 → 128k。读数里没人愿意数后面的零。
func humanTokens(n int) string {
	if n >= 1000 {
		return strconv.Itoa(n/1000) + "k"
	}
	return strconv.Itoa(n)
}

// applyCompression 真正动手：汇总溢出轮，需要时再把下一轮携带的轮数收紧。
//
// 收紧是"真的腾出上下文"的那一半：只汇总溢出轮几乎不会让水位降下来——那些轮本来
// 就已经不在提示里了（提示带的是摘要 + 最近几轮）。让水位降下来的动作是**下一轮少带几轮**。
func (a *Agent) applyCompression(ctx context.Context, taskID string, tighten bool) (compressed, tightened bool) {
	compressed = a.Mem.Compress(a.CompressSummarizer(ctx, taskID))
	if tighten {
		tightened = a.Mem.SetCarryTurns(memory.CarryTight)
	}
	return compressed, tightened
}
