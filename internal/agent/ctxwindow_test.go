package agent

import (
	"context"
	"strings"
	"testing"

	"gleam/internal/harness/memory"
	"gleam/internal/llm"
)

// 占用水位的算法。向上取整是刻意的：95.6% 说成 96% 只是早 0.4% 触发一次压缩，
// 说成 95% 则可能整整一轮都不动——两个方向的代价不对称。
func TestWindowPct(t *testing.T) {
	cases := []struct{ last, window, want int }{
		{0, 128000, 0},
		{115200, 128000, 90},  // 正好 90%
		{115100, 128000, 90},  // 89.9% → 向上取整
		{128000, 128000, 100}, // 满
		{200000, 128000, 100}, // 超了也钳到 100：界面拿它画宽度，105% 会溢出那一栏
		{1000, 0, 0},          // 没有分母
		{1000, 128000, 1},
	}
	for _, c := range cases {
		if got := windowPct(c.last, c.window); got != c.want {
			t.Errorf("windowPct(%d, %d) = %d，应为 %d", c.last, c.window, got, c.want)
		}
	}
}

// 分母的三个来源：手填优先，其次内置表，最后兜底。
// 手填优先是"用户说了算"的落点——他不认可内置表里的数就该能改。
func TestContextWindowResolution(t *testing.T) {
	f := newFixture(t, nil)

	f.a.Cfg.LLM.Model = "claude-sonnet-4"
	if got, src, _ := f.a.contextWindow(); got != 200000 || src != llm.SourceTable {
		t.Errorf("内置表：得到 %d/%s", got, src)
	}
	f.a.Cfg.LLM.Model = "谁也没见过的模型"
	if got, src, _ := f.a.contextWindow(); got != llm.DefaultContextWindow || src != llm.SourceDefault {
		t.Errorf("兜底：得到 %d/%s", got, src)
	}
	f.a.Cfg.LLM.ContextWindow = 32000
	if got, src, _ := f.a.contextWindow(); got != 32000 || src != llm.SourceManual {
		t.Errorf("手填应当压过内置表：得到 %d/%s", got, src)
	}
}

// 任务结束时的两条触发线：
//   - 水位 < 90% 且没有溢出 → 什么都不做（为了省一点而下一次开场多花一次摘要调用，不值）；
//   - 水位 ≥ 90% → **收紧**下一轮携带（这一步才真的把下一次请求压下来）；
//   - 水位不高但有溢出轮 → 仍然汇总（溢出轮早就不在提示里了，不汇总=把早期上下文丢掉）。
func TestCompressAfterTask(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.LLM.ContextWindow = 1000
	ctx := context.Background()

	// 1) 水位低、没有溢出
	f.a.notePromptSize(llm.Usage{PromptTokens: 500})
	if c, tn := f.a.compressAfterTask(ctx, "t1"); c || tn {
		t.Errorf("水位 50%% 且无溢出时不该动上下文（得到 %v/%v）", c, tn)
	}
	if got := f.a.Mem.CarryTurns(); got != memory.CarryFull {
		t.Errorf("不该收紧，实际携带 %d 轮", got)
	}

	// 2) 水位到 90%
	f.a.notePromptSize(llm.Usage{PromptTokens: 900})
	c, tn := f.a.compressAfterTask(ctx, "t1")
	if !tn {
		t.Error("水位 90% 应当收紧下一轮携带")
	}
	_ = c
	if got := f.a.Mem.CarryTurns(); got != memory.CarryTight {
		t.Errorf("应当收紧到 %d 轮，实际 %d", memory.CarryTight, got)
	}

	// 3) 水位低但有溢出：仍然要汇总（这是"满了不会直接丢"那条承诺）
	f2 := newFixture(t, nil)
	f2.a.Cfg.LLM.ContextWindow = 1000
	f2.a.notePromptSize(llm.Usage{PromptTokens: 100})
	for i := 0; i < f2.a.Cfg.Memory.ShortTermCap+3; i++ {
		f2.a.Mem.AddTurn("user", "第几轮不重要")
	}
	if f2.a.Mem.Stats().Overflow == 0 {
		t.Fatal("前提不成立：应当已经攒出溢出轮")
	}
	if c, _ := f2.a.compressAfterTask(ctx, "t2"); !c {
		t.Error("有溢出轮就该汇总，与水位无关")
	}
}

// 任务进行中：只在水位越过 96% 时动手；压完继续（不结束任务）；
// 掉回 70% 以下松开携带轮数（有回差，免得在阈值附近每轮抖一次）。
func TestCompressMidTask(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.LLM.ContextWindow = 1000
	ctx := context.Background()

	f.a.notePromptSize(llm.Usage{PromptTokens: 950})
	if _, tn := f.a.compressMidTask(ctx, "t1"); tn {
		t.Error("95% 还不该在任务中动手（96% 才是那条线）")
	}
	f.a.notePromptSize(llm.Usage{PromptTokens: 960})
	if _, tn := f.a.compressMidTask(ctx, "t1"); !tn {
		t.Error("96% 应当收紧")
	}
	if got := f.a.Mem.CarryTurns(); got != memory.CarryTight {
		t.Errorf("应当收紧到 %d 轮，实际 %d", memory.CarryTight, got)
	}
	// 压完水位掉回来 → 松开
	f.a.notePromptSize(llm.Usage{PromptTokens: 400})
	f.a.compressMidTask(ctx, "t1")
	if got := f.a.Mem.CarryTurns(); got != memory.CarryFull {
		t.Errorf("水位掉回 40%% 应当松回 %d 轮，实际 %d", memory.CarryFull, got)
	}
}

// 自动压缩关掉时，任何一条路径都不许动上下文——这个开关是用户的，不是建议。
func TestCompressionOffDoesNothing(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.LLM.ContextWindow = 1000
	f.a.Cfg.Agent.ContextCompress = false
	f.a.notePromptSize(llm.Usage{PromptTokens: 999})
	ctx := context.Background()

	if c, tn := f.a.compressMidTask(ctx, "t1"); c || tn {
		t.Error("自动压缩关着时任务中不该动上下文")
	}
	if c, tn := f.a.compressAfterTask(ctx, "t1"); c || tn {
		t.Error("自动压缩关着时任务结束也不该动上下文")
	}
}

// 读数要带齐"这个百分比是怎么来的"：分母、来源、实测还是估算。
// 少任何一项，界面上那个数字就会变成一个看起来很确定、其实无从核对的数。
func TestContextViewCarriesWindowReading(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.LLM.ContextWindow = 1000
	f.a.notePromptSize(llm.Usage{PromptTokens: 123, Estimated: true})

	v := f.a.ContextView()
	if v["window_tokens"] != 1000 || v["window_source"] != llm.SourceManual {
		t.Errorf("分母与来源不对：%v / %v", v["window_tokens"], v["window_source"])
	}
	if v["prompt_tokens"] != 123 || v["prompt_estimated"] != true {
		t.Errorf("读数应当是 123（估算）：%v / %v", v["prompt_tokens"], v["prompt_estimated"])
	}
	if v["window_pct"] != 13 { // 12.3% 向上取整
		t.Errorf("占用应为 13%%，实际 %v", v["window_pct"])
	}
	if v["carry_turns"] != memory.CarryFull {
		t.Errorf("应当带上携带轮数，实际 %v", v["carry_turns"])
	}
	// 阈值也由后端给：前端不另写一份数字，改的时候不会两头漂
	if v["compress_end_pct"] != compressAtEndPct || v["compress_mid_pct"] != compressMidPct {
		t.Errorf("阈值没带出去：%v / %v", v["compress_end_pct"], v["compress_mid_pct"])
	}
	// 轮数口径仍在（记忆页那一套用它），两套口径各自说话
	if _, ok := v["fill_pct"]; !ok {
		t.Error("轮数口径不该被这次改动顶掉")
	}
}

// 给用户看的那句话要能回答"现在紧不紧"：带上占用、窗口、下一轮带几轮。
func TestContextActionText(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.LLM.ContextWindow = 1000
	f.a.notePromptSize(llm.Usage{PromptTokens: 970})
	// 真实顺序是"先收紧、再报这句"，所以这里先收紧：
	// 那句话里的"下一轮带几轮"必须报**动作之后**的数，报动作之前的等于没说。
	f.a.Mem.SetCarryTurns(memory.CarryTight)

	got := f.a.contextActionText(true, true, true)
	for _, want := range []string{"97%", "1k", "2 轮最近对话", "任务继续"} {
		if !strings.Contains(got, want) {
			t.Errorf("这句话里应当有 %q：%s", want, got)
		}
	}
}
