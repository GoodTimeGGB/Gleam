package eval

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/config"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/llm"
	"gleam/internal/tools/std"
	"gleam/pkg/types"
)

// ---------- 用量：让"这批评测花了多少"有答案（批次 F-1 / 清单 P1）----------

// usageLLM 规划 / 反思两用的桩，**显式调用 llm.ReportUsage**。
//
// 这一点是测试能不能成立的关键：用量是靠 ReportUsage 广播到引擎的
// （见该函数的注释："自定义客户端（含测试替身）也应调用它"）。
// 桩"忘了报"，测试就只会测出一个 0——那种绿是最坏的一种，因为它看起来通过了。
type usageLLM struct {
	n atomic.Int64
}

func (m *usageLLM) Chat(_ context.Context, req llm.ChatRequest) (string, error) {
	text := `{"score":90,"verdict":"done","reason":"ok"}`
	if llm.KindOf(req.System) == "plan" {
		text = `{"steps":[{"id":"s1","tool":"reply","args":{"text":"好了"}}],"estimated_time":"short"}`
	}
	m.n.Add(1)
	// 每次调用固定报 100 输入 / 20 输出，便于断言"合计 = 次数 × 每次"。
	llm.ReportUsage(req, text, llm.Usage{PromptTokens: 100, CompletionTokens: 20})
	return text, nil
}

func (m *usageLLM) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (string, error) {
	return m.Chat(ctx, req)
}

func (m *usageLLM) Name() string { return "usage-stub" }

func (m *usageLLM) calls() int { return int(m.n.Load()) }

// fullDepthRunner 装一个最小可跑的 full 深度评测器。
//
// full 会**真的执行工具**（写文件、调 shell），所以 CWD 指向临时目录——
// 与 CLI 的做法一致（cmd/gleam/eval.go 里也是这个理由）。
func fullDepthRunner(t *testing.T, client llm.Client, repeat int) *Runner {
	t.Helper()
	cfg := config.Default()
	// mock provider：避免档位逻辑去建真实客户端（离线测试不该产生网络调用）
	cfg.LLM.Provider = "mock"
	// 数据目录必须指到临时目录。默认的 config.Default() 里 DataDir 是空串，
	// 而运行日志的路径是 filepath.Join(DataDir, "runs")——空串拼出来就是相对路径 "runs"，
	// 相对谁？相对 go test 的 cwd，也就是**包目录**。结果是每跑一次
	// `go test ./internal/eval/` 都往源码树里写 16 个 runs/*.jsonl，
	// 而且因为它们是"运行时产物"，git status 里会以未跟踪文件的样子一直晃，
	// 直到有人顺手提交进仓库。这不是洁癖：测试写进源码树是**污染**，
	// 它会让"工作区有没有改动"这个判断变得不可信。
	cfg.DataDir = t.TempDir()
	reg := registry.New()
	reg.MustRegister(std.NewReply())
	gate := safety.New("auto", nil, nil, nil, time.Second)
	a := agent.New(cfg, client, reg, nil, gate, nil, nil, nil)
	return &Runner{Agent: a, Depth: DepthFull, CWD: t.TempDir(), Repeat: repeat}
}

// TestFullDepthReportsUsage 是批次 F-1 的核心断言：full 深度的报告必须回答"这批评测花了多少"。
//
// 以前 runCaseOnce 拿到了 GoalResult 却把 Usage 丢了——报告只有 PromptChars
// （**成本代理**：提示词多长），没有 token（**成本本体**：实际花了多少）。
// 而评测的存在理由之一就是"给提示词做减法"：减法要算账，
// 账不能只存在于终端的回滚缓冲里。
func TestFullDepthReportsUsage(t *testing.T) {
	stub := &usageLLM{}
	rep := fullDepthRunner(t, stub, 1).Run(context.Background(), []Case{{ID: "u1", Goal: "打个招呼"}})

	if rep.Usage == nil {
		t.Fatal("full 深度的报告必须有用量合计")
	}
	// 用"等于桩实际调用次数"而不是写死一个数：写死会随循环多一次反思就过期，
	// 而这条断言真正要守的是"每一次调用都被计上了"。
	if stub.calls() == 0 {
		t.Fatal("桩一次都没被调用，这条测试没测到东西")
	}
	if rep.Usage.LLMCalls != stub.calls() {
		t.Errorf("报告记了 %d 次模型调用，桩实际调用 %d 次（有调用没被计入）", rep.Usage.LLMCalls, stub.calls())
	}
	if rep.Usage.PromptTokens != stub.calls()*100 || rep.Usage.CompletionTokens != stub.calls()*20 {
		t.Errorf("token 合计 = 输入%d/输出%d，应为 输入%d/输出%d",
			rep.Usage.PromptTokens, rep.Usage.CompletionTokens, stub.calls()*100, stub.calls()*20)
	}
	// 单条也要有：只有合计的话"哪条用例贵"就答不出来。
	c := rep.Cases[0]
	if c.Usage == nil {
		t.Fatal("用例级也要有用量——只看合计答不出'哪条贵'")
	}
	if c.Usage.TotalTokens() != rep.Usage.TotalTokens() {
		t.Errorf("单条合计 %d 与报告合计 %d 不一致（本次只有一条用例）",
			c.Usage.TotalTokens(), rep.Usage.TotalTokens())
	}
}

// TestRepeatUsageCountsEveryRun 守的是口径：`--repeat N` 的每一遍都真的调了模型。
//
// 只报首遍会让 `--repeat 3` 的成本看起来与 `--repeat 1` 一样——那是账错了，不是省了。
// 通过率仍取首遍（口径可比，见 Runner.Repeat 的注释），但用量必须把每一遍都算上。
func TestRepeatUsageCountsEveryRun(t *testing.T) {
	one := &usageLLM{}
	rep1 := fullDepthRunner(t, one, 1).Run(context.Background(), []Case{{ID: "u1", Goal: "打个招呼"}})

	three := &usageLLM{}
	rep3 := fullDepthRunner(t, three, 3).Run(context.Background(), []Case{{ID: "u1", Goal: "打个招呼"}})

	if rep1.Usage == nil || rep3.Usage == nil {
		t.Fatal("两边的报告都该有用量")
	}
	if three.calls() <= one.calls() {
		t.Fatalf("repeat 3 的模型调用（%d）应多于 repeat 1（%d）", three.calls(), one.calls())
	}
	if rep3.Usage.LLMCalls != three.calls() {
		t.Errorf("repeat 3 报告记了 %d 次调用，桩实际 %d 次（重跑的遍数被漏计了）",
			rep3.Usage.LLMCalls, three.calls())
	}
	// 通过率口径不受影响：仍然取首遍。这里只有一条用例、恒过，所以只要它没变成 0 就说明
	// 累加用量没有把 tally 的输入弄乱。
	if rep3.Passed != 1 {
		t.Errorf("通过率口径应仍取首遍，实得 Passed=%d", rep3.Passed)
	}
}

// TestSelectDepthHasNoUsage 守的是"nil 与 0 不是一回事"。
//
// select 深度走本地关键词打分、零模型调用，所以用量必须**缺席**而不是 0——
// 一个 0 会被读成"计量了但没花钱"，而真相是"这个深度压根不计量"。
// 这个区分正是 CaseResult.Usage 用指针而不是零值结构的理由。
func TestSelectDepthHasNoUsage(t *testing.T) {
	rep := (&Runner{Depth: DepthSelect}).Run(context.Background(), nil)
	if rep.Usage != nil {
		t.Errorf("select 深度不该有用量合计，实得 %+v", rep.Usage)
	}
}

// TestUsageFromTaskKeepsEveryCostField 把转换点钉住：TaskUsage → CaseUsage 少抄一个字段，
// 报告就会**少报一部分成本**，而且少报是看不出来的（数字变小不会报错）。
//
// 这里刻意把每个字段都填成互不相同的值：填成同一个数的话，
// 抄错字段（比如把 CachedTokens 写进 CachedCalls）也能通过。
func TestUsageFromTaskKeepsEveryCostField(t *testing.T) {
	in := types.TaskUsage{
		LLMCalls:         11,
		PromptTokens:     12,
		CompletionTokens: 13,
		EstimatedCalls:   14,
		CachedTokens:     15,
		CachedCalls:      16,
		ToolCalls:        17,
		Retries:          18,
		Deduped:          19,
		DurationMs:       20,
	}
	got := usageFromTask(in)
	want := &CaseUsage{
		LLMCalls: 11, PromptTokens: 12, CompletionTokens: 13,
		EstimatedCalls: 14, CachedTokens: 15, CachedCalls: 16, ToolCalls: 17,
	}
	if *got != *want {
		t.Errorf("转换结果 = %+v，应为 %+v", *got, *want)
	}
	if got.TotalTokens() != 25 {
		t.Errorf("TotalTokens = %d，应为 25", got.TotalTokens())
	}
}
