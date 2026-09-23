package memory

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestManager_OverflowCapturedWhenRingFull(t *testing.T) {
	m, err := Open(t.TempDir(), 3, 64, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		m.AddTurn("user", fmt.Sprintf("早期对话 %d", i))
	}
	if got := m.Short.Len(); got != 3 {
		t.Fatalf("短期记忆 = %d，应为容量 3", got)
	}
	if len(m.overflow) != 2 {
		t.Fatalf("溢出区 = %d，应为 2", len(m.overflow))
	}
	if m.overflow[0].Content != "早期对话 0" || m.overflow[1].Content != "早期对话 1" {
		t.Errorf("溢出顺序错误: %+v", m.overflow)
	}
}

func TestManager_CompressUsesLLMSummary(t *testing.T) {
	m, _ := Open(t.TempDir(), 2, 64, 100)
	m.AddTurn("user", "第一轮")
	m.AddTurn("assistant", "第二轮")
	m.AddTurn("user", "触发溢出的第三轮")

	compressed := m.Compress(func(overflow string) (string, error) {
		if !strings.Contains(overflow, "第一轮") {
			t.Errorf("溢出内容缺失: %q", overflow)
		}
		return "用户讨论了第一轮内容（LLM 摘要）", nil
	})
	if !compressed {
		t.Fatal("应发生压缩")
	}
	if !strings.Contains(m.Summary(), "LLM 摘要") {
		t.Errorf("摘要 = %q", m.Summary())
	}
	// 溢出区已清空：再次压缩返回 false
	if m.Compress(nil) {
		t.Error("空溢出区不应再次压缩")
	}
}

func TestManager_CompressFallsBackToExtractive(t *testing.T) {
	m, _ := Open(t.TempDir(), 2, 64, 100)
	m.AddTurn("user", "请记住项目代号是 Gleam，使用 Go 语言自研")
	m.AddTurn("assistant", "好的")
	m.AddTurn("user", "另外我喜欢中文回复")

	ok := m.Compress(func(string) (string, error) { return "", errors.New("LLM 不可用") })
	if !ok {
		t.Fatal("LLM 失败时也应完成压缩")
	}
	if s := m.Summary(); !strings.Contains(s, "Gleam") {
		t.Errorf("抽取式摘要缺失关键内容: %q", s)
	}
}

func TestManager_CompressNilSummarizer(t *testing.T) {
	m, _ := Open(t.TempDir(), 2, 64, 100)
	m.AddTurn("user", "甲")
	m.AddTurn("assistant", "乙")
	m.AddTurn("user", "丙")
	if !m.Compress(nil) {
		t.Fatal("nil 摘要器应走本地兜底")
	}
	if strings.TrimSpace(m.Summary()) == "" {
		t.Error("摘要不应为空")
	}
}

func TestManager_CompressStatePersistedAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	m, _ := Open(dir, 2, 64, 100)
	m.AddTurn("user", "早期内容")
	m.AddTurn("assistant", "x")
	m.AddTurn("user", "溢出内容")
	m.Compress(func(string) (string, error) { return "持久化摘要内容", nil })

	m2, err := Open(dir, 2, 64, 100)
	if err != nil {
		t.Fatal(err)
	}
	if s := m2.Summary(); !strings.Contains(s, "持久化摘要内容") {
		t.Errorf("重开后摘要丢失: %q", s)
	}
	if len(m2.overflow) != 0 {
		t.Errorf("重开后溢出区应为空: %+v", m2.overflow)
	}
}

func TestShortTerm_Oldest(t *testing.T) {
	s := NewShortTerm(3)
	if _, ok := s.Oldest(); ok {
		t.Error("空缓冲不应有最旧轮")
	}
	s.Add("user", "a")
	s.Add("user", "b")
	s.Add("user", "c") // 满
	s.Add("user", "d") // 覆盖 a
	if old, ok := s.Oldest(); !ok || old.Content != "b" {
		t.Errorf("最旧 = %+v ok=%v，应为 b", old, ok)
	}
}

func TestManager_StatsAndClearSummary(t *testing.T) {
	m, _ := Open(t.TempDir(), 3, 64, 100)
	for i := 0; i < 5; i++ {
		m.AddTurn("user", "历史对话内容")
	}
	st := m.Stats()
	if st.ShortTurns != 3 || st.ShortCap != 3 || st.Overflow != 2 {
		t.Fatalf("stats = %+v", st)
	}
	m.Compress(func(string) (string, error) { return "统计测试摘要", nil })

	st2 := m.Stats()
	if st2.Summary == "" || st2.Overflow != 0 {
		t.Fatalf("压缩后 stats = %+v", st2)
	}

	m.ClearSummary()
	st3 := m.Stats()
	if st3.Summary != "" || st3.Overflow != 0 {
		t.Errorf("清空后 stats = %+v", st3)
	}
	// 清空后重启不恢复旧摘要
	m2, _ := Open(m.dir, 3, 64, 100)
	if s := m2.Summary(); s != "" {
		t.Errorf("清空后重开仍有摘要: %q", s)
	}
}
