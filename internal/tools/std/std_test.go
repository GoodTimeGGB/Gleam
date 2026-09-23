package std

import (
	"context"
	"strings"
	"testing"
)

// ---------- fake 适配器 ----------

type fakeMem struct{ items []SearchHit }

func (f *fakeMem) Remember(content string, tags []string) (string, error) {
	id := "mem-" + string(rune('a'+len(f.items)))
	f.items = append(f.items, SearchHit{ID: id, Content: content, Tags: tags, Score: 0.9})
	return id, nil
}
func (f *fakeMem) Search(query string, k int) []SearchHit { return f.items }
func (f *fakeMem) Count() int                             { return len(f.items) }

func TestReplyTool(t *testing.T) {
	tool := NewReply()
	out, err := tool.Execute(context.Background(), map[string]any{"text": "回答"})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["text"] != "回答" {
		t.Errorf("out = %v", out)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Error("缺 text 应报错")
	}
}

func TestMemoryTools(t *testing.T) {
	store := &fakeMem{}
	save := NewMemSave(store)
	out, err := save.Execute(context.Background(), map[string]any{
		"content": "用户偏好简洁", "tags": []any{"偏好", "ui"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["saved"] != true {
		t.Errorf("save = %v", out)
	}
	search := NewMemSearch(store)
	res, err := search.Execute(context.Background(), map[string]any{"query": "偏好", "k": 3})
	if err != nil {
		t.Fatal(err)
	}
	hits := res.(map[string]any)["hits"].([]SearchHit)
	if len(hits) != 1 || hits[0].Content != "用户偏好简洁" {
		t.Errorf("hits = %v", hits)
	}
	if _, err := save.Execute(context.Background(), map[string]any{}); err == nil {
		t.Error("缺 content 应报错")
	}
}

type fakeSched struct {
	jobs []JobDef
	err  error
}

func (f *fakeSched) AddJob(name, cron string, intervalSec int, goal, mode string) (JobDef, error) {
	if f.err != nil {
		return JobDef{}, f.err
	}
	j := JobDef{Name: name, Cron: cron, IntervalSec: intervalSec, Goal: goal, Mode: mode, Enabled: true}
	f.jobs = append(f.jobs, j)
	return j, nil
}
func (f *fakeSched) DeleteJob(name string) (bool, error) {
	for i, j := range f.jobs {
		if j.Name == name {
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeSched) ListJobs() []JobDef { return f.jobs }

func TestScheduleTools(t *testing.T) {
	mgr := &fakeSched{}
	create := NewScheduleCreate(mgr)
	out, err := create.Execute(context.Background(), map[string]any{
		"name": "daily", "goal": "整理", "cron": "0 9 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.(JobDef).Name != "daily" {
		t.Errorf("out = %v", out)
	}
	// 自然语言 when：工具内部解析为 cron，fakeSched 走兼容的 AddJob。
	outNL, err := create.Execute(context.Background(), map[string]any{
		"name": "nl", "goal": "整理下载目录", "when": "工作日18:30",
	})
	if err != nil {
		t.Fatalf("自然语言创建失败: %v", err)
	}
	if got := outNL.(JobDef).Cron; got != "30 18 * * 1-5" {
		t.Errorf("自然语言 cron = %q, 期望 30 18 * * 1-5", got)
	}
	if got := outNL.(JobDef).ScheduleText; got == "" {
		t.Error("自然语言创建应返回人读调度说明")
	}
	if _, err := create.Execute(context.Background(), map[string]any{"name": "x"}); err == nil {
		t.Error("缺 goal 应报错")
	}
	// 没有任何触发方式应报错。
	if _, err := create.Execute(context.Background(), map[string]any{"name": "y", "goal": "g"}); err == nil {
		t.Error("缺少 when/cron/interval 应报错")
	}
	list := NewScheduleList(mgr)
	res, _ := list.Execute(context.Background(), map[string]any{})
	if res.(map[string]any)["count"] != 2 {
		t.Error("列表数量")
	}
	del := NewScheduleDelete(mgr)
	if _, err := del.Execute(context.Background(), map[string]any{"name": "nope"}); err == nil {
		t.Error("删除不存在应报错")
	}
	if _, err := del.Execute(context.Background(), map[string]any{"name": "daily"}); err != nil {
		t.Fatal(err)
	}
}

type fakeSkillStore struct {
	summaries []SkillSummary
	params    map[string][]string
}

func (f *fakeSkillStore) ListSummaries() []SkillSummary { return f.summaries }
func (f *fakeSkillStore) GetParams(name string) ([]string, error) {
	if p, ok := f.params[name]; ok {
		return p, nil
	}
	return nil, &notFoundError{}
}

type notFoundError struct{}

func (*notFoundError) Error() string { return "not found" }

type fakeRunner struct{ ran string }

func (f *fakeRunner) RunSkill(ctx context.Context, name string, params map[string]string) (map[string]any, error) {
	// 与生产实现一致：不存在的技能返回错误
	if _, ok := map[string]bool{"demo": true}[name]; !ok {
		return nil, &notFoundError{}
	}
	f.ran = name + ":" + strings.Join(keysOf(params), ",")
	return map[string]any{"status": "success"}, nil
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSkillTools(t *testing.T) {
	store := &fakeSkillStore{
		params: map[string][]string{"demo": {"dir"}},
	}
	runner := &fakeRunner{}
	list := NewSkillList(store)
	res, _ := list.Execute(context.Background(), map[string]any{})
	if res.(map[string]any)["count"] != 0 {
		t.Error("初始列表应为空")
	}

	run := NewSkillRun(store, runner)
	if _, err := run.Execute(context.Background(), map[string]any{"name": "demo"}); err == nil {
		t.Error("缺参数应报错")
	}
	out, err := run.Execute(context.Background(), map[string]any{
		"name": "demo", "params": map[string]any{"dir": "/tmp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["status"] != "success" {
		t.Errorf("out = %v", out)
	}
	if !strings.Contains(runner.ran, "demo") || !strings.Contains(runner.ran, "dir") {
		t.Errorf("runner 调用 = %q", runner.ran)
	}
	// 未声明参数的技能
	if _, err := run.Execute(context.Background(), map[string]any{"name": "ghost"}); err == nil {
		t.Error("运行不存在的技能应报错")
	}
}
