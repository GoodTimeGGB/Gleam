package webui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/config"
	"gleam/internal/harness/conversation"
	"gleam/internal/harness/memory"
	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/harness/scheduler"
	"gleam/internal/harness/skill"
	"gleam/internal/llm"
	"gleam/internal/tools/file"
	"gleam/internal/tools/std"
	"gleam/pkg/types"
)

func TestReadJSONOversizedBodyReturnsError(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(bytes.Repeat([]byte{'x'}, 2<<20+128)))
	var dst any
	if err := readJSON(r, &dst); err == nil {
		t.Fatal("expected oversized body to fail without panic")
	}
}

type fixture struct {
	t       *testing.T
	ts      *httptest.Server
	agent   *agent.Agent
	srv     *Server
	ws      string
	mock    *llm.MockClient
	dataDir string
}

func newFixture(t *testing.T, scripts []llm.Scripted) *fixture {
	t.Helper()
	ws := t.TempDir()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = ws
	cfg.DataDir = dataDir
	cfg.LLM.Provider = "mock"
	cfg.Safety.ApprovalTimeoutSecs = 5
	for _, d := range []string{"memory", "skills", "tasks"} {
		_ = os.MkdirAll(filepath.Join(dataDir, d), 0o755)
	}

	m := llm.NewMock()
	m.Apply(scripts)
	mem, err := memory.Open(dataDir, 20, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := skill.Open(filepath.Join(dataDir, "skills"))
	gate := safety.New("auto", cfg.Safety.TrustedTools, nil, []string{ws}, 3*time.Second)
	r := registry.New()
	fileTools := file.New(ws)
	fileTools.RegisterAll(r)
	r.MustRegister(std.NewReply())
	sched, _ := scheduler.Open(filepath.Join(dataDir, "schedules.json"), nil)

	a := agent.New(cfg, m, r, mem, gate, store, sched, nil)
	a.FileTools = fileTools
	if convo, err := conversation.Open(dataDir); err == nil {
		a.Convos = convo
	}
	srv := NewServer(a)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &fixture{t: t, ts: ts, agent: a, srv: srv, ws: ws, mock: m, dataDir: dataDir}
}

func (f *fixture) call(method, path string, body any) map[string]any {
	f.t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 400 {
		f.t.Fatalf("%s %s -> %d: %v", method, path, resp.StatusCode, out)
	}
	return out
}

// waitTask 轮询直到任务结束或超时。
func (f *fixture) waitTask(id string, timeout time.Duration) map[string]any {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := f.call("GET", "/api/goals/"+id, nil)
		if s, _ := out["status"].(string); s != "running" {
			return out
		}
		time.Sleep(30 * time.Millisecond)
	}
	f.t.Fatal("等待任务结束超时")
	return nil
}

// ---------- 基础 ----------

func TestWebUI_InfoAndStatic(t *testing.T) {
	f := newFixture(t, nil)
	info := f.call("GET", "/api/info", nil)
	if info["name"] != "gleam" {
		t.Errorf("info = %v", info)
	}
	resp, err := http.Get(f.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("首页状态 = %d", resp.StatusCode)
	}
	var buf [96]byte
	n, _ := resp.Body.Read(buf[:])
	if !strings.Contains(string(buf[:n]), "<!DOCTYPE html>") {
		t.Errorf("首页内容异常: %q", string(buf[:n]))
	}
	if resp2, err := http.Get(f.ts.URL + "/assets/app.js"); err != nil || resp2.StatusCode != 200 {
		t.Errorf("app.js 加载失败: %v", err)
	} else {
		resp2.Body.Close()
	}
}

func TestTaskInfoSnapshotIsIndependent(t *testing.T) {
	original := &taskInfo{
		ID: "task-1", Status: types.GoalRunning,
		Events: []sseEvent{newEvent("progress", map[string]any{"progress": 1})},
		Cancel: func() {},
	}
	snapshot := original.snapshot()
	if snapshot.Cancel != nil {
		t.Fatal("快照不应暴露取消函数")
	}
	snapshot.Events[0] = newEvent("completed", map[string]any{"score": 100})
	if original.Events[0].Type != "progress" {
		t.Fatal("快照与原任务共享事件切片")
	}
}

func TestWebUI_RolesAvailableForComposer(t *testing.T) {
	f := newFixture(t, nil)
	out := f.call("GET", "/api/roles", nil)
	roles, ok := out["roles"].([]any)
	if !ok || len(roles) < 2 {
		t.Fatalf("角色列表异常: %v", out)
	}
	found := map[string]bool{}
	for _, raw := range roles {
		role, _ := raw.(map[string]any)
		found[fmt.Sprint(role["id"])] = true
	}
	for _, id := range []string{"general", "analyst"} {
		if !found[id] {
			t.Errorf("角色列表缺少 %q: %v", id, out)
		}
	}
}

func TestWebUI_GoalRejectsInvalidReference(t *testing.T) {
	f := newFixture(t, nil)
	body := `{"goal":"测试目标","references":[{"kind":"unknown","label":"x","value":"x"}]}`
	resp, err := http.Post(f.ts.URL+"/api/goals", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，应为 400", resp.StatusCode)
	}
	if len(f.srv.tasks) != 0 {
		t.Fatalf("无效请求不应创建任务: %v", f.srv.tasks)
	}
}

// ---------- 目标全流程（无审批） ----------

func TestWebUI_GoalFullFlow(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"web.txt","content":"来自 Web UI"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"网页流程完成"},"depends_on":["s1"]}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":96,"verdict":"done","reason":"ok","suggestion":"建议归档"}`}},
	})
	out := f.call("POST", "/api/goals", map[string]any{"goal": "创建 web.txt", "mode": "auto"})
	id, _ := out["task_id"].(string)
	if id == "" {
		t.Fatalf("submit = %v", out)
	}
	done := f.waitTask(id, 10*time.Second)
	if done["status"] != "success" {
		t.Fatalf("结果 = %v", done)
	}
	result := done["result"].(map[string]any)
	if result["summary"] != "网页流程完成" {
		t.Errorf("summary = %v", result["summary"])
	}
	if result["score"] != float64(96) {
		t.Errorf("score = %v", result["score"])
	}
	data, err := os.ReadFile(filepath.Join(f.ws, "web.txt"))
	if err != nil || string(data) != "来自 Web UI" {
		t.Errorf("产物错误: %v %q", err, data)
	}
	// 事件日志已持久在任务上
	detail := f.call("GET", "/api/goals/"+id, nil)
	events := detail["events"].([]any)
	if len(events) == 0 {
		t.Error("任务事件日志为空")
	}
	foundPlan := false
	for _, e := range events {
		ev := e.(map[string]any)
		if ev["type"] == "progress" {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Error("缺少 progress 事件")
	}
}

// ---------- 审批回路 ----------

func TestWebUI_ApprovalRoundTrip(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"删除","tool":"file.delete","args":{"path":"victim.txt","recursive":false}}]}`}},
		{Kind: "reflect", Texts: []string{`{"score":95,"verdict":"done","reason":"已删除"}`}},
	})
	victim := filepath.Join(f.ws, "victim.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := f.call("POST", "/api/goals", map[string]any{"goal": "删除 victim.txt"})
	id, _ := out["task_id"].(string)

	// 轮询待决审批
	var approvals []any
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		res := f.call("GET", "/api/approvals", nil)
		approvals, _ = res["approvals"].([]any)
		if len(approvals) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(approvals) == 0 {
		t.Fatal("未出现审批请求")
	}
	ap := approvals[0].(map[string]any)
	if ap["risk"] != "high" {
		t.Errorf("risk = %v", ap["risk"])
	}
	f.call("POST", fmt.Sprintf("/api/approvals/%s", ap["id"]), map[string]any{"approved": true})

	done := f.waitTask(id, 10*time.Second)
	if done["status"] != "success" {
		t.Fatalf("批准后应成功: %v", done["status"])
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Error("批准后文件应被删除")
	}
	// 审批事件留在任务日志
	detail := f.call("GET", "/api/goals/"+id, nil)
	found := false
	for _, e := range detail["events"].([]any) {
		if e.(map[string]any)["type"] == "approval" {
			found = true
		}
	}
	if !found {
		t.Error("任务日志缺少 approval 事件")
	}
}

// ---------- SSE ----------

func TestWebUI_SSEStream(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"sse ok"}}]}`}},
		{Kind: "reflect", Texts: []string{`{"score":90,"verdict":"done","reason":"ok"}`}},
	})
	// 先订阅 SSE 再提交目标，保证能收到事件
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", f.ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
	}()
	time.Sleep(200 * time.Millisecond) // 等 SSE 握手

	f.call("POST", "/api/goals", map[string]any{"goal": "sse 测试目标"})
	sawProgress, sawCompleted := false, false
	deadline := time.After(8 * time.Second)
	for !(sawProgress && sawCompleted) {
		select {
		case ev := <-events:
			switch ev {
			case "progress":
				sawProgress = true
			case "completed":
				sawCompleted = true
			}
		case <-deadline:
			t.Fatalf("SSE 事件缺失: progress=%v completed=%v", sawProgress, sawCompleted)
		}
	}
}

// ---------- 工具 / 记忆 / 技能 / 调度 ----------

func TestWebUI_ToolsMemorySkillsSchedules(t *testing.T) {
	f := newFixture(t, nil)

	tools := f.call("GET", "/api/tools", nil)
	if tools["count"] == float64(0) {
		t.Error("工具列表为空")
	}

	// 工具直调（只读）
	out := f.call("POST", "/api/tools/call", map[string]any{"name": "reply", "args": map[string]any{"text": "直调"}})
	if out["name"] != "reply" {
		t.Errorf("tools/call = %v", out)
	}

	// 记忆
	f.call("POST", "/api/memory", map[string]any{"content": "用户偏好深色界面", "tags": []string{"偏好"}})
	hits := f.call("GET", "/api/memory?q=%E6%B7%B1%E8%89%B2%E7%95%8C%E9%9D%A2&k=3", nil)
	if hits["count"] == float64(0) {
		t.Error("记忆检索无命中")
	}
	// L3（2026-09-23 QA）：单条软删——删后检索不再命中，误删不存在的要 404
	firstHit := hits["hits"].([]any)[0].(map[string]any)
	memID, _ := firstHit["id"].(string)
	if memID == "" {
		t.Fatal("检索结果应带 id 供删除")
	}
	f.call("DELETE", "/api/memory/"+memID, nil)
	after := f.call("GET", "/api/memory?q=%E6%B7%B1%E8%89%B2%E7%95%8C%E9%9D%A2&k=3", nil)
	for _, it := range after["hits"].([]any) {
		if h := it.(map[string]any); h["id"] == memID {
			t.Error("软删后该条仍被检索命中")
		}
	}
	if code, _ := f.raw("DELETE", "/api/memory/nope-404", nil); code != 404 {
		t.Errorf("删除不存在的记忆应 404，实际 %d", code)
	}

	// 技能
	f.call("POST", "/api/skills", map[string]any{
		"name": "web-skill", "description": "d",
		"steps": []map[string]any{
			{"id": "s1", "tool": "file.write", "args": map[string]any{"path": "sk.txt", "content": "{{word}}"}},
		},
		"params": []string{"word"},
	})
	run := f.call("POST", "/api/skills/web-skill/run", map[string]any{"params": map[string]string{"word": "技能输出"}})
	if run["status"] != "success" {
		t.Errorf("技能运行 = %v", run)
	}
	data, _ := os.ReadFile(filepath.Join(f.ws, "sk.txt"))
	if string(data) != "技能输出" {
		t.Errorf("技能产物 = %q", data)
	}
	list := f.call("GET", "/api/skills", nil)
	if list["count"] == float64(0) {
		t.Error("技能列表为空")
	}
	f.call("DELETE", "/api/skills/web-skill", nil)

	// 调度
	f.call("POST", "/api/schedules", map[string]any{"name": "web-job", "goal": "g", "cron": "0 9 * * *"})
	jobs := f.call("GET", "/api/schedules", nil)
	if jobs["count"] == float64(0) {
		t.Error("调度列表为空")
	}
	f.call("DELETE", "/api/schedules/web-job", nil)

	// 自然语言时间：后端解析为 cron 并记录人读说明。
	nl := f.call("POST", "/api/schedules", map[string]any{"name": "nl-job", "goal": "整理下载目录", "when": "工作日18:30"})
	if nl["cron"] != "30 18 * * 1-5" {
		t.Errorf("自然语言 cron = %v", nl["cron"])
	}
	if nl["schedule_text"] == "" {
		t.Error("自然语言任务缺少人读调度说明")
	}
	f.call("DELETE", "/api/schedules/nl-job", nil)
}

// ---------- 取消 ----------

func TestWebUI_Cancel(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"慢","tool":"slow"}]}`}},
	})
	f.agent.Reg.Replace(&slowTool{})
	out := f.call("POST", "/api/goals", map[string]any{"goal": "慢任务"})
	id, _ := out["task_id"].(string)
	time.Sleep(100 * time.Millisecond)
	f.call("POST", "/api/goals/"+id+"/cancel", nil)
	done := f.waitTask(id, 5*time.Second)
	if done["status"] != "cancelled" {
		t.Errorf("取消后状态 = %v", done["status"])
	}
}

type slowTool struct{}

func (slowTool) Name() string                 { return "slow" }
func (slowTool) Description() string          { return "slow" }
func (slowTool) Schema() map[string]any       { return map[string]any{"type": "object"} }
func (slowTool) Permission() types.Permission { return types.PermissionReadOnly }
func (slowTool) Execute(ctx context.Context, _ map[string]any) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// ---------- 多会话 ----------

func TestWebUI_ConversationsCRUD(t *testing.T) {
	f := newFixture(t, nil)
	// 新建
	created := f.call("POST", "/api/conversations", nil)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("创建会话返回异常: %v", created)
	}
	if msgs, _ := created["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("新会话不应含消息: %v", msgs)
	}
	// 列表
	list := f.call("GET", "/api/conversations", nil)
	items, _ := list["conversations"].([]any)
	if len(items) != 1 {
		t.Fatalf("列表数量=%d", len(items))
	}
	// 重命名
	renamed := f.call("PATCH", "/api/conversations/"+id, map[string]any{"title": "我的测试对话"})
	if renamed["title"] != "我的测试对话" {
		t.Fatalf("重命名失败: %v", renamed["title"])
	}
	// 激活（载入上下文）
	act := f.call("POST", "/api/conversations/"+id+"/activate", nil)
	if act["id"] != id {
		t.Fatalf("激活返回异常: %v", act)
	}
	// 删除
	f.call("DELETE", "/api/conversations/"+id, nil)
	items2, _ := f.call("GET", "/api/conversations", nil)["conversations"].([]any)
	if len(items2) != 0 {
		t.Fatalf("删除后列表仍有 %d 项", len(items2))
	}
}

func TestWebUI_ConversationRecordsChatTurns(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "chat_mode", Texts: []string{"你好，我是微光，有什么可以帮你？"}},
	})
	created := f.call("POST", "/api/conversations", nil)
	id, _ := created["id"].(string)

	out := f.call("POST", "/api/goals", map[string]any{
		"goal": "你好呀", "mode": "auto", "task_mode": "chat", "conversation_id": id,
	})
	taskID, _ := out["task_id"].(string)
	done := f.waitTask(taskID, 10*time.Second)
	if done["status"] != "success" {
		t.Fatalf("chat 结果异常: %v", done["status"])
	}

	detail := f.call("GET", "/api/conversations/"+id, nil)
	msgs, _ := detail["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("会话消息数=%d, 期望 2", len(msgs))
	}
	u := msgs[0].(map[string]any)
	a := msgs[1].(map[string]any)
	if u["role"] != "user" || u["content"] != "你好呀" {
		t.Errorf("用户消息异常: %v", u)
	}
	if a["role"] != "assistant" || a["content"] != "你好，我是微光，有什么可以帮你？" {
		t.Errorf("助手消息异常: %v", a)
	}
	if a["task_id"] != taskID {
		t.Errorf("助手消息未关联 task_id")
	}
	if detail["title"] != "你好呀" {
		t.Errorf("标题未从首条用户消息生成: %v", detail["title"])
	}
}

func TestWebUI_ConversationUnknownID404(t *testing.T) {
	f := newFixture(t, nil)
	req, _ := http.NewRequest("GET", f.ts.URL+"/api/conversations/deadbeefdeadbeef", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("不存在的会话应返回 404, 实际 %d", resp.StatusCode)
	}
}
