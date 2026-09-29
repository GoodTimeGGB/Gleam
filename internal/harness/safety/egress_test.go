package safety

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- 数据出网留痕（P1-3） ----------

// TestRecordEgressKeepsHostAndSizeOnly 出网留痕的边界：只记"发给了谁、发了多少"，
// **绝不记内容**。把请求正文写进 audit.jsonl，等于把风险从"网络"搬到"磁盘上的日志"——
// 审计本身就成了新的泄露面。
func TestRecordEgressKeepsHostAndSizeOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	g := New("auto", nil, nil, nil, time.Second)
	g.SetAuditPath(path)

	g.RecordEgress("llm", "api.deepseek.com", 12345)

	entries, err := LoadAuditLog(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("应有 1 条留痕，实际 %d", len(entries))
	}
	e := entries[0]
	if e.Action != "egress" {
		t.Errorf("动作应为 egress，实际 %q", e.Action)
	}
	if e.Tool != "llm" {
		t.Errorf("工具应为 llm，实际 %q", e.Tool)
	}
	if e.Egress != "llm:api.deepseek.com" {
		t.Errorf("结构化目标不对，实际 %q", e.Egress)
	}
	// 人读的那一列也要能直接看出"发给了谁、发了多少"
	if !strings.Contains(e.Reason, "api.deepseek.com") || !strings.Contains(e.Reason, "12345") {
		t.Errorf("说明里应含主机与字节数，实际 %q", e.Reason)
	}
	if e.Detail != "" {
		t.Errorf("出网留痕不该带明细（那是给正文留的口子），实际 %q", e.Detail)
	}
}

// TestRecordEgressDropsEmptyHost 主机名未知时不记——
// 一条"发给了谁都不知道"的出网记录对审计毫无价值，只会稀释真正有用的行。
func TestRecordEgressDropsEmptyHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	g := New("auto", nil, nil, nil, time.Second)
	g.SetAuditPath(path)

	g.RecordEgress("llm", "", 100)
	g.RecordEgress("", "example.com", 100)

	entries, err := LoadAuditLog(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("主机名或类型为空时不该留痕，实际 %+v", entries)
	}
}

// TestRecordEgressNilGateSafe 未装配门控时（nil）不能崩——审计是旁路。
func TestRecordEgressNilGateSafe(t *testing.T) {
	var g *Gate
	g.RecordEgress("llm", "example.com", 1) // 不应 panic
}

// TestEgressStatsCountsFromStructuredFields 「连接与出网」台账的读数层。
//
// 三条断言各自钉一件事：
//   - 字节数取结构化字段：台账要加总，而加总若靠解析 Reason 那句中文，
//     改一个标点就断，且断的方向是"少算"——报出"一共发出去 0 字节"比不报更坏。
//   - 只数 egress 动作：审批/拦截留痕一起算，出网次数就成了假数。
//   - 统计范围（Scanned/Cap）随读数一起给：内存环只有最近若干条，
//     界面若不带上这个范围，读者会把它当成"这台机器上一共发了多少"。
func TestEgressStatsCountsFromStructuredFields(t *testing.T) {
	g := New("auto", nil, nil, nil, time.Second)
	g.RecordEgress("llm", "api.deepseek.com", 1200)
	g.RecordEgress("llm", "api.deepseek.com", 800)
	g.RecordEgress("llm", "open.bigmodel.cn", 500)
	g.RecordEgress("web.fetch", "example.com", 60)
	g.Record(AuditEntry{Tool: "file.write", Action: "approved", Reason: "路径均在信任路径内"})

	rep := g.EgressStats()
	if rep.Scanned != 5 || rep.Cap != auditCap {
		t.Errorf("统计范围应随读数一起给出：%+v（应扫 5 条、容量 %d）", rep, auditCap)
	}
	byKind := map[string]EgressStat{}
	for _, s := range rep.Stats {
		byKind[s.Kind] = s
	}
	llmStat, ok := byKind["llm"]
	if !ok {
		t.Fatalf("台账里没有 llm 这一类：%+v", rep.Stats)
	}
	if llmStat.Count != 3 || llmStat.Bytes != 2500 {
		t.Errorf("llm 应为 3 次 / 2500 字节，实际 %+v", llmStat)
	}
	if len(llmStat.Hosts) != 2 || llmStat.Hosts[0] != "api.deepseek.com" {
		t.Errorf("主机应去重并排序，实际 %+v", llmStat.Hosts)
	}
	if _, bad := byKind["file.write"]; bad {
		t.Errorf("非出网留痕不能混进出网统计：%+v", rep.Stats)
	}
	if webStat, ok := byKind["web.fetch"]; !ok || webStat.Hosts[0] != "example.com" {
		t.Errorf("web.fetch 一类读数不对：%+v", rep.Stats)
	}
}

// TestEgressStatsNilGateSafe 未装配门控时台账仍能出题（空读数 + 容量）。
func TestEgressStatsNilGateSafe(t *testing.T) {
	var g *Gate
	rep := g.EgressStats()
	if rep.Scanned != 0 || len(rep.Stats) != 0 || rep.Cap != auditCap {
		t.Errorf("nil 门控应返回空读数，实际 %+v", rep)
	}
}
