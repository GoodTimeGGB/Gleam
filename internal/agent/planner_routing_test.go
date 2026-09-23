package agent

import (
	"strings"
	"testing"

	"gleam/internal/harness/registry"
	"gleam/internal/textmatch"
)

// 本文件守的是本地关键词打分器的两条口径（见 planner.go 的 routingTextLimit 与
// scoreToolNames 注释）。这两条都是被 `gleam eval` 抓出来的真问题：
//
//	描述尾部的用法提醒/举例被当成了路由信号；
//	英文词按子串匹配，"go" 命中了 "goal"。
//
// 之所以要单测而不是只靠评测：评测跑的是真实工具描述，改动描述就会连带改动断言；
// 这里用假工具把"口径"本身钉死，与具体描述解耦。

// routingRegistry 只装待测工具，避免其他工具干扰打分。
func routingRegistry(tools ...*fakeTool) *registry.Registry {
	r := registry.New()
	for _, t := range tools {
		r.MustRegister(t)
	}
	return r
}

// TestRoutingIgnoresManualTail 守住「路由只看描述开头一段」。
//
// 工具描述里混着两类文字：讲"这是什么、什么时候用"的，和讲"参数怎么填、注意什么"的。
// 后者是写给已经选中该工具的模型看的，但同样含词，会被打分当成相关度——
// schedule.create 的「不要自己拼 cron」就这么在闲聊目标里排到了第 1。
func TestRoutingIgnoresManualTail(t *testing.T) {
	const goal = "你好，简单介绍一下你自己"

	// 关键词只出现在第 100 字之后（描述尾部）。
	tail := &fakeTool{name: "schedule.create", desc: strings.Repeat("说明文字。", 20) + "不要自己拼 cron"}
	p := &Planner{Reg: routingRegistry(tail), MaxToolSchemas: 12}
	if got := p.RelevantTools(goal); len(got) != 0 {
		t.Fatalf("描述尾部的用法提醒不该参与路由，却选到了 %v", got)
	}

	// 同一句话挪到开头 —— 必须命中。否则上面那条只是"永远选不到"，测不出东西。
	near := &fakeTool{name: "schedule.create", desc: "不要自己拼 cron"}
	p2 := &Planner{Reg: routingRegistry(near), MaxToolSchemas: 12}
	if got := p2.RelevantTools(goal); len(got) == 0 {
		t.Fatal("关键词落在描述开头时必须命中")
	}
}

// TestAlnumTermsMatchWholeWords 守住「英文/数字按整词匹配」。
//
// 目标是 go 时不该命中描述里的 goal；反过来，goal 也不该命中 goals。
func TestAlnumTermsMatchWholeWords(t *testing.T) {
	tool := &fakeTool{name: "alpha.tool", desc: "inspect goals field"}
	p := &Planner{Reg: routingRegistry(tool), MaxToolSchemas: 12}

	// 子串匹配下 "goal" 会命中 "goals"，整词匹配下不会。
	if got := p.RelevantTools("goal"); len(got) != 0 {
		t.Fatalf("goal 不该命中 goals，实际选到了 %v", got)
	}
	// 正面对照：整词命中必须生效。
	if got := p.RelevantTools("inspect"); len(got) == 0 {
		t.Fatal("整词命中必须生效，否则上面那条只是'永远选不到'")
	}
}

// TestShortAlnumTermsAreDropped 守住「两字母词不进检索词表」。
//
// go / in / is / id 这类词到处都是，当检索词只会制造噪音。
func TestShortAlnumTermsAreDropped(t *testing.T) {
	for _, w := range []string{"go", "in", "is", "id"} {
		if textmatch.Terms(w)[w] {
			t.Errorf("两字母词 %q 不该进入检索词表", w)
		}
	}
	if !textmatch.Terms("goa")["goa"] {
		t.Error("三字母词应当保留")
	}
}

// TestRoutingTextLimitIsEnforced 守住截断本身：超限要截、不超限不要画蛇添足。
func TestRoutingTextLimitIsEnforced(t *testing.T) {
	long := &fakeTool{name: "x.y", desc: strings.Repeat("甲", 300)}
	got := routingText(long)
	// 名称 + 空格 + 上限字数 + 省略号
	if n, limit := len([]rune(got)), len("x.y")+1+routingTextLimit+1; n > limit {
		t.Fatalf("路由文本没有被截断：%d 字，上限 %d", n, limit)
	}
	if !strings.Contains(got, "…") {
		t.Error("被截断时应当带省略号，排障时一眼能看出截过")
	}

	short := &fakeTool{name: "x.y", desc: "短描述"}
	if strings.Contains(routingText(short), "…") {
		t.Error("没超限就不该出现省略号")
	}
}

// TestRelevantToolsIsNotTheMenu 守住两者不是一回事——这正是评测断言必须用
// RelevantTools 而不是 SelectTools 的原因。
//
// reply 在最终菜单里被**强制保留**（selectTools 里 `t.Name() == "reply"`），
// 但它从不参与打分；而菜单末尾还有一条按注册顺序补齐的兜底，永远填满。
// 所以"某工具在菜单里"证明不了它相关。
func TestRelevantToolsIsNotTheMenu(t *testing.T) {
	p := &Planner{
		Reg:            routingRegistry(&fakeTool{name: "reply", desc: "直接向用户回复文字"}),
		MaxToolSchemas: 12,
	}
	if got := p.SelectTools("随便聊聊"); len(got) != 1 {
		t.Fatalf("菜单里应当有被强制保留的 reply，实际 %v", got)
	}
	if got := p.RelevantTools("随便聊聊"); len(got) != 0 {
		t.Fatalf("打分相关集里不该有 reply（它不参与打分），实际 %v", got)
	}
}

// TestNameHitWeighsMoreThanDescription 守住"名称命中权重更高"这条没有在重构中丢掉。
//
// 注意只能用**英文词**来测：工具名是英文（file.write），中文二元组永远不可能命中它，
// 所以中文目标下"名称加权"根本不会触发。
func TestNameHitWeighsMoreThanDescription(t *testing.T) {
	byName := &fakeTool{name: "file.write", desc: "unrelated"}
	byDesc := &fakeTool{name: "zzz.tool", desc: "write text files"}
	p := &Planner{Reg: routingRegistry(byName, byDesc), MaxToolSchemas: 12}

	got := p.RelevantTools("write")
	if len(got) != 2 {
		t.Fatalf("两个工具都该命中，实际 %v", got)
	}
	if got[0] != "file.write" {
		t.Fatalf("名称命中应排在描述命中之前，实际顺序 %v", got)
	}
}
