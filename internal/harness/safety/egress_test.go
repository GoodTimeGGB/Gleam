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
