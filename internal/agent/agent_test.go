package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/config"
	"gleam/internal/harness/memory"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/harness/skill"
	"gleam/internal/llm"
	"gleam/internal/tools/file"
	"gleam/pkg/types"
)

// ---------- 测试装配 ----------

type agentFixture struct {
	a      *Agent
	notify *recordingNotifier
	ws     string
	llm    *llm.MockClient
	store  *skill.Store
}

func newFixture(t *testing.T, scripts []llm.Scripted) *agentFixture {
	t.Helper()
	ws := t.TempDir()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = ws
	cfg.DataDir = dataDir
	cfg.LLM.Provider = "mock"
	cfg.Agent.StepTimeoutSecs = 5
	agent_DataDir(cfg)

	m := llm.NewMock()
	m.Apply(scripts)
	mem, err := memory.Open(dataDir, 20, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := skill.Open(filepath.Join(dataDir, "skills"))
	gate := safety.New("auto", cfg.Safety.TrustedTools, nil, []string{ws}, 2*time.Second)

	r := newTestRegistry()
	file.New(ws).RegisterAll(r)

	notify := &recordingNotifier{}
	a := New(cfg, m, r, mem, gate, store, nil, notify)
	return &agentFixture{a: a, notify: notify, ws: ws, llm: m, store: store}
}

func agent_DataDir(cfg *config.Config) string {
	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "memory"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "skills"), 0o755)
	_ = os.MkdirAll(filepath.Join(cfg.DataDir, "tasks"), 0o755)
	return cfg.DataDir
}

var _ = registry.New // 引用保持

func planScript(jsonText string) llm.Scripted {
	return llm.Scripted{Kind: "plan", Texts: []string{jsonText}}
}

func reflectScript(score int, verdict, reason string) llm.Scripted {
	return llm.Scripted{Kind: "reflect", Texts: []string{
		`{"score":` + itoa(score) + `,"verdict":"` + verdict + `","reason":"` + reason + `"}`,
	}}
}

func TestRunGoal_ReferencesAndRoleReachPlanner(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"已读取上下文"}}]}`),
		reflectScript(92, "done", "上下文已处理"),
	})
	result := f.a.RunGoal(context.Background(), types.GoalRequest{
		Goal: "基于引用整理回复",
		Mode: "auto",
		Role: "analyst",
		References: []types.Reference{
			{Kind: "file", Label: "计划.md", Value: "@D:/workspace/计划.md"},
			{Kind: "memory", Label: "回复偏好", Value: "@memory:pref-1"},
		},
	})
	if result.Status != types.GoalSuccess {
		t.Fatalf("任务未成功: %+v", result)
	}
	if len(f.llm.Calls) == 0 {
		t.Fatal("规划器没有收到 LLM 请求")
	}
	request := f.llm.Calls[0]
	joined := request.System + "\n" + request.Messages[0].Content
	for _, want := range []string{"[file] 计划.md: @D:/workspace/计划.md", "[memory] 回复偏好: @memory:pref-1", "分析师"} {
		if !strings.Contains(joined, want) {
			t.Errorf("规划请求缺少 %q: %s", want, joined)
		}
	}
}

// TestPlanProgress_NoRawJSONLeak 规划流只报进度、不转发内容（2026-09-23 QA 报告 M5）：
// 规划响应本身就是 JSON，逐 token 转发会把 {"steps":... 碎片打进 CLI/WebUI 的进度行。
func TestPlanProgress_NoRawJSONLeak(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"你好"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "打个招呼", Mode: "auto"})
	f.notify.mu.Lock()
	defer f.notify.mu.Unlock()
	if len(f.notify.progress) == 0 {
		t.Fatal("没有任何进度事件")
	}
	for _, ev := range f.notify.progress {
		if ev.Phase == "plan" && (strings.Contains(ev.Message, `"steps"`) || strings.Contains(ev.Message, `{"`)) {
			t.Errorf("plan 进度泄漏原始 JSON：%q", ev.Message)
		}
	}
}

func TestValidateGoalRequest(t *testing.T) {
	if err := ValidateGoalRequest(types.GoalRequest{}); err != nil {
		t.Fatalf("空值应使用默认配置: %v", err)
	}
	for _, req := range []types.GoalRequest{
		{Mode: "unsafe"},
		{TaskMode: "batch"},
		{Role: "unknown"},
	} {
		if err := ValidateGoalRequest(req); err == nil {
			t.Errorf("请求应被拒绝: %+v", req)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// ---------- 场景 ----------

func TestRunGoal_HappyPathWithFileTools(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[
			{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"out/hello.txt","content":"你好 Gleam"}},
			{"id":"s2","description":"读回来","tool":"file.read","args":{"path":"out/hello.txt"},"depends_on":["s1"]},
			{"id":"s3","description":"回复","tool":"reply","args":{"text":"已创建 out/hello.txt 并验证可读"}}
		],"estimated_time":"short"}`),
		reflectScript(95, "done", "文件创建成功"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "创建问候文件"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("status = %s, err=%s, steps=%+v", res.Status, res.Error, res.Steps)
	}
	if res.Score != 95 {
		t.Errorf("score = %d", res.Score)
	}
	data, err := os.ReadFile(filepath.Join(f.ws, "out", "hello.txt"))
	if err != nil || string(data) != "你好 Gleam" {
		t.Fatalf("文件未正确写入: %v %q", err, data)
	}
	if res.Summary != "已创建 out/hello.txt 并验证可读" {
		t.Errorf("summary = %q", res.Summary)
	}
	if res.TaskID == "" {
		t.Error("TaskID 为空")
	}
	phases := map[string]bool{}
	for _, ev := range f.notify.progress {
		phases[ev.Phase] = true
	}
	if !phases["plan"] || !phases["execute"] || !phases["reflect"] {
		t.Errorf("阶段缺失: %v", phases)
	}
}

func TestRunGoal_ReplanOnLowScore(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"读","tool":"file.read","args":{"path":"missing.txt"}}]}`),
		reflectScript(30, "replan", "文件不存在，应先创建"),
		planScript(`{"steps":[
			{"id":"s1","description":"写","tool":"file.write","args":{"path":"fixed.txt","content":"ok"}},
			{"id":"s2","description":"读","tool":"file.read","args":{"path":"fixed.txt"},"depends_on":["s1"]}
		]}`),
		reflectScript(90, "done", "已修复"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "读取 fixed.txt"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("重规划后应成功: %s %s", res.Status, res.Error)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "fixed.txt")); err != nil {
		t.Error("重规划后的文件未创建")
	}
}

func TestRunGoal_PlanningValidationRetry(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","tool":"不存在的工具"}]}`),
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"重试成功"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "g"})
	if res.Status != types.GoalSuccess || res.Summary != "重试成功" {
		t.Fatalf("status=%s summary=%q err=%s", res.Status, res.Summary, res.Error)
	}
}

func TestRunGoal_ApprovalDenied(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"删除","tool":"file.delete","args":{"path":"target.txt","recursive":false}}]}`),
		reflectScript(10, "replan", "被拒绝"),
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"收到，已放弃删除"}}]}`),
		reflectScript(85, "done", "已放弃"),
	})
	if err := os.WriteFile(filepath.Join(f.ws, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "删除 target.txt"})
	if len(f.notify.approvals) == 0 {
		t.Fatal("高风险操作未触发审批")
	}
	if f.notify.approvals[0].Risk != "high" {
		t.Errorf("risk = %s", f.notify.approvals[0].Risk)
	}
	if res.Status != types.GoalSuccess {
		t.Fatalf("放弃后回复目标应完成: %s", res.Status)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "target.txt")); err != nil {
		t.Error("文件不应被删除")
	}
}

func TestRunGoal_PlanFirstWholePlanApproval(t *testing.T) {
	// 拒绝整计划
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"写","tool":"file.write","args":{"path":"pf.txt","content":"x"}}]}`),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "g", Mode: "plan_first"})
	if res.Status != types.GoalCancelled {
		t.Fatalf("拒绝计划应取消: %s", res.Status)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "pf.txt")); !os.IsNotExist(err) {
		t.Error("拒绝后不应写文件")
	}

	// 批准整计划
	f2 := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"写","tool":"file.write","args":{"path":"pf.txt","content":"x"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	f2.notify.approveAll = true
	res2 := f2.a.RunGoal(context.Background(), types.GoalRequest{Goal: "g", Mode: "plan_first"})
	if res2.Status != types.GoalSuccess {
		t.Fatalf("批准后应成功: %s %s", res2.Status, res2.Error)
	}
}

func TestRunGoal_SuggestSkillOnSuccess(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[
			{"id":"s1","description":"写1","tool":"file.write","args":{"path":"a1.txt","content":"1"}},
			{"id":"s2","description":"写2","tool":"file.write","args":{"path":"a2.txt","content":"2"},"depends_on":["s1"]}
		]}`),
		reflectScript(92, "done", "完成"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "连续写两个文件"})
	if res.Status != types.GoalSuccess {
		t.Fatalf("status = %s", res.Status)
	}
	if len(f.notify.skillDrafts) != 1 {
		t.Fatalf("应提出技能固化建议: %d", len(f.notify.skillDrafts))
	}
	draft := f.notify.skillDrafts[0]
	if len(draft.Steps) != 2 || draft.Name == "" {
		t.Errorf("draft = %+v", draft)
	}
}

func TestRunSkill_ExecuteAndStats(t *testing.T) {
	f := newFixture(t, nil)
	_, err := f.store.Save(skill.Skill{
		Name:        "make-notes",
		Description: "创建笔记",
		Params:      []string{"title"},
		Steps: []types.Step{
			{ID: "s1", Tool: "file.write", Args: map[string]any{"path": "notes/{{title}}.txt", "content": "笔记：{{title}}"}},
			{ID: "s2", Tool: "file.read", Args: map[string]any{"path": "notes/{{title}}.txt"}, DependsOn: []string{"s1"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.a.RunSkill(context.Background(), "make-notes", map[string]string{"title": "购物清单"})
	if err != nil {
		t.Fatal(err)
	}
	if out["status"] != string(types.GoalSuccess) {
		t.Errorf("skill run = %v", out)
	}
	data, err := os.ReadFile(filepath.Join(f.ws, "notes", "购物清单.txt"))
	if err != nil || string(data) != "笔记：购物清单" {
		t.Fatalf("技能产物错误: %v %q", err, data)
	}
	got, _ := f.store.Get("make-notes")
	if got.Runs != 1 || got.Successes != 1 {
		t.Errorf("统计 = %d/%d", got.Successes, got.Runs)
	}
	if _, err := f.a.RunSkill(context.Background(), "make-notes", nil); err == nil {
		t.Error("缺参数应报错")
	}
}

func TestRunGoal_CancelWhileRunning(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"慢工具","tool":"slow"}]}`),
	})
	f.a.Reg.Replace(&funcTool{name: "slow", perm: types.PermissionReadOnly, fn: func(ctx context.Context, args map[string]any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.a.CancelTask("fixed-task-id")
	}()
	res := f.a.RunGoal(context.Background(), types.GoalRequest{TaskID: "fixed-task-id", Goal: "慢任务"})
	if res.Status != types.GoalCancelled {
		t.Fatalf("应取消: %s (%s)", res.Status, res.Error)
	}
}

func TestRunGoal_MaxReplansExhausted(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"失败","tool":"fail"}]}`),
		reflectScript(20, "replan", "一直失败"),
		planScript(`{"steps":[{"id":"s1","description":"失败","tool":"fail"}]}`),
		reflectScript(20, "replan", "一直失败"),
		planScript(`{"steps":[{"id":"s1","description":"失败","tool":"fail"}]}`),
		reflectScript(20, "replan", "一直失败"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "不可能任务"})
	if res.Status == types.GoalSuccess {
		t.Fatal("不应成功")
	}
	if res.Status != types.GoalFailed && res.Status != types.GoalPartial {
		t.Errorf("status = %s", res.Status)
	}
}

func TestRunGoal_MemoryContextInjected(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好的"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	if _, err := f.a.Mem.Remember("用户名是小明，喜欢被称呼小明同学", []string{"偏好"}); err != nil {
		t.Fatal(err)
	}
	f.a.RunGoal(context.Background(), types.GoalRequest{Goal: "用户名是小明相关的偏好是什么"})
	found := false
	for _, call := range f.llm.Calls {
		if strings.Contains(call.System, "小明") && strings.Contains(call.System, llm.MarkerPlan) {
			found = true
		}
	}
	if !found {
		t.Error("规划提示词应注入长期记忆")
	}
	if f.a.Mem.Short.Len() < 2 {
		t.Errorf("短期记忆 = %d", f.a.Mem.Short.Len())
	}
}

func TestRunGoal_ReusedTaskIDWhileRunning(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"好"}}]}`),
		reflectScript(90, "done", "ok"),
	})
	// 同 ID 复用：第二个调用应直接引用第一个的结果
	goal := types.GoalRequest{TaskID: "same-id", Goal: "g1"}
	res1 := f.a.RunGoal(context.Background(), goal)
	res2 := f.a.RunGoal(context.Background(), goal)
	if res1.TaskID != "same-id" || res2.TaskID != "same-id" {
		t.Error("TaskID 应保留")
	}
}
