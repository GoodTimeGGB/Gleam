package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/agent/geo"
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
)

// stubLLM 确定性假模型：创作请求回一篇够长的正文，GEO 请求回固定评分。
type stubLLM struct {
	mu       sync.Mutex
	geoCalls int
	planJSON string // 非空时，规划请求返回该 JSON（走完整 Plan-Execute 链路）
	// reflectJSON 非空时，反思请求返回该 JSON（用于验证逐条验收判定）
	reflectJSON string
}

const stubArticle = "远程办公不是福利，而是生产关系的重构。它把协作成本从通勤转移到信息与信任上，" +
	"因此衡量标准也应从“在岗时长”切换为“产出可验证性”。对团队而言，关键动作有三：第一，把目标拆成可独立交付的切片；" +
	"第二，把决策与结论写成可被检索的文档，而不是留在会议里；第三，用异步沟通替代同步等待。"

func (s *stubLLM) Chat(_ context.Context, req llm.ChatRequest) (string, error) {
	if strings.Contains(req.System, llm.MarkerGEO) {
		s.mu.Lock()
		s.geoCalls++
		s.mu.Unlock()
		text := `{"score":75,"summary":"结构清晰，建议补充数据支撑","strengths":["结论前置"],"weaknesses":["缺少具体案例"],"actionables":[{"category":"引用性","description":"补充 1-2 个具体数据","priority":"high"}]}`
		llm.ReportUsage(req, text, llm.Usage{})
		return text, nil
	}
	var text string
	switch {
	case s.planJSON != "" && strings.Contains(req.System, llm.MarkerPlan):
		text = s.planJSON
	case s.reflectJSON != "" && strings.Contains(req.System, llm.MarkerReflect):
		text = s.reflectJSON
	default:
		text = stubArticle
	}
	llm.ReportUsage(req, text, llm.Usage{})
	return text, nil
}

func (s *stubLLM) ChatStream(ctx context.Context, req llm.ChatRequest, onDelta func(string)) (string, error) {
	// 流式分支不能复用 Chat，否则用量会被重复上报一次
	var text string
	switch {
	case s.planJSON != "" && strings.Contains(req.System, llm.MarkerPlan):
		text = s.planJSON
	case s.reflectJSON != "" && strings.Contains(req.System, llm.MarkerReflect):
		text = s.reflectJSON
	default:
		text = stubArticle
	}
	llm.ReportUsage(req, text, llm.Usage{})
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

func (s *stubLLM) Name() string { return "stub" }

// callsN 读取 GEO 调用次数（异步分析线程写入，需加锁读）。
func (s *stubLLM) callsN() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.geoCalls
}

func newGEOFixture(t *testing.T) (*fixture, *stubLLM) {
	return newGEOFixtureWithPlan(t, "")
}

func newGEOFixtureWithPlan(t *testing.T, planJSON string) (*fixture, *stubLLM) {
	return newGEOFixtureFull(t, planJSON, "")
}

// newGEOFixtureFull 同时桩掉规划与反思两个环节，用于验证逐条验收判定贯通到前端。
func newGEOFixtureFull(t *testing.T, planJSON, reflectJSON string) (*fixture, *stubLLM) {
	t.Helper()
	ws := t.TempDir()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.Workspace = ws
	cfg.DataDir = dataDir
	cfg.Safety.ApprovalTimeoutSecs = 5
	for _, d := range []string{"memory", "skills", "tasks"} {
		_ = os.MkdirAll(filepath.Join(dataDir, d), 0o755)
	}
	stub := &stubLLM{planJSON: planJSON, reflectJSON: reflectJSON}
	mem, err := memory.Open(dataDir, 20, 128, 100)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := skill.Open(filepath.Join(dataDir, "skills"))
	gate := safety.New("auto", nil, nil, []string{ws}, 3*time.Second)
	r := registry.New()
	fileTools := file.New(ws)
	fileTools.RegisterAll(r)
	r.MustRegister(std.NewReply())
	sched, _ := scheduler.Open(filepath.Join(dataDir, "schedules.json"), nil)

	a := agent.New(cfg, stub, r, mem, gate, store, sched, nil)
	a.FileTools = fileTools
	geoStore, err := geo.Open(filepath.Join(dataDir, "geo_history.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.GEO = geoStore
	if convo, err := conversation.Open(dataDir); err == nil {
		a.Convos = convo
	}
	srv := NewServer(a)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &fixture{t: t, ts: ts, agent: a, srv: srv, ws: ws, dataDir: dataDir}, stub
}

// waitGEO 轮询直到 GEO 历史出现记录（自动分析在任务完成后异步执行）。
func waitGEO(f *fixture, want int, timeout time.Duration) []geo.Record {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := f.call("GET", "/api/geo", nil)
		raw, _ := json.Marshal(out["records"])
		var recs []geo.Record
		_ = json.Unmarshal(raw, &recs)
		if len(recs) >= want {
			waitGEOPersisted(f, want)
			return recs
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.t.Fatalf("等待 GEO 记录超时（期望 %d 条）", want)
	return nil
}

// waitGEOPersisted 等待 GEO 历史文件完成落盘。自动分析先写内存再异步落盘，
// 若测试在落盘（写 tmp + rename）结束前返回，t.TempDir 的 RemoveAll 会在
// Windows 上偶发「文件被占用」清理失败，表现为与本断言无关的假红。
func waitGEOPersisted(f *fixture, want int) {
	f.t.Helper()
	path := filepath.Join(f.dataDir, "geo_history.json")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var recs []geo.Record
			if json.Unmarshal(data, &recs) == nil && len(recs) >= want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("GEO 历史未完成落盘（期望至少 %d 条）: %s", want, path)
}

// TestGEO_AutoAnalyzeAfterCreativeTask 创作任务完成后应自动留档一条 GEO 建议。
func TestGEO_AutoAnalyzeAfterCreativeTask(t *testing.T) {
	f, stub := newGEOFixture(t)
	res := f.call("POST", "/api/goals", map[string]any{
		"goal":      "写一篇关于远程办公的公众号文章",
		"task_mode": "chat",
		"role":      "writer",
	})
	id, _ := res["task_id"].(string)
	if id == "" {
		t.Fatalf("未返回 task_id: %v", res)
	}
	task := f.waitTask(id, 8*time.Second)
	if task["status"] != "success" {
		t.Fatalf("任务未成功: %v", task)
	}
	recs := waitGEO(f, 1, 8*time.Second)
	if recs[0].Score != 75 {
		t.Fatalf("分数不符: %d", recs[0].Score)
	}
	if recs[0].Source != "auto" {
		t.Fatalf("来源应为自动分析: %s", recs[0].Source)
	}
	if len(recs[0].Actionables) == 0 {
		t.Fatal("应包含可操作建议")
	}
	if stub.callsN() == 0 {
		t.Fatal("未调用模型做 GEO 分析")
	}
	out := f.call("GET", "/api/geo", nil)
	stats, _ := out["stats"].(map[string]any)
	if int(stats["total"].(float64)) < 1 {
		t.Fatalf("统计未累计: %v", stats)
	}
}

// TestGEO_AutoAnalyzeByIntent 未选创作者角色、但目标本身是创作时也应给出建议。
func TestGEO_AutoAnalyzeByIntent(t *testing.T) {
	f, _ := newGEOFixture(t)
	res := f.call("POST", "/api/goals", map[string]any{
		"goal":      "帮我写一篇产品发布推文，突出三个卖点",
		"task_mode": "chat",
		"role":      "general",
	})
	id, _ := res["task_id"].(string)
	task := f.waitTask(id, 8*time.Second)
	if task["status"] != "success" {
		t.Fatalf("任务未成功: %v", task)
	}
	recs := waitGEO(f, 1, 8*time.Second)
	if recs[0].Source != "auto" {
		t.Fatalf("应按创作意图自动分析，实际: %s", recs[0].Source)
	}
}

// TestGEO_SkipsNonCreative 非创作任务（工程类）不应打扰用户。
func TestGEO_SkipsNonCreative(t *testing.T) {
	f, stub := newGEOFixture(t)
	res := f.call("POST", "/api/goals", map[string]any{
		"goal":      "看一下当前工作区有哪些文件",
		"task_mode": "chat",
		"role":      "coder",
	})
	id, _ := res["task_id"].(string)
	f.waitTask(id, 8*time.Second)
	time.Sleep(500 * time.Millisecond)
	if n := stub.callsN(); n != 0 {
		t.Fatalf("非创作任务不应调用 GEO 分析，实际调用 %d 次", n)
	}
	out := f.call("GET", "/api/geo", nil)
	stats, _ := out["stats"].(map[string]any)
	if int(stats["total"].(float64)) != 0 {
		t.Fatalf("不应产生历史记录: %v", stats)
	}
}

// TestGEO_ManualAnalyzeAndClear 板块手动分析与清空。
func TestGEO_ManualAnalyzeAndClear(t *testing.T) {
	f, _ := newGEOFixture(t)
	out := f.call("POST", "/api/geo/analyze", map[string]any{
		"content": stubArticle,
		"goal":    "产品发布推文",
	})
	sugg, _ := out["suggestion"].(map[string]any)
	if int(sugg["score"].(float64)) != 75 {
		t.Fatalf("手动分析返回异常: %v", sugg)
	}
	if out["text"] == "" {
		t.Fatal("应返回格式化文本")
	}
	recs := waitGEO(f, 1, 3*time.Second)
	if recs[0].Source != "manual" {
		t.Fatalf("来源应为手动: %s", recs[0].Source)
	}
	f.call("DELETE", "/api/geo/history", nil)
	out = f.call("GET", "/api/geo", nil)
	recsRaw, _ := json.Marshal(out["records"])
	var recs2 []geo.Record
	_ = json.Unmarshal(recsRaw, &recs2)
	if len(recs2) != 0 {
		t.Fatalf("清空后仍有 %d 条", len(recs2))
	}
}

// TestGEO_RejectsTooShort 过短内容应被拒绝，避免无意义调用。
func TestGEO_RejectsTooShort(t *testing.T) {
	f, _ := newGEOFixture(t)
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/geo/analyze", strings.NewReader(`{"content":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d", resp.StatusCode)
	}
}

// TestGEO_ToggleEnabled 设置开关可关闭自动分析。
func TestGEO_ToggleEnabled(t *testing.T) {
	f, _ := newGEOFixture(t)
	out := f.call("POST", "/api/settings", map[string]any{"agent": map[string]any{"geo_enabled": false}})
	agentView, _ := out["agent"].(map[string]any)
	if agentView["geo_enabled"] != false {
		t.Fatalf("开关未生效: %v", agentView)
	}
	if f.agent.Cfg.Agent.GEOEnabled {
		t.Fatal("运行时配置未同步关闭")
	}
	// 恢复开启，断言视图回显
	out = f.call("POST", "/api/settings", map[string]any{"agent": map[string]any{"geo_enabled": true}})
	agentView, _ = out["agent"].(map[string]any)
	if agentView["geo_enabled"] != true {
		t.Fatalf("未能恢复开启: %v", agentView)
	}
}
