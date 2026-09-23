package conversation

import (
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestCreateAppendList(t *testing.T) {
	s := newStore(t)
	c, err := s.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" || c.Title != "新对话" {
		t.Fatalf("新会话默认值异常: %+v", c)
	}
	if _, err := s.Append(c.ID, Message{Role: "user", Content: "帮我整理下载目录的文件", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Append user: %v", err)
	}
	if _, err := s.Append(c.ID, Message{Role: "assistant", Content: "好的，已整理完成", Status: "success", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("Append assistant: %v", err)
	}
	got, err := s.Get(c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("消息数=%d, 期望 2", len(got.Messages))
	}
	if got.Title != "帮我整理下载目录的文件" {
		t.Fatalf("首条用户消息未生成标题: %q", got.Title)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List: n=%d err=%v", len(list), err)
	}
	if list[0].Preview == "" || list[0].Count != 2 {
		t.Fatalf("摘要异常: %+v", list[0])
	}
}

func TestTitleTruncate(t *testing.T) {
	s := newStore(t)
	c, _ := s.Create()
	long := "这是一个非常非常非常非常非常非常非常非常非常非常非常非常长的第一句话用于测试截断"
	if _, err := s.Append(c.ID, Message{Role: "user", Content: long, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(c.ID)
	if r := []rune(got.Title); len(r) != maxTitleRunes+1 { // 24 + 省略号
		t.Fatalf("标题长度=%d, 期望 %d（含省略号）", len(r), maxTitleRunes+1)
	}
}

func TestRenameDelete(t *testing.T) {
	s := newStore(t)
	c, _ := s.Create()
	renamed, err := s.Rename(c.ID, "  我的周报任务\n第二行 ")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.Title != "我的周报任务 第二行" {
		t.Fatalf("重命名清洗异常: %q", renamed.Title)
	}
	if err := s.Delete(c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(c.ID); err == nil {
		t.Fatal("删除后仍可读取")
	}
	// 重复删除不报错
	if err := s.Delete(c.ID); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
}

func TestListOrdering(t *testing.T) {
	s := newStore(t)
	base := time.Now()
	for i := 0; i < 3; i++ {
		c, _ := s.Create()
		_, _ = s.Append(c.ID, Message{Role: "user", Content: "对话" + string(rune('A'+i)), CreatedAt: base.Add(time.Duration(i) * time.Minute)})
	}
	list, err := s.List()
	if err != nil || len(list) != 3 {
		t.Fatalf("List n=%d err=%v", len(list), err)
	}
	if list[0].Title != "对话C" {
		t.Fatalf("列表未按最近更新倒序: 首项=%q", list[0].Title)
	}
}

func TestRejectTraversalID(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get(filepath.FromSlash("../x")); err == nil {
		t.Fatal("期望拒绝目录穿越 ID")
	}
	if err := s.Delete("ab/cd"); err == nil {
		t.Fatal("期望拒绝带分隔符 ID")
	}
}
