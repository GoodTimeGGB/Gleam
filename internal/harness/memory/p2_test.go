package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- P2-1 钉住区 ----------

// 钉住区的存在意义：压缩可以丢过程，不能丢用户定下的约束。
func TestManager_PinSurvivesCompress(t *testing.T) {
	m, _ := Open(t.TempDir(), 2, 64, 100)
	m.Pin("输出必须是 Markdown 表格")
	m.Pin("永远不要动 /etc 目录")
	m.AddTurn("user", "第一轮")
	m.AddTurn("assistant", "第二轮")
	m.AddTurn("user", "触发溢出的第三轮")
	if !m.Compress(nil) {
		t.Fatal("应发生压缩")
	}
	pins := m.Pinned()
	if len(pins) != 2 {
		t.Fatalf("钉住区 = %v，压缩不该动它", pins)
	}
	// 顺序是语义：按加入顺序原样返回，绝不重排
	if pins[0] != "输出必须是 Markdown 表格" || pins[1] != "永远不要动 /etc 目录" {
		t.Errorf("钉住区顺序被重排: %v", pins)
	}
}

func TestManager_PinDedupAndCap(t *testing.T) {
	m, _ := Open(t.TempDir(), 2, 64, 100)
	m.Pin("同一条")
	m.Pin("同一条")
	m.Pin("  同一条  ") // 首尾空白不构成新条目
	if got := len(m.Pinned()); got != 1 {
		t.Fatalf("重复 Pin 后钉住区 = %d 条，应为 1", got)
	}
	for i := 0; i < pinnedCap+5; i++ {
		m.Pin(string(rune('a'+i%26)) + " 约束 " + string(rune('0'+i%10)))
	}
	if got := len(m.Pinned()); got != pinnedCap {
		t.Fatalf("钉住区 = %d 条，应封顶 %d", got, pinnedCap)
	}
}

// 约束信号词启发式：用户说"以后都……"时该轮自动进不可压缩区。
func TestManager_AddTurn_PinHint(t *testing.T) {
	m, _ := Open(t.TempDir(), 4, 64, 100)
	m.AddTurn("user", "以后所有回复都用中文标题")
	m.AddTurn("assistant", "好的")
	m.AddTurn("user", "今天天气怎么样") // 无约束词，不该钉
	if got := m.Pinned(); len(got) != 1 || !strings.Contains(got[0], "以后") {
		t.Fatalf("钉住区 = %v，应只含约束轮", got)
	}
}

func TestManager_SummaryAnnotated_Omission(t *testing.T) {
	m, _ := Open(t.TempDir(), 2, 64, 100)
	if s := m.SummaryAnnotated(); s != "" {
		t.Fatalf("空摘要不该有标注: %q", s)
	}
	// 容量 2 的环缓冲：5 轮对话会产生 3 轮溢出（被覆盖进压缩管线）
	for i := 0; i < 5; i++ {
		m.AddTurn("user", fmt.Sprintf("第 %d 轮的对话内容", i+1))
	}
	m.Compress(nil)
	turns, runes := m.Omission()
	if turns != 3 {
		t.Errorf("省略轮数 = %d，应为 3", turns)
	}
	if runes == 0 {
		t.Error("省略字数不应为 0")
	}
	s := m.SummaryAnnotated()
	if !strings.Contains(s, "省略 3 轮") || !strings.Contains(s, "约 ") {
		t.Errorf("摘要标注缺省略量: %q", s)
	}
}

// 钉住区与省略量跨进程存活：压缩状态文件是它们唯一的家。
func TestManager_PinAndOmissionPersisted(t *testing.T) {
	dir := t.TempDir()
	m, _ := Open(dir, 2, 64, 100)
	m.Pin("持久化的约束")
	for i := 0; i < 5; i++ {
		m.AddTurn("user", fmt.Sprintf("第 %d 轮的对话内容", i+1))
	}
	m.Compress(nil)

	m2, err := Open(dir, 2, 64, 100)
	if err != nil {
		t.Fatal(err)
	}
	pins := m2.Pinned()
	if len(pins) != 1 || pins[0] != "持久化的约束" {
		t.Fatalf("重开后钉住区 = %v", pins)
	}
	if turns, _ := m2.Omission(); turns != 3 {
		t.Errorf("重开后省略轮数 = %d，应为 3", turns)
	}
}

// 向后兼容：旧版 context.json 没有 pinned/omitted 字段，读取必须成功。
func TestManager_OldContextFileBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	old := map[string]any{
		"overflow":     []any{},
		"summary":      "旧版摘要",
		"saved_tokens": 42,
	}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "memory", contextFile), b, 0o644); err != nil {
		_ = os.MkdirAll(filepath.Join(dir, "memory"), 0o755)
		if err := os.WriteFile(filepath.Join(dir, "memory", contextFile), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Open(dir, 2, 64, 100)
	if err != nil {
		t.Fatalf("旧状态文件应能读取: %v", err)
	}
	if !strings.Contains(m.Summary(), "旧版摘要") {
		t.Errorf("摘要 = %q", m.Summary())
	}
}

// ---------- P2-3 记忆去重 / 软删 / 阈值 ----------

// 去重的动机：同一目标跑 10 次不该产生 10 条互相挤占的任务记录。
func TestStore_RememberDedup(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "lt.json"), 64, 100)
	id1, err := s.Remember("任务记录[success] 目标：整理下载目录的文件 结果：完成（完成度 95）", []string{"task"})
	if err != nil {
		t.Fatal(err)
	}
	// 第二次是近似重复（微调措辞），应命中去重并更新原条目
	id2, err := s.Remember("任务记录[success] 目标：整理下载目录的文件 结果：完成（完成度 96）", []string{"task"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("近似重复应更新原条目，却新增了 %s → %s", id1, id2)
	}
	if got := s.Count(); got != 1 {
		t.Errorf("去重后条目数 = %d，应为 1", got)
	}
}

func TestStore_RememberDistinctNotDeduped(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "lt.json"), 64, 100)
	if _, err := s.Remember("用户偏好：回复用中文", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember("部署脚本位于 /srv/deploy 目录", nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Count(); got != 2 {
		t.Errorf("不同内容不应去重，条目数 = %d", got)
	}
}

// 软删除：检索隐身，但条目还在——误删可挽回，痕迹可追溯。
func TestStore_SoftDelete_HiddenFromSearch(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "lt.json"), 64, 100)
	id, _ := s.Remember("项目部署在华东区的 Kubernetes 集群", nil)
	if _, err := s.Remember("用户偏好：回复用中文", nil); err != nil {
		t.Fatal(err)
	}
	if !s.SoftDelete(id) {
		t.Fatal("首次软删应成功")
	}
	if s.SoftDelete(id) {
		t.Error("重复软删应返回 false")
	}
	hits := s.Search("Kubernetes 集群部署", 5)
	for _, h := range hits {
		if h.ID == id {
			t.Error("软删条目不应出现在检索结果")
		}
	}
	if got := s.Count(); got != 2 {
		t.Errorf("软删不物理移除，条目数 = %d，应为 2", got)
	}
	if got := s.DeletedCount(); got != 1 {
		t.Errorf("软删计数 = %d，应为 1", got)
	}
}

// 检索阈值：完全无关的 query 不该带出长尾噪音。
func TestStore_SearchMinScore(t *testing.T) {
	s, _ := OpenStore(filepath.Join(t.TempDir(), "lt.json"), 64, 100)
	if _, err := s.Remember("整理下载目录里的发票扫描件", nil); err != nil {
		t.Fatal(err)
	}
	hits := s.Search("量子色动力学中的渐近自由", 5)
	if len(hits) != 0 {
		t.Errorf("无关检索不该命中: %+v", hits)
	}
	hits = s.Search("下载目录 发票", 5)
	if len(hits) != 1 {
		t.Errorf("相关检索应命中 1 条，实际 %d", len(hits))
	}
}

// 旧格式记忆文件（无 source/deleted 字段）读取必须成功。
func TestStore_OldFileBackwardCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lt.json")
	content := "旧版记忆条目"
	old := []map[string]any{
		{
			"id": "old-1", "content": content,
			"created_at": "2025-01-01T00:00:00Z",
			"vec":        embed(content, 64), // 真实旧文件带向量；全零向量与任何内容余弦为 0
		},
	}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path, 64, 100)
	if err != nil {
		t.Fatalf("旧文件应能读取: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("条目数 = %d", s.Count())
	}
	// 读出来之后新能力照常可用（去重、软删）
	id2, err := s.Remember("旧版记忆条目", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id2 != "old-1" {
		t.Errorf("旧条目应参与去重，实际新增 %s", id2)
	}
}
