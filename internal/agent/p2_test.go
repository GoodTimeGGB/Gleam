package agent

import (
	"strings"
	"testing"

	"gleam/pkg/types"
)

// ---------- P2-1 钉住区注入 ----------

// 钉住区是易变段里唯一"每轮都必须在"的内容：
// 它必须被注入、排在易变段最前、按原顺序渲染（绝不重排）。
func TestBuildSystemPrompt_PinnedFirstInVolatile(t *testing.T) {
	p := &Planner{Reg: newTestRegistry()}
	p.Pinned = []string{"输出必须是 Markdown 表格", "永远不要动 /etc 目录"}
	sys := p.buildSystemPrompt("整理下载目录", "D:/ws", nil, nil, "早期摘要内容", "")

	if !strings.Contains(sys, "## 用户约束（钉住，永久有效）") {
		t.Fatalf("钉住区未被注入:\n%s", sys)
	}
	if !strings.Contains(sys, "- 输出必须是 Markdown 表格\n") || !strings.Contains(sys, "- 永远不要动 /etc 目录\n") {
		t.Errorf("钉住内容或顺序错误:\n%s", sys)
	}
	// 顺序语义：钉住区在易变段最前（上下文、摘要、对话、反馈都在它后面）
	iPin := strings.Index(sys, "用户约束（钉住")
	iSummary := strings.Index(sys, "早期上下文摘要")
	iCtx := strings.Index(sys, "## 上下文")
	if !(iCtx >= 0 && iPin < iCtx && iPin < iSummary) {
		t.Errorf("钉住区应排在易变段最前: pin=%d summary=%d ctx=%d", iPin, iSummary, iCtx)
	}
	// 钉住区在易变段（schema 之后）：不能破坏稳定段缓存前缀
	iSchema := strings.Index(sys, "本次可用工具")
	if iSchema > iPin {
		t.Errorf("钉住区排进了稳定段之前，会打碎缓存前缀: schema=%d pin=%d", iSchema, iPin)
	}
}

// Pinned 为空时提示词里不能出现空标题（空段落只会稀释注意力）。
func TestBuildSystemPrompt_NoPinnedNoSection(t *testing.T) {
	p := &Planner{Reg: newTestRegistry()}
	sys := p.buildSystemPrompt("整理下载目录", "D:/ws", nil, nil, "", "")
	if strings.Contains(sys, "用户约束") {
		t.Errorf("无钉住内容不应出现钉住区标题:\n%s", sys)
	}
}

// BuildPlanner 必须把 Manager 的钉住区带上——字段存在 ≠ 语义存在（P1 的教训）。
func TestBuildPlanner_CarriesPinned(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Mem.Pin("用户约束 A")
	pl := f.a.BuildPlanner(types.GoalRequest{Goal: "整理下载目录"})
	if len(pl.Pinned) != 1 || pl.Pinned[0] != "用户约束 A" {
		t.Fatalf("BuildPlanner 未携带钉住区: %v", pl.Pinned)
	}
	// 摘要注入走省略量标注版本：fixture 窗口容量 20，须溢出才有摘要可标注
	a := f.a.Mem
	for i := 0; i < 22; i++ {
		a.AddTurn("user", "对话轮次内容")
	}
	a.Compress(nil)
	if !strings.Contains(a.SummaryAnnotated(), "已自动压缩") {
		t.Error("SummaryAnnotated 应带省略量标注")
	}
}
