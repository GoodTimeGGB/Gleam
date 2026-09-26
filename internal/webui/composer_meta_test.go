package webui

import (
	"strings"
	"testing"
)

// TestContext_CarriesWaterLevel 输入区水位条的那条线。
//
// 为什么单独测：`fill_pct` 由 memory.fillPct 算出来，一路要经过
// Manager.Stats → Agent.ContextView → handleContextGet → JSON 四道手。
// 每一道都可能把它漏掉，而漏掉的**表现不是报错，是水位条一直 0%**——
// 一个看起来"还很空"的假健康。所以走真实路由断言，而不是直接调门面。
//
// 测试自己按 short_turns/short_cap 除一遍是故意的：这里是**交叉核对**，
// 不是把实现的公式抄一遍。前端不许自己除（那才会出现两个面板两个数）。
func TestContext_CarriesWaterLevel(t *testing.T) {
	f := newFixture(t, nil)

	first := f.call("GET", "/api/context", nil)
	pct, ok := first["fill_pct"]
	if !ok {
		t.Fatal(`/api/context 没有 fill_pct 字段：水位条没有数据来源，会永远停在"0%"`)
	}
	p, ok := pct.(float64)
	if !ok {
		t.Fatalf("fill_pct 应为数字，实际 %T", pct)
	}
	if p != 0 {
		t.Errorf("空会话的水位 = %v，应为 0", p)
	}

	// 往短期窗口里塞 5 轮（容量 20，见 newFixture）：水位应到 25%。
	for i := 0; i < 5; i++ {
		f.agent.Mem.AddTurn("user", "填满窗口的一轮")
	}
	got := f.call("GET", "/api/context", nil)
	turns, _ := got["short_turns"].(float64)
	cap_, _ := got["short_cap"].(float64)
	want := 100 * turns / cap_
	if v, _ := got["fill_pct"].(float64); v != want {
		t.Errorf("水位 = %v%%，按回传的 %v/%v 轮应为 %v%%", got["fill_pct"], turns, cap_, want)
	}
	if v, _ := got["fill_pct"].(float64); v < 0 || v > 100 {
		t.Errorf("水位 = %v%%，越界会把水位条画溢出那一栏", v)
	}
}

// TestSettings_ModelOnlyPatchIsAccepted 「就地切模型」这条路的最小证明。
//
// 输入区的模型弹层只发 {"llm":{"model":"..."}} 这一个键。设置页发的是整段，
// 所以"能保存模型"从来不证明"只发一个键也能保存"——补丁要是被当成整段替换，
// 切一次模型就会把 base_url/协议/档位清空，而那要下次调用模型时才炸。
//
// Mock 厂商不改客户端（见 ApplySettings），所以这里断言的是配置层的回显与留存，
// 不是 /api/info 的模型名。
func TestSettings_ModelOnlyPatchIsAccepted(t *testing.T) {
	f := newFixture(t, nil)

	// 先摆两个"会被清空"的靶子：档位表和辅助模型。缺了它们，
	// len(tiers) 0==0、""=="" 都会虚假通过，这条断言就只是句话。
	f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{
			"fast_model": "cheap-helper",
			"tiers":      map[string]any{"coding": "deepseek-coder", "office": "glm-5.3-flash"},
		},
	})

	before := f.call("GET", "/api/settings", nil)
	baseBefore, _ := dot(before, "llm.base_url").(string)
	if baseBefore == "" {
		t.Fatal("夹具的 base_url 为空，下面的断言会虚假通过")
	}
	tiersBefore, _ := dot(before, "llm.tiers").(map[string]any)
	if len(tiersBefore) == 0 {
		t.Fatal("夹具的档位表为空，「清空档位」那条断言会虚假通过")
	}

	out := f.call("POST", "/api/settings", map[string]any{
		"llm": map[string]any{"model": "just-switching-model"},
	})
	if got := dot(out, "llm.model"); got != "just-switching-model" {
		t.Fatalf("补丁后 llm.model = %v，应为 just-switching-model（切了没生效，界面还显示旧名字）", got)
	}
	// 同一次响应里，没发的键必须还在——这一条才是"整段替换"事故的负例。
	if baseAfter, _ := dot(out, "llm.base_url").(string); baseAfter != baseBefore {
		t.Errorf("只发 model 就把 base_url 从 %q 改成了 %q：补丁被当成整段替换", baseBefore, baseAfter)
	}
	if fmAfter, _ := dot(out, "llm.fast_model").(string); fmAfter != "cheap-helper" {
		t.Errorf("只发 model 就把辅助模型从 %q 改成了 %q：压缩/GEO 会悄悄退回主模型，贵一档", "cheap-helper", fmAfter)
	}
	tiersAfter, _ := dot(out, "llm.tiers").(map[string]any)
	if len(tiersAfter) != len(tiersBefore) {
		t.Errorf("只发 model 就把档位表从 %d 条变成 %d 条", len(tiersBefore), len(tiersAfter))
	}

	// 再看一次：证明改动落在生效的配置里，而不是只拼进了那次响应。
	if got := dot(f.call("GET", "/api/settings", nil), "llm.model"); got != "just-switching-model" {
		t.Errorf("重新 GET 的 llm.model = %v，改动没留在运行时配置里", got)
	}
}

// dot 按 "a.b" 逐层下钻取值，取不到返回 nil。
func dot(m map[string]any, path string) any {
	cur := any(m)
	for _, part := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = mm[part]
		if !ok {
			return nil
		}
	}
	return cur
}
