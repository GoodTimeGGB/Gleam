package geo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gleam/internal/llm"
)

func TestAnalyzeMock(t *testing.T) {
	az := NewAnalyzer(llm.NewMock())
	s, err := az.Analyze(context.Background(), "人工智能正在改变内容生产方式。本文给出三个可落地的判断。")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if s.Score <= 0 || s.Score > 100 {
		t.Fatalf("分数越界: %d", s.Score)
	}
	if len(s.Actionables) == 0 {
		t.Fatal("期望至少一条可操作建议")
	}
	if Format(s) == "" {
		t.Fatal("格式化输出不应为空")
	}
}

func TestAnalyzeRejectsEmptyOrNil(t *testing.T) {
	az := NewAnalyzer(llm.NewMock())
	if _, err := az.Analyze(context.Background(), "   "); err == nil {
		t.Fatal("空内容应报错")
	}
	var nilAz *Analyzer
	if _, err := nilAz.Analyze(context.Background(), "正文"); err == nil {
		t.Fatal("nil 分析器应报错")
	}
}

func TestParseToleratesCodeFence(t *testing.T) {
	raw := "好的：\n```json\n{\"score\":42,\"summary\":\"结构一般\",\"actionables\":[{\"category\":\"结构\",\"description\":\"加小标题\",\"priority\":\"urgent\"}]}\n```"
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Score != 42 {
		t.Fatalf("分数解析错误: %d", s.Score)
	}
	// 非法优先级应收敛为 medium，缺省类别应补默认值
	if s.Actionables[0].Priority != "medium" {
		t.Fatalf("优先级未归一化: %s", s.Actionables[0].Priority)
	}
	if s.Actionables[0].Category != "结构" {
		t.Fatalf("类别被覆盖: %s", s.Actionables[0].Category)
	}
}

func TestStorePersistAndTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geo_history.json")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < maxRecords+10; i++ {
		st.Add(Record{Score: i % 100, Summary: "x"})
	}
	if got := len(st.List(0)); got != maxRecords {
		t.Fatalf("历史应裁剪到 %d 条，实际 %d", maxRecords, got)
	}
	// 重新打开应读回持久化内容
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("重新打开: %v", err)
	}
	if got := len(reopened.List(0)); got != maxRecords {
		t.Fatalf("持久化后条数错误: %d", got)
	}
	// 最新在前
	if reopened.List(1)[0].Score != (maxRecords+9)%100 {
		t.Fatalf("排序应为最新在前: %+v", reopened.List(1)[0])
	}
	reopened.Clear()
	if got := len(reopened.List(0)); got != 0 {
		t.Fatalf("清空失败: %d", got)
	}
}

func TestStoreSurvivesCorruptedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "geo_history.json")
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("损坏文件不应导致装配失败: %v", err)
	}
	st.Add(Record{Score: 1})
	if got := len(st.List(0)); got != 1 {
		t.Fatalf("重建后应可正常写入: %d", got)
	}
}

func TestStoreStats(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "geo.json"))
	if total, _, _ := st.Stats(); total != 0 {
		t.Fatalf("空存储 total 应为 0: %d", total)
	}
	st.Add(Record{Score: 60})
	st.Add(Record{Score: 90})
	total, avg, best := st.Stats()
	if total != 2 || avg != 75 || best != 90 {
		t.Fatalf("统计错误: total=%d avg=%v best=%d", total, avg, best)
	}
}

func TestIsCreative(t *testing.T) {
	cases := []struct {
		goal string
		role string
		want bool
	}{
		{"整理一下", "writer", true},               // 创作者角色：始终创作
		{"写一篇关于远程办公的文章", "", true},             // 强信号
		{"帮我润色这段产品介绍", "general", true},        // 弱信号
		{"把代码里的 bug 修一下", "coder", false},      // 工程任务不是创作
		{"给这个项目写单元测试", "coder", false},         // 工程角色 + 无强信号
		{"给这个项目写个 README 介绍文案", "coder", true}, // 工程角色但命中强信号
		{"把这份报告的数据整理成 Excel 表格", "", false},    // 反向信号压过弱信号
		{"", "", false}, // 空目标
	}
	for _, c := range cases {
		if got := IsCreative(c.goal, c.role); got != c.want {
			t.Errorf("IsCreative(%q, %q) = %v, want %v", c.goal, c.role, got, c.want)
		}
	}
}
