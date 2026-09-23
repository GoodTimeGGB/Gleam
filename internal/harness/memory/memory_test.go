package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShortTerm_RingBuffer(t *testing.T) {
	s := NewShortTerm(3)
	for i := 0; i < 5; i++ {
		s.Add("user", string(rune('a'+i)))
	}
	if s.Len() != 3 {
		t.Errorf("Len = %d", s.Len())
	}
	got := s.Recent(0)
	if len(got) != 3 || got[0].Content != "c" || got[2].Content != "e" {
		t.Errorf("环缓冲顺序错误: %v", got)
	}
	got = s.Recent(2)
	if len(got) != 2 || got[0].Content != "d" {
		t.Errorf("Recent(2) = %v", got)
	}
}

func TestTokenize(t *testing.T) {
	toks := tokenize("Hello, 世界 Go语言!")
	joined := ""
	for _, tk := range toks {
		joined += tk + "|"
	}
	// 拉丁词 + 中文双字组
	want := "hello|世界|go|语言|"
	if joined != want {
		t.Errorf("tokenize = %q, want %q", joined, want)
	}
}

func TestEmbedNormalized(t *testing.T) {
	v := embed("本地优先的桌面智能体", 256)
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("未归一化: %f", sum)
	}
	zero := embed("...", 256) // 无词元（..均为分隔符）→ 零向量
	allZero := true
	for _, x := range zero {
		if x != 0 {
			allZero = false
		}
	}
	if !allZero {
		t.Error("空词元应为零向量")
	}
}

func TestSearchHitsTop_Relevance(t *testing.T) {
	items := []Item{
		{ID: "1", Content: "用户喜欢简洁的回复风格", Vec: embed("用户喜欢简洁的回复风格", 256)},
		{ID: "2", Content: "项目采用 Go 语言开发", Vec: embed("项目采用 Go 语言开发", 256)},
		{ID: "3", Content: "用户的办公目录是 D:/work", Vec: embed("用户的办公目录是 D:/work", 256)},
	}
	hits := SearchHitsTop(items, "用户的回复偏好", 2, 256)
	if len(hits) != 2 {
		t.Fatalf("hits = %d", len(hits))
	}
	if hits[0].ID != "1" {
		t.Errorf("top1 = %s（期望偏好条目）", hits[0].ID)
	}
	if hits[0].Score <= hits[1].Score {
		t.Error("分数应降序")
	}
}

func TestStore_PersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "longterm.json")
	s, err := OpenStore(path, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := s.Remember("记录一：用户偏好深色主题", []string{"偏好"})
	id2, _ := s.Remember("记录二：项目在 D:/Gleam", nil)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Errorf("Count = %d", s.Count())
	}

	s2, err := OpenStore(path, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 2 {
		t.Fatalf("重载后 Count = %d", s2.Count())
	}
	hits := s2.Search("深色主题", 1)
	if len(hits) != 1 || hits[0].ID != id1 {
		t.Errorf("重载后检索错误: %v", hits)
	}
	if !s2.Delete(id2) {
		t.Error("删除失败")
	}
	if s2.Delete(id2) {
		t.Error("重复删除应返回 false")
	}
	if s2.Count() != 1 {
		t.Errorf("删除后 Count = %d", s2.Count())
	}
	_ = id2
}

func TestStore_CapacityEviction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "longterm.json")
	s, _ := OpenStore(path, 64, 3)
	_, _ = s.Remember("第一条 oldest", nil)
	time.Sleep(2 * time.Millisecond)
	_, _ = s.Remember("第二条", nil)
	time.Sleep(2 * time.Millisecond)
	_, _ = s.Remember("第三条", nil)
	_, _ = s.Remember("第四条 newest", nil)
	if s.Count() != 3 {
		t.Errorf("容量淘汰后 Count = %d", s.Count())
	}
	hits := s.Search("oldest", 1)
	if len(hits) > 0 && hits[0].Score > 0.5 {
		t.Errorf("最旧条目应被淘汰: %v", hits)
	}
}

func TestStore_CorruptFileRecovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "longterm.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path, 64, 10)
	if err != nil {
		t.Fatalf("损坏文件不应阻断启动: %v", err)
	}
	if s.Count() != 0 {
		t.Error("损坏库应为空")
	}
}

func TestManager_Facade(t *testing.T) {
	m, err := Open(t.TempDir(), 5, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	m.AddTurn("user", "你好")
	m.AddTurn("assistant", "你好，需要什么帮助？")
	if m.Short.Len() != 2 {
		t.Errorf("短期记忆 = %d", m.Short.Len())
	}
	if _, err := m.Remember("测试记忆条目", []string{"test"}); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if len(m.Relevant("测试记忆", 3)) == 0 {
		t.Error("Relevant 应有命中")
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
