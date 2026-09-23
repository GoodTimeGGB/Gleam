package conversation

import (
	"testing"
	"time"
)

// 重试检测的判定面：宁可把"像的"算进来（误报成本低），
// 不能漏掉真实重试（漏了，重试率这个最接近真相的信号就哑了）。
func TestSimilarGoal(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"完全相同", "整理下载目录的文件", "整理下载目录的文件", true},
		{"微调措辞（真实重试的典型形态）", "整理下载目录的文件", "帮我整理一下下载目录的文件", true},
		{"只加标点", "整理下载目录", "整理，下载目录。", true},
		{"不同任务", "整理下载目录的文件", "给 utils.go 补单元测试", false},
		{"空串", "", "整理下载目录", false},
	}
	for _, tc := range cases {
		if got := SimilarGoal(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: SimilarGoal(%q, %q) = %v，应 %v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDetectRetry(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Create()

	// 没有任何历史：谈不上重试
	if s.DetectRetry(c.ID, "整理下载目录的文件") {
		t.Fatal("空会话不应判定为重试")
	}

	// 十分钟前的相似目标：在窗内，算重试
	if _, err := s.Append(c.ID, Message{Role: "user", Content: "整理下载目录的文件", CreatedAt: time.Now().Add(-2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if !s.DetectRetry(c.ID, "帮我整理一下下载目录的文件") {
		t.Error("窗内相似目标应判定为重试")
	}

	// 窗外（11 分钟前）：更可能是新一轮任务
	s2, _ := Open(t.TempDir())
	c2, _ := s2.Create()
	old := time.Now().Add(-11 * time.Minute)
	if _, err := s2.Append(c2.ID, Message{Role: "user", Content: "整理下载目录的文件", CreatedAt: old}); err != nil {
		t.Fatal(err)
	}
	if s2.DetectRetry(c2.ID, "帮我整理一下下载目录的文件") {
		t.Error("窗外相似目标不应判定为重试")
	}

	// 窗内但目标不同：不是重试
	if s.DetectRetry(c.ID, "给 utils.go 补单元测试") {
		t.Error("窗内不同目标不应判定为重试")
	}

	// 助手消息夹在中间不影响：检测的是最近一条 user 消息
	if _, err := s.Append(c.ID, Message{Role: "assistant", Content: "已整理完成"}); err != nil {
		t.Fatal(err)
	}
	if !s.DetectRetry(c.ID, "整理下载目录的文件") {
		t.Error("助手消息之后的相似提交仍应判定为重试")
	}
}
