package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// 配置快照：把"这次任务是用什么跑的"变成可核对的事实。
//
// 为什么不能靠事后留痕：一个跑几分钟的任务，中途用户在设置页改了模型或档位，
// 前半段和后半段用的就不是同一套配置了。结果里只有一个事后算出的规则指纹，
// **无法区分"是配置变了"还是"模型发挥不稳"**。

// 影响行为的关键项都要进快照。漏一项不会报错，只会在复盘时给出一个错的结论——
// 这种缺失是静默的，所以必须逐项钉住。
func TestSnapshot_CoversBehaviorFields(t *testing.T) {
	cfg := Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.Model = "gpt-x"
	cfg.LLM.FastModel = "gpt-x-mini"
	cfg.LLM.Tiers = map[string]string{"fast": "m-fast", "deep": "m-deep"}
	cfg.Safety.Mode = "strict"
	cfg.Agent.DoneThreshold = 77
	cfg.Agent.MaxReplans = 3
	cfg.Agent.MaxSteps = 11
	cfg.Agent.StepTimeoutSecs = 45
	cfg.Agent.StepRetries = 2
	cfg.Agent.MaxConcurrency = 4
	cfg.Agent.DedupeCalls = false
	cfg.Agent.MaxOutputRunes = 1234
	cfg.Agent.MaxToolSchemas = 5
	cfg.Agent.ReflectEachStep = true

	s := cfg.Snapshot()
	if s.Provider != "openai" || s.Model != "gpt-x" || s.FastModel != "gpt-x-mini" {
		t.Errorf("模型三项没跟上: %+v", s)
	}
	if s.SafetyMode != "strict" {
		t.Errorf("SafetyMode = %q", s.SafetyMode)
	}
	if s.DoneThreshold != 77 || s.MaxReplans != 3 || s.MaxSteps != 11 {
		t.Errorf("停止条件没跟上: %+v", s)
	}
	if s.StepTimeoutSecs != 45 || s.StepRetries != 2 || s.MaxConcurrency != 4 || s.DedupeCalls || s.MaxOutputRunes != 1234 {
		t.Errorf("单步执行参数没跟上: %+v", s)
	}
	if s.MaxToolSchemas != 5 || !s.ReflectEachStep {
		t.Errorf("上下文参数没跟上: %+v", s)
	}
	if s.Tiers["fast"] != "m-fast" || s.Tiers["deep"] != "m-deep" {
		t.Errorf("档位没跟上: %+v", s.Tiers)
	}
}

// 布尔项必须用 true 验。
//
// 这两个字段的默认值里 DedupeCalls 是 true、ReflectEachStep 是 false，
// 而 false 恰好是"字段根本没被拷贝"时的零值——只拿 false 去断言，
// 漏拷一处也照样通过。所以必须有一个 true 的用例把两件事分开。
func TestSnapshot_BoolFieldsAreCopied(t *testing.T) {
	cfg := Default()
	cfg.Agent.DedupeCalls = true
	cfg.Agent.ReflectEachStep = true

	s := cfg.Snapshot()
	if !s.DedupeCalls {
		t.Error("DedupeCalls 没被拷进快照——而它的默认值就是 true，只能靠显式设 true 验出来")
	}
	if !s.ReflectEachStep {
		t.Error("ReflectEachStep 没被拷进快照")
	}
}

// Tiers 必须是深拷贝。
//
// 不拷贝的话，快照与活配置共享同一个 map：用户改一次档位，就把**所有历史快照**
// 一起改掉——而快照存在的全部意义就是不跟着变。这类错误不会报错，
// 只会让复盘时看到的"当时的配置"其实是今天的配置。
func TestSnapshot_TiersIsDeepCopy(t *testing.T) {
	cfg := Default()
	cfg.LLM.Tiers = map[string]string{"fast": "m-fast"}

	s := cfg.Snapshot()
	cfg.LLM.Tiers["fast"] = "改过的档位"
	cfg.LLM.Tiers["new"] = "新增档位"

	if s.Tiers["fast"] != "m-fast" {
		t.Errorf("改活配置改动了快照：Tiers[fast] = %q，应为 m-fast", s.Tiers["fast"])
	}
	if _, ok := s.Tiers["new"]; ok {
		t.Error("活配置新增档位后快照里也出现了——两者共享了同一个 map")
	}
}

// 空配置（例如 Agent 尚未装配）取快照不能 panic，如实返回零值即可。
func TestSnapshot_NilConfig(t *testing.T) {
	var cfg *Config
	if s := cfg.Snapshot(); s.Model != "" || s.MaxSteps != 0 {
		t.Errorf("nil 配置应得到零值快照，实际 %+v", s)
	}
}

// 快照**刻意不记全量**，这条测试守的是"以后别顺手把整份配置塞进来"。
//
// 两个理由都具体：全量快照会把 API Key 明文写进 tasks/<id>.json；
// 而路径类字段（工作区、数据目录）属于"这次在哪跑"，不属于"用什么跑"——
// 把它们记进来，换台机器复盘就会看到一堆"配置变了"，真正影响结果的那几项反而看不出来。
func TestSnapshot_LeavesOutSecretsAndPaths(t *testing.T) {
	cfg := Default()
	cfg.LLM.APIKey = "sk-绝密-不该落盘"
	cfg.LLM.BaseURL = "https://internal.example/v1"
	cfg.Workspace = "D:/某个工作区"
	cfg.DataDir = "C:/Users/x/.gleam"

	b, err := json.Marshal(cfg.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	blob := string(b)
	for _, leak := range []string{"sk-绝密-不该落盘", "internal.example", "D:/某个工作区", "C:/Users/x/.gleam"} {
		if strings.Contains(blob, leak) {
			t.Errorf("快照里出现了 %q，它既不该进快照也不该明文落盘:\n%s", leak, blob)
		}
	}
}
