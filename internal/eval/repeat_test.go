package eval

import (
	"context"
	"strings"
	"testing"

	"gleam/internal/agent"
	"gleam/internal/config"
	"gleam/internal/harness/registry"
	"gleam/internal/llm"
	"gleam/internal/tools/std"
)

// ---------- 可重复性：通过率回答"对不对"，这里回答"稳不稳" ----------

// planLLM 规划阶段的可控桩：vary=true 时每次规划换一个步数，用来构造"重跑不一致"。
//
// 为什么不用 MockClient 的脚本队列：队列用尽后会掉回默认响应，
// 第三遍就"自己变稳"了——那测的是队列长度，不是可重复性。
type planLLM struct {
	vary bool
	n    int
}

func (m *planLLM) Chat(_ context.Context, req llm.ChatRequest) (string, error) {
	if llm.KindOf(req.System) != "plan" {
		return `{"score":85,"verdict":"done","reason":"ok"}`, nil
	}
	m.n++
	if m.vary && m.n%2 == 0 {
		return `{"steps":[{"id":"s1","tool":"reply","args":{"text":"a"}}],"estimated_time":"short"}`, nil
	}
	return `{"steps":[{"id":"s1","tool":"reply","args":{"text":"a"}},` +
		`{"id":"s2","tool":"reply","args":{"text":"b"}}],"estimated_time":"short"}`, nil
}

func (m *planLLM) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (string, error) {
	return m.Chat(ctx, req)
}

func (m *planLLM) Name() string { return "plan-stub" }

// planDepthRunner 装一个最小可跑的 plan 深度评测器。
// plan 深度只用到规划器（Reg / Cfg / LLM），所以记忆、安全闸门、技能、调度都可以为 nil。
func planDepthRunner(t *testing.T, client llm.Client, repeat int) *Runner {
	t.Helper()
	cfg := config.Default()
	// mock provider：避免档位逻辑去建真实客户端（离线测试不该产生网络调用）
	cfg.LLM.Provider = "mock"
	reg := registry.New()
	reg.MustRegister(std.NewReply())
	a := agent.New(cfg, client, reg, nil, nil, nil, nil, nil)
	return &Runner{Agent: a, Depth: DepthPlan, CWD: t.TempDir(), Repeat: repeat}
}

// TestRepeatFlagsUnstableCase 重跑得到不同结果时必须标出来——这是 P1-1 的核心。
//
// 不标出来的后果很具体：一条 Stable=false 的用例，它这次的绿可能只是掷硬币掷出来的，
// 而人会拿它当"改动有效"的证据。上一轮减法实验正是栽在这里
// （噪声基线 3.00/5.67 > 处理效应 4.83，结论根本不可归因）。
func TestRepeatFlagsUnstableCase(t *testing.T) {
	r := planDepthRunner(t, &planLLM{vary: true}, 3)
	rep := r.Run(context.Background(), []Case{{ID: "u1", Goal: "随便做点什么"}})

	if rep.RepeatN != 3 {
		t.Fatalf("报告应记下重跑遍数，实际 %d", rep.RepeatN)
	}
	c := rep.Cases[0]
	if c.Runs != 3 {
		t.Fatalf("用例应记下重跑遍数，实际 %d", c.Runs)
	}
	if c.Stable {
		t.Fatal("三次运行步数不同，不能判为稳定")
	}
	if c.Agreed != 2 {
		t.Errorf("应记下与最常见结果一致的遍数 2，实际 %d", c.Agreed)
	}
	if len(c.Variants) != 2 {
		t.Fatalf("应报出 2 种结果（2 步 ×2、1 步 ×1），实际 %v", c.Variants)
	}
	if !strings.Contains(c.Variants[0], "×2") {
		t.Errorf("次数多的应排前面，实际 %v", c.Variants)
	}
	if rep.Repeatability == nil || *rep.Repeatability != 0 {
		t.Errorf("这条用例不稳，一致率应为 0，实际 %v", rep.Repeatability)
	}
	if len(rep.UnstableCases) != 1 || rep.UnstableCases[0] != "u1" {
		t.Errorf("应报出不稳的用例 ID，实际 %v", rep.UnstableCases)
	}
	// 口径必须落在摘要里：两个数不能互相替代，所以得同时出现
	if !strings.Contains(rep.SummaryLine(), "可重复性") {
		t.Errorf("一行摘要里也要有可重复性，实际 %q", rep.SummaryLine())
	}
}

// TestRepeatStableCase 每次结果相同时一致率 100%，且不误报不稳。
func TestRepeatStableCase(t *testing.T) {
	r := planDepthRunner(t, &planLLM{}, 3)
	rep := r.Run(context.Background(), []Case{{ID: "s1", Goal: "随便做点什么"}})

	c := rep.Cases[0]
	if !c.Stable || c.Agreed != 3 {
		t.Errorf("结果一致时应 Stable/Agreed=3，实际 %+v", c)
	}
	if len(c.Variants) != 0 {
		t.Errorf("稳定用例不该报差异明细，实际 %v", c.Variants)
	}
	if rep.Repeatability == nil || *rep.Repeatability != 1 {
		t.Errorf("一致率应为 1，实际 %v", rep.Repeatability)
	}
	if rep.StableCases != 1 || len(rep.UnstableCases) != 0 {
		t.Errorf("稳定用例计数不对：stable=%d unstable=%v", rep.StableCases, rep.UnstableCases)
	}
}

// TestRepeatOffKeepsOldShape 默认（不重复采样）时报告形态与从前一致：
// 不出现一致率字段，通过率口径也不变——否则基线全部失效。
func TestRepeatOffKeepsOldShape(t *testing.T) {
	for _, n := range []int{0, 1} {
		r := planDepthRunner(t, &planLLM{}, n)
		rep := r.Run(context.Background(), []Case{{ID: "s1", Goal: "随便做点什么"}})
		if rep.Repeatability != nil || rep.RepeatN != 0 {
			t.Errorf("Repeat=%d 时不该报可重复性，实际 n=%d rate=%v", n, rep.RepeatN, rep.Repeatability)
		}
		if rep.Cases[0].Runs != 1 || !rep.Cases[0].Stable {
			t.Errorf("Repeat=%d 时单遍运行应记 Runs=1/Stable=true，实际 %+v", n, rep.Cases[0])
		}
	}
}

// TestRunSignatureOnlyCountsRealDifferences 指纹的判据：什么算"不同"。
//
// 工具顺序不算（模型换个排列只是措辞），工具集合、步数、通过与否才算。
// 判据写宽了会把噪声算成不稳定，写窄了会漏掉真差异——两边都会让这个指标失去意义。
func TestRunSignatureOnlyCountsRealDifferences(t *testing.T) {
	base := runSignature(true, 3, []string{"file.read", "file.list"})

	if got := runSignature(true, 3, []string{"file.list", "file.read"}); got != base {
		t.Errorf("工具顺序不该算差异：%q vs %q", got, base)
	}
	if runSignature(true, 3, []string{"file.read"}) == base {
		t.Error("工具集合少了东西必须算差异")
	}
	if runSignature(false, 3, []string{"file.read", "file.list"}) == base {
		t.Error("通过与否不同必须算差异")
	}
	if runSignature(true, 4, []string{"file.read", "file.list"}) == base {
		t.Error("步数不同必须算差异")
	}
}

// TestDescribeVariantsOrdersByCount 差异明细按出现次数从多到少，
// 并列时保持首次出现顺序（保证同一份输入永远输出同一份报告）。
func TestDescribeVariantsOrdersByCount(t *testing.T) {
	got := describeVariants([]string{"b", "a", "b", "c", "b", "a"})
	want := []string{"b ×3", "a ×2", "c ×1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("差异明细排序不对：实际 %v，期望 %v", got, want)
	}
}
