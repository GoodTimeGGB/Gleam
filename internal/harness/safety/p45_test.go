package safety

import (
	"os"
	"path/filepath"
	"testing"
)

// P5-2：审计必须落盘。内存环只够复盘最近 200 条，重启即失忆——
// 站点 F3 的判断是「副作用闸门 + 全量审计是唯一确定性的那层」。
func TestAudit_PersistedAsJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	g := New("auto", nil, nil, nil, 0)
	g.SetAuditPath(path)

	g.Record(AuditEntry{Tool: "shell.exec", Risk: "high", Action: "approved", Reason: "高风险操作"})
	g.Record(AuditEntry{Tool: "file.delete", Risk: "high", Action: "denied", Reason: "用户拒绝", Detail: "不想删"})
	g.Record(AuditEntry{Tool: "file.write", Risk: "medium", Action: "auto", Reason: "信任路径内"})

	entries, err := LoadAuditLog(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("落盘 %d 条，应为 3", len(entries))
	}
	// 新的在前
	if entries[0].Tool != "file.write" || entries[2].Tool != "shell.exec" {
		t.Errorf("顺序应新的在前: %+v", entries)
	}
	// 被拒的也要在（"没人看过的拦截等于没发生过"）
	var denied *AuditEntry
	for i := range entries {
		if entries[i].Action == "denied" {
			denied = &entries[i]
		}
	}
	if denied == nil || denied.Detail != "不想删" {
		t.Errorf("被拒记录应完整落盘: %+v", entries)
	}
}

// 追加而不是重写：审计的价值在于"发生过什么"，重写会把它变成"当前状态"。
func TestAudit_AppendsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	g1 := New("auto", nil, nil, nil, 0)
	g1.SetAuditPath(path)
	g1.Record(AuditEntry{Tool: "t1", Action: "approved"})

	// 模拟重启：新 Gate 实例，同一文件
	g2 := New("auto", nil, nil, nil, 0)
	g2.SetAuditPath(path)
	g2.Record(AuditEntry{Tool: "t2", Action: "denied"})

	entries, _ := LoadAuditLog(path, 0)
	if len(entries) != 2 {
		t.Fatalf("重启后应保留历史 + 新记录，实际 %d 条", len(entries))
	}
}

// 未配置落盘路径时不写文件（默认行为不变，测试环境不产生垃圾文件）。
func TestAudit_NoPathNoFile(t *testing.T) {
	g := New("auto", nil, nil, nil, 0)
	g.Record(AuditEntry{Tool: "t", Action: "auto"})
	if g.AuditPath() != "" {
		t.Errorf("未配置时路径应为空: %q", g.AuditPath())
	}
	if got := g.RecentAudit(10); len(got) != 1 {
		t.Errorf("内存环应照常工作: %v", got)
	}
}

// 读不存在的文件不报错（首次运行）。
func TestLoadAuditLog_MissingFile(t *testing.T) {
	entries, err := LoadAuditLog(filepath.Join(t.TempDir(), "nope.jsonl"), 10)
	if err != nil || len(entries) != 0 {
		t.Errorf("缺文件应返回空且不报错: %v %v", entries, err)
	}
}

// P4-2：等待审批中的步骤要落盘——进程若在此期间退出，重启后能列出卡住的任务。
func TestPending_MarkAndClear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending_approvals.json")
	g := New("auto", nil, nil, nil, 0)
	g.SetPendingPath(path)

	g.MarkPending(PendingApproval{TaskID: "t1", StepID: "s1", Tool: "shell.exec", Risk: "high", Reason: "高风险"})
	g.MarkPending(PendingApproval{TaskID: "t2", StepID: "s2", Tool: "file.delete", Risk: "high"})

	// 模拟重启：直接从文件读
	items, err := LoadPendingApprovals(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("重启后应列出 2 条等待审批，实际 %d", len(items))
	}
	if items[0].TaskID != "t1" || items[0].Tool != "shell.exec" {
		t.Errorf("记录内容不完整: %+v", items[0])
	}

	// 审批有结果 → 摘除
	g.ClearPending("t1", "s1")
	items, _ = LoadPendingApprovals(path)
	if len(items) != 1 || items[0].TaskID != "t2" {
		t.Errorf("清除后应只剩 t2: %+v", items)
	}
}

// 同一 (任务, 步骤) 重复登记只保留最新一条：重规划会重新走同一步。
func TestPending_ReMarkUpdatesNotDuplicates(t *testing.T) {
	dir := t.TempDir()
	g := New("auto", nil, nil, nil, 0)
	g.SetPendingPath(filepath.Join(dir, "p.json"))

	g.MarkPending(PendingApproval{TaskID: "t1", StepID: "s1", Reason: "第一次"})
	g.MarkPending(PendingApproval{TaskID: "t1", StepID: "s1", Reason: "第二次"})

	items := g.PendingApprovals()
	if len(items) != 1 {
		t.Fatalf("重复登记不该产生重复记录: %+v", items)
	}
	if items[0].Reason != "第二次" {
		t.Errorf("应保留最新一条: %+v", items[0])
	}
}

func TestPending_ClearFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	g := New("auto", nil, nil, nil, 0)
	g.SetPendingPath(path)
	g.MarkPending(PendingApproval{TaskID: "t1", StepID: "s1"})

	if err := ClearPendingFile(path); err != nil {
		t.Fatal(err)
	}
	items, _ := LoadPendingApprovals(path)
	if len(items) != 0 {
		t.Errorf("清理后应为空: %+v", items)
	}
	// 再次清理不报错（幂等）
	if err := ClearPendingFile(path); err != nil {
		t.Errorf("重复清理应幂等: %v", err)
	}
}

// 损坏文件不阻断：等待审批记录是辅助信息，读不出来就当没有。
func TestLoadPendingApprovals_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(path, []byte("{不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := LoadPendingApprovals(path)
	if err != nil {
		t.Errorf("损坏文件不该报错: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("损坏文件应视为空: %+v", items)
	}
}

// 未配置路径时不落盘（默认行为不变）。
func TestPending_NoPathNoFile(t *testing.T) {
	dir := t.TempDir()
	g := New("auto", nil, nil, nil, 0)
	g.MarkPending(PendingApproval{TaskID: "t1", StepID: "s1"})
	if got := g.PendingApprovals(); len(got) != 1 {
		t.Errorf("内存态应照常工作: %v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("未配置路径不该产生文件: %v", entries)
	}
}
