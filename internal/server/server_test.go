package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/config"
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

func TestConnCallRemovesPendingOnEncodeError(t *testing.T) {
	c := NewConn(strings.NewReader(""), io.Discard)
	err := c.Call(context.Background(), "bad", func() {}, nil)
	if err == nil {
		t.Fatal("expected JSON encoding error")
	}
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if len(c.pending) != 0 {
		t.Fatalf("pending requests leaked after encode error: %d", len(c.pending))
	}
}

// ---------- 测试用 JSON-RPC 客户端 ----------

type rpcClient struct {
	t       *testing.T
	r       *bufio.Reader
	w       *bufio.Writer
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan incoming
	notifs  chan incoming
	done    chan struct{}
}

func startServer(t *testing.T, scripts []llm.Scripted, approveAll bool) (*rpcClient, *agent.Agent, string) {
	t.Helper()
	ws := t.TempDir()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = ws
	cfg.DataDir = dataDir
	cfg.LLM.Provider = "mock"
	cfg.Safety.ApprovalTimeoutSecs = 5
	_ = os.MkdirAll(filepath.Join(dataDir, "memory"), 0o755)
	_ = os.MkdirAll(filepath.Join(dataDir, "skills"), 0o755)

	m := llm.NewMock()
	m.Apply(scripts)
	mem, err := memory.Open(dataDir, 20, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := skill.Open(filepath.Join(dataDir, "skills"))
	gate := safety.New("auto", cfg.Safety.TrustedTools, nil, []string{ws}, 5*time.Second)
	r := registry.New()
	file.New(ws).RegisterAll(r)
	r.MustRegister(std.NewReply())
	sched, _ := scheduler.Open(filepath.Join(dataDir, "schedules.json"), nil)
	a := agent.New(cfg, m, r, mem, gate, store, sched, nil)

	c1, c2 := net.Pipe()
	svc := NewService(a)
	conn := NewConn(c1, c1)
	svc.Bind(conn)
	go func() { _ = conn.Serve() }()

	cl := &rpcClient{
		t:       t,
		r:       bufio.NewReader(c2),
		w:       bufio.NewWriter(c2),
		pending: map[int64]chan incoming{},
		notifs:  make(chan incoming, 128),
		done:    make(chan struct{}),
	}
	go cl.loop(approveAll)
	t.Cleanup(func() { close(cl.done); c1.Close(); c2.Close() })
	return cl, a, ws
}

func (c *rpcClient) loop(approveAll bool) {
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return
		}
		var msg incoming
		if jsonErr := json.Unmarshal([]byte(line), &msg); jsonErr != nil {
			continue
		}
		switch {
		case msg.Method == "goal/ask_approval":
			// 模拟宿主应答审批
			var resp any
			if approveAll {
				resp = map[string]any{"approved": true}
			} else {
				resp = map[string]any{"approved": false, "note": "测试拒绝"}
			}
			c.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": resp})
		case msg.Method != "":
			select {
			case c.notifs <- msg:
			case <-c.done:
				return
			}
		default:
			var id int64
			if jsonErr := json.Unmarshal(msg.ID, &id); jsonErr == nil {
				c.mu.Lock()
				ch := c.pending[id]
				delete(c.pending, id)
				c.mu.Unlock()
				if ch != nil {
					ch <- msg
				}
			}
		}
	}
}

func (c *rpcClient) send(v any) {
	b, _ := json.Marshal(v)
	c.wmu()
	c.w.Write(append(b, '\n'))
	c.w.Flush()
}

func (c *rpcClient) wmu() {}

func (c *rpcClient) call(method string, params any) (json.RawMessage, *RPCError, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan incoming, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case resp := <-ch:
		return resp.Result, resp.Error, nil
	case <-time.After(10 * time.Second):
		return nil, nil, fmt.Errorf("等待 %s 响应超时", method)
	}
}

// waitNotification 等待指定方法的通知（跳过其他通知）。
func (c *rpcClient) waitNotification(method string, timeout time.Duration, filter func(incoming) bool) (incoming, error) {
	deadline := time.After(timeout)
	for {
		select {
		case n := <-c.notifs:
			if n.Method == method && (filter == nil || filter(n)) {
				return n, nil
			}
		case <-deadline:
			return incoming{}, fmt.Errorf("等待通知 %s 超时", method)
		}
	}
}

// ---------- 测试 ----------

func TestRPC_InitializeAndPing(t *testing.T) {
	cl, _, _ := startServer(t, nil, false)
	res, rpcErr, err := cl.call("initialize", map[string]any{})
	if err != nil || rpcErr != nil {
		t.Fatalf("initialize: %v %v", err, rpcErr)
	}
	var out struct {
		Name         string         `json:"name"`
		Model        string         `json:"model"`
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "gleam" || out.Capabilities["goal"] != true {
		t.Errorf("initialize = %s", res)
	}
	res, _, err = cl.call("ping", nil)
	if err != nil || !jsonContains(res, "pong") {
		t.Errorf("ping: %v %s", err, res)
	}
	// 未知方法
	_, rpcErr, _ = cl.call("no/such", nil)
	if rpcErr == nil || rpcErr.Code != CodeMethodNotFound {
		t.Errorf("未知方法应返回 -32601: %v", rpcErr)
	}
}

func TestRPC_GoalSubmitFullFlow(t *testing.T) {
	scripts := []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"rpc-out.txt","content":"rpc 内容"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"RPC 流程完成"},"depends_on":["s1"]}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":93,"verdict":"done","reason":"ok","suggestion":"记得提交代码"}`}},
	}
	cl, _, ws := startServer(t, scripts, false)

	subRes, rpcErr, err := cl.call("goal/submit", map[string]any{"goal": "创建 rpc-out.txt"})
	if err != nil || rpcErr != nil {
		t.Fatalf("submit: %v %v", err, rpcErr)
	}
	var sub struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(subRes, &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Status != "running" || sub.TaskID == "" {
		t.Fatalf("submit 结果 = %s", subRes)
	}

	// 收集通知直到 completed（保留全部，供后续断言）
	gotProgress := 0
	var suggestionText string
	var completed incoming
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && completed.Method == "" {
		select {
		case n := <-cl.notifs:
			switch n.Method {
			case "goal/progress":
				gotProgress++
			case "goal/completed":
				completed = n
			case "agent/suggestion":
				suggestionText = string(n.Params)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("等待通知超时")
		}
	}
	if completed.Method == "" {
		t.Fatal("未收到 goal/completed")
	}
	if gotProgress == 0 {
		t.Error("未收到任何进度通知")
	}
	var result types.GoalResult
	if err := json.Unmarshal(completed.Params, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != types.GoalSuccess || result.Score != 93 {
		t.Errorf("result = %+v", result)
	}
	if result.Summary != "RPC 流程完成" {
		t.Errorf("summary = %q", result.Summary)
	}
	// 文件确实生成
	if data, err := os.ReadFile(filepath.Join(ws, "rpc-out.txt")); err != nil || string(data) != "rpc 内容" {
		t.Errorf("产物错误: %v %q", err, data)
	}
	// 主动提议通知（在 completed 之前发出，已在上面的循环中捕获）
	if !containsStr(suggestionText, "记得提交代码") {
		t.Errorf("suggestion = %q", suggestionText)
	}
	// goal/status 查询
	statusRes, _, err := cl.call("goal/status", map[string]any{"task_id": sub.TaskID})
	if err != nil || !jsonContains(statusRes, "success") {
		t.Errorf("goal/status: %v %s", err, statusRes)
	}
}

func TestRPC_GoalRejectsInvalidReference(t *testing.T) {
	cl, _, _ := startServer(t, nil, false)
	_, rpcErr, err := cl.call("goal/submit", map[string]any{
		"goal":       "测试目标",
		"references": []map[string]string{{"kind": "unknown", "label": "x", "value": "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rpcErr == nil || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("应返回 InvalidParams: %v", rpcErr)
	}
}

func TestGoalListNewestFirst(t *testing.T) {
	now := time.Now()
	svc := &Service{tasks: map[string]*taskEntry{
		"old": {ID: "old", Goal: "旧任务", Status: types.GoalSuccess, Started: now.Add(-time.Minute), Result: &types.GoalResult{Score: 90}},
		"new": {ID: "new", Goal: "新任务", Status: types.GoalRunning, Started: now},
	}}
	raw, rpcErr := svc.handleGoalList(context.Background(), nil)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	tasks := raw.(map[string]any)["tasks"].([]map[string]any)
	if len(tasks) != 2 || tasks[0]["task_id"] != "new" || tasks[1]["task_id"] != "old" {
		t.Fatalf("任务顺序异常: %v", tasks)
	}
	if _, exists := tasks[1]["result"]; exists {
		t.Fatal("列表快照不应携带完整结果")
	}
}

func TestRPC_GoalSubmitApprovalRoundTrip(t *testing.T) {
	scripts := []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"删除文件","tool":"file.delete","args":{"path":"victim.txt","recursive":false}}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":40,"verdict":"replan","reason":"失败"}`}},
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"删除被拒，已放弃"}}]}`}},
		{Kind: "reflect", Texts: []string{`{"score":85,"verdict":"done","reason":"ok"}`}},
	}
	cl, _, ws := startServer(t, scripts, false)
	victim := filepath.Join(ws, "victim.txt")
	os.WriteFile(victim, []byte("x"), 0o644)

	// 客户端 loop 中 approveAll=false → 拒绝
	_, rpcErr, err := cl.call("goal/submit", map[string]any{"goal": "删除 victim.txt"})
	if err != nil || rpcErr != nil {
		t.Fatalf("submit: %v %v", err, rpcErr)
	}
	n, err := cl.waitNotification("goal/completed", 15*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContains(n.Params, "已放弃") {
		t.Errorf("completed = %s", n.Params)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Error("拒绝后文件不应被删除")
	}
	// 批准路径：删除成功且反思直接判定完成
	approveScripts := []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"删除文件","tool":"file.delete","args":{"path":"victim.txt","recursive":false}}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":95,"verdict":"done","reason":"已删除"}`}},
	}
	cl2, _, ws2 := startServer(t, approveScripts, true)
	victim2 := filepath.Join(ws2, "victim.txt")
	os.WriteFile(victim2, []byte("x"), 0o644)
	_, _, _ = cl2.call("goal/submit", map[string]any{"goal": "删除 victim.txt"})
	n2, err := cl2.waitNotification("goal/completed", 15*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonContains(n2.Params, "success") {
		t.Errorf("批准场景应成功: %s", n2.Params)
	}
	if _, err := os.Stat(victim2); !os.IsNotExist(err) {
		t.Error("批准后文件应被删除")
	}
}

func TestRPC_ToolsAndMemoryAndSkills(t *testing.T) {
	cl, _, ws0 := startServer(t, nil, false)
	// tools/list
	res, _, err := cl.call("tools/list", nil)
	if err != nil || !jsonContains(res, "file.write") {
		t.Fatalf("tools/list: %v %s", err, res)
	}
	// tools/call（只读直调）
	if err := os.WriteFile(filepath.Join(ws0, "callme.txt"), []byte("直调目标"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rpcErr2 *RPCError
	res, rpcErr2, err = cl.call("tools/call", map[string]any{"name": "file.read", "args": map[string]any{"path": "callme.txt"}})
	if err != nil || rpcErr2 != nil || !jsonContains(res, "直调目标") {
		t.Errorf("tools/call: %v %v %s", err, rpcErr2, res)
	}
	// memory/save + search
	_, _, err = cl.call("memory/save", map[string]any{"content": "用户偏好：简洁回复", "tags": []string{"偏好"}})
	if err != nil {
		t.Fatalf("memory/save: %v", err)
	}
	res, _, err = cl.call("memory/search", map[string]any{"query": "回复偏好", "k": 3})
	if err != nil || !jsonContains(res, "简洁回复") {
		t.Errorf("memory/search: %v %s", err, res)
	}
	// skills/save + run
	steps := `[
		{"id":"s1","tool":"file.write","args":{"path":"sk.txt","content":"技能产出"}},
		{"id":"s2","tool":"file.read","args":{"path":"sk.txt"},"depends_on":["s1"]}
	]`
	res, _, err = cl.call("skills/save", jsonToParams(`{"name":"rpc-skill","description":"d","steps":`+steps+`}`))
	if err != nil || !jsonContains(res, "saved") {
		t.Fatalf("skills/save: %v %s", err, res)
	}
	res, _, err = cl.call("skills/run", map[string]any{"name": "rpc-skill"})
	if err != nil || !jsonContains(res, "success") {
		t.Errorf("skills/run: %v %s", err, res)
	}
	res, _, err = cl.call("skills/list", nil)
	if err != nil || !jsonContains(res, "rpc-skill") {
		t.Errorf("skills/list: %v %s", err, res)
	}
	// schedule create/list/delete
	res, _, err = cl.call("schedule/create", map[string]any{"name": "daily-dust", "goal": "打扫", "cron": "0 9 * * *"})
	if err != nil || !jsonContains(res, "daily-dust") {
		t.Errorf("schedule/create: %v %s", err, res)
	}
	res, _, _ = cl.call("schedule/list", nil)
	if !jsonContains(res, "daily-dust") {
		t.Errorf("schedule/list: %s", res)
	}
	_, _, err = cl.call("schedule/delete", map[string]any{"name": "daily-dust"})
	if err != nil {
		t.Errorf("schedule/delete: %v", err)
	}
}

func TestRPC_GoalCancelAndErrors(t *testing.T) {
	cl, _, _ := startServer(t, nil, false)
	// 参数错误
	_, rpcErr, err := cl.call("goal/submit", map[string]any{"goal": ""})
	if err != nil || rpcErr == nil || rpcErr.Code != CodeInvalidParams {
		t.Errorf("空 goal 应返回 -32602: %v %v", err, rpcErr)
	}
	// 不存在的任务
	_, rpcErr, err = cl.call("goal/status", map[string]any{"task_id": "nope"})
	if err != nil || rpcErr == nil {
		t.Errorf("不存在任务应报错: %v %v", err, rpcErr)
	}
	// cancel 不存在的任务（幂等返回）
	res, _, err := cl.call("goal/cancel", map[string]any{"task_id": "nope"})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !jsonContains(res, "cancelled") {
		t.Errorf("cancel = %s", res)
	}
	// goal/list
	res, _, err = cl.call("goal/list", nil)
	if err != nil || !jsonContains(res, "tasks") {
		t.Errorf("goal/list: %v %s", err, res)
	}
}

// ---------- helpers ----------

func jsonContains(raw json.RawMessage, sub string) bool {
	return raw != nil && len(raw) > 0 && containsStr(string(raw), sub)
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func jsonToParams(s string) json.RawMessage {
	return json.RawMessage(s)
}

var _ = context.Background
