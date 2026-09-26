package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/config"
	"gleam/internal/harness/credentials"
	"gleam/internal/llm"
	"gleam/pkg/types"
)

// ---------- 归档任务要读得回来（批次 F-2 / 清单 P2）----------

// newArchiveFixture 装一个最小 Web UI：只需要 Agent 的 DataDir 与一个空的内存任务表。
//
// 刻意不装模型 / 工具 / 记忆 / 调度——这条测试问的是"盘上有档案时接口怎么答"，
// 装全套只会让失败原因变多，而它要守的事与那些组件无关。
func newArchiveFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dataDir
	if err := os.MkdirAll(filepath.Join(dataDir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := agent.New(cfg, nil, nil, nil, nil, nil, nil, nil)
	return NewServer(a), dataDir
}

// writeArchive 按归档出口（`agent.SaveTaskResult`）落一份终态记录。
//
// 刻意调真出口而不是自己拼 JSON：这份测试要验的是"接口怎么答"，不是"能不能写出一个文件"。
// 自己写一份就把落盘格式复制成了第二处 owner——格式一漂移，这些测试还是绿的，
// 而线上读不回来。
func writeArchive(t *testing.T, dataDir, id string, g types.GoalResult) {
	t.Helper()
	if err := agent.SaveTaskResult(dataDir, &g); err != nil {
		t.Fatalf("归档失败：%v", err)
	}
}

// TestGoalGet_FallsBackToArchive 是批次 F-2 的核心断言。
//
// 它守的是一个具体的损失：s.tasks 是**纯内存**表（进程重启即空，还有 maxRetainedTasks
// 的淘汰上限），而 tasks/<id>.json 是终态归档、`gleam replay <id>` 读的就是它。
// 内存里没有就直接回 404，等于让 Web UI 对盘上真实存在的数据撒谎——
// 而 404 是一个明确、自信的答复：用户会据此认为记录丢了，然后重跑，重复花钱。
//
// 断言走**真实路由**（GET /api/goals/{id}）而不是直接调方法：
// 这样"端点有没有接上"也一起验了——判据对但线没接，是本仓库栽过多次的坑。
func TestGoalGet_FallsBackToArchive(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	writeArchive(t, dataDir, "task-archived-1", types.GoalResult{
		TaskID: "task-archived-1",
		Goal:   "昨晚跑的那个",
		Status: types.GoalSuccess,
		Score:  95,
	})

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/goals/task-archived-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("盘上有档案时应回 200，实得 %d", resp.StatusCode)
	}
	var got struct {
		TaskID string           `json:"task_id"`
		Goal   string           `json:"goal"`
		Status types.GoalStatus `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "task-archived-1" || got.Goal != "昨晚跑的那个" || got.Status != types.GoalSuccess {
		t.Errorf("归档回落的内容不对：%+v", got)
	}
}

// TestGoalGet_MemoryWinsOverArchive 守的是优先级：内存里**正在跑**的任务不能被档案盖掉。
//
// 档案是终态快照（状态一定不是 running），拿它去答一个正在跑的任务，
// 会把"跑着呢"报成"已结束"——这比 404 更坏，因为它是错的而不是空的。
func TestGoalGet_MemoryWinsOverArchive(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	writeArchive(t, dataDir, "dup", types.GoalResult{
		TaskID: "dup", Goal: "档案里的旧版本", Status: types.GoalFailed,
	})
	srv.mu.Lock()
	srv.tasks["dup"] = &taskInfo{ID: "dup", Goal: "内存里的新版本", Status: types.GoalRunning}
	srv.mu.Unlock()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/goals/dup")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Goal   string           `json:"goal"`
		Status types.GoalStatus `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Goal != "内存里的新版本" || got.Status != types.GoalRunning {
		t.Errorf("内存表里的任务应优先于档案，实得 %+v", got)
	}
}

// TestGoalGet_NoArchiveStill404 是负例控制：**没有档案时必须仍然是 404**。
//
// 少了它，"回落"很容易被写成"永远返回 200 加一个空壳"——那样 404 这个
// "确实没有"的答复就消失了，前端再也分不清"存在但空"与"不存在"。
func TestGoalGet_NoArchiveStill404(t *testing.T) {
	srv, _ := newArchiveFixture(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/goals/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("没有档案时应回 404，实得 %d", resp.StatusCode)
	}
}

// TestGoalSubmit_ArchivesForReplay 界面跑出来的任务必须留档（批次 F7）。
//
// 症状：`tasks/` 此前只有 CLI（gleam goal）与 stdio 服务（gleam serve）两条路径在写，
// 而桌面端的主路径——webui——只写内存表。于是这三件事同时不成立：
// 重启后任务详情 404、`gleam replay <id>` 报"没有落盘的计划"、评测的 badcase 回流
// 一条真实用户跑过的任务都拿不到。归档的读侧与工具链早就齐了，缺的只是这一处写。
//
// 断言走完整个环：提交 → 等结束 → 盘上读得回来 → **清空内存表（模拟重启）** → 接口仍回 200。
// 只验第一步的话，"写得出去读不回来"照样绿。
func TestGoalSubmit_ArchivesForReplay(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"arch.txt","content":"留档"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"留档完成"},"depends_on":["s1"]}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":95,"verdict":"done","reason":"ok"}`}},
	})
	out := f.call("POST", "/api/goals", map[string]any{"goal": "创建 arch.txt", "mode": "auto"})
	id, _ := out["task_id"].(string)
	if id == "" {
		t.Fatalf("submit = %v", out)
	}
	if done := f.waitTask(id, 10*time.Second); done["status"] != "success" {
		t.Fatalf("任务没跑成：%v", done)
	}

	g, err := agent.ReadTaskResult(f.dataDir, id)
	if err != nil || g == nil {
		t.Fatalf("界面任务应归档到 tasks/：读到=%v err=%v", g, err)
	}
	if g.Goal != "创建 arch.txt" || g.Status != types.GoalSuccess {
		t.Errorf("归档内容不对：goal=%q status=%q", g.Goal, g.Status)
	}

	f.srv.mu.Lock()
	f.srv.tasks = map[string]*taskInfo{}
	f.srv.mu.Unlock()
	detail := f.call("GET", "/api/goals/"+id, nil)
	if detail["status"] != "success" || detail["goal"] != "创建 arch.txt" {
		t.Errorf("清空内存表后应回落到归档，实得 %v", detail)
	}
}

// TestGoalSubmit_WarningDistinguishesKeyHost 换厂商时的提示不能说"未配置"（批次 F7 / F4 收尾）。
//
// 密钥按接入主机绑定之后，"本机存着别家的 key"与"从来没配过"是两件事：
// 都报成"未配置 API Key"，用户会去填一遍他已经有的那把，或者以为自己填丢了。
func TestGoalSubmit_WarningDistinguishesKeyHost(t *testing.T) {
	f := newFixture(t, nil)
	cred, err := credentials.Open(f.dataDir)
	if err != nil {
		t.Fatalf("开凭证库失败：%v", err)
	}
	f.agent.Creds = cred
	f.agent.Cfg.LLM.Provider = "openai"
	f.agent.Cfg.LLM.BaseURL = "https://api.b.example.com/v1"
	f.agent.Cfg.LLM.APIKey, f.agent.Cfg.LLM.APIKeyScope = "", ""
	if err := cred.SetLLMAPIKey("sk-A-只属于A", "api.a.example.com"); err != nil {
		t.Fatalf("预置 A 家密钥失败：%v", err)
	}

	out := f.call("POST", "/api/goals", map[string]any{"goal": "换厂商之后的提交", "mode": "auto"})
	warn, _ := out["warning"].(string)
	if !strings.Contains(warn, "api.a.example.com") {
		t.Errorf("提示应点出密钥属于哪台主机，实得 %q", warn)
	}
	if strings.Contains(warn, "未配置 API Key") {
		t.Errorf("本机明明存着一把 key，不该报「未配置」：%q", warn)
	}
	// 等这一次提交真的收尾：只验响应就返回的话，后台那趟跑会活过 t.TempDir() 的清理，
	// 于是"警告语对不对"这条测试偶发红在目录删不掉上——报的还是错的原因。
	id, _ := out["task_id"].(string)
	if id == "" {
		t.Fatalf("submit = %v", out)
	}
	f.waitTask(id, 10*time.Second)
}

// TestGoalGet_RejectsPathTraversal 守的是回落读盘带来的新攻击面。
//
// id 来自 URL 且会被拼进文件路径，所以 `..` 与路径分隔符必须被挡住——
// 挡不住就等于把数据目录下的**任意 JSON** 变成一个可读接口（settings 里可能有密钥）。
//
// 诱饵刻意放在 `<dataDir>/settings.json`：`tasks/../settings.json` 正好解析到它，
// 于是"守卫没挡住"这件事会**真的读到一个文件**。诱饵放错位置（比如放成 .yaml）
// 会让穿越路径解析失败、接口照样回 404——那样这条断言是空的，改坏了也不会响。
func TestGoalGet_RejectsPathTraversal(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	decoy := filepath.Join(dataDir, "settings.json")
	if err := os.WriteFile(decoy, []byte(`{"api_key":"不该被读到"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// ① 直接调方法：路由可能先挡掉一部分形态，只测 HTTP 的话守卫本身可能测不到。
	for _, id := range []string{"../settings", `..\settings`, "..settings", "/etc/passwd", ""} {
		if _, ok := srv.archivedTask(id); ok {
			t.Errorf("archivedTask(%q) 不该读得到 tasks/ 之外的文件", id)
		}
	}

	// ② 再走一遍真实路由：确认这一层也没有漏。
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	for _, id := range []string{"..%2Fsettings", "..%5Csettings", "..settings"} {
		resp, err := http.Get(ts.URL + "/api/goals/" + id)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || strings.Contains(string(body), "api_key") {
			t.Errorf("id=%q 不该读到 tasks/ 之外的文件：状态 %d，响应 %s", id, resp.StatusCode, body)
		}
	}
}

// TestGoalList_IncludesArchived 重启后"最近任务"不能是空的（批次 F8）。
//
// 详情接口早就回落到 tasks/ 了，列表却没有：于是重启之后界面显示「全部 0」，
// 而那份档案其实就在盘上、按 id 也读得到。用户据此会认为历史被清空，
// 而 404 与"从没跑过"是同一个假象的两个版本——都比真相更自信。
//
// 这条也顺手验去重：内存里正在跑的同 id 任务不能被档案盖掉（否则卡片会从"进行中"退回"已完成"）。
func TestGoalList_IncludesArchived(t *testing.T) {
	srv, dataDir := newArchiveFixture(t)
	writeArchive(t, dataDir, "task-list-1", types.GoalResult{
		TaskID:   "task-list-1",
		Goal:     "昨晚跑的那个",
		Status:   types.GoalSuccess,
		Mode:     "auto",
		TaskMode: types.TaskWork,
		Score:    95,
	})
	srv.mu.Lock()
	srv.tasks["task-live"] = &taskInfo{ID: "task-live", Goal: "正在跑的", Status: types.GoalRunning}
	srv.mu.Unlock()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/goals")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Count int        `json:"count"`
		Goals []taskInfo `json:"goals"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]taskInfo{}
	for _, g := range got.Goals {
		byID[g.ID] = g
	}
	if got.Count != 2 || len(byID) != 2 {
		t.Fatalf("应看到归档那条 + 内存那条，实得 %v", got.Goals)
	}
	arch, ok := byID["task-list-1"]
	if !ok {
		t.Fatalf("列表少了归档记录：%v", got.Goals)
	}
	if arch.Goal != "昨晚跑的那个" || arch.Status != types.GoalSuccess {
		t.Errorf("归档记录内容不对：%+v", arch)
	}
	// 徽标与筛选靠这两个字段：档案里没带，重启后卡片就只剩状态，看不出是对话还是编程任务
	if arch.Mode != "auto" || arch.TaskMode != string(types.TaskWork) {
		t.Errorf("归档应带出模式，实得 mode=%q task_mode=%q", arch.Mode, arch.TaskMode)
	}
	if live, ok := byID["task-live"]; !ok || live.Status != types.GoalRunning {
		t.Errorf("内存里正在跑的那条要在，且不被档案覆盖：%+v", live)
	}
}

// TestGoalSubmit_ArchivesRunMode 提交时选的模式要一起归档（F8）。
//
// 界面上的「对话 / 工作 / 编程」徽标重启后还得在，靠的就是结果里带这两个字段；
// 只写进内存表的话，第一次重启就会把它们抹掉。
func TestGoalSubmit_ArchivesRunMode(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"回复","tool":"reply","args":{"text":"模式留档"}}]}`}},
		{Kind: "reflect", Texts: []string{`{"score":90,"verdict":"done","reason":"ok"}`}},
	})
	// 用 auto：plan_first 会在审批处停下（那是另一条测试的事），这里要的是跑到底看归档。
	out := f.call("POST", "/api/goals", map[string]any{"goal": "留模式", "mode": "auto", "task_mode": "code"})
	id, _ := out["task_id"].(string)
	if done := f.waitTask(id, 10*time.Second); done["status"] != "success" {
		t.Fatalf("任务没跑成：%v", done)
	}
	g, err := agent.ReadTaskResult(f.dataDir, id)
	if err != nil || g == nil {
		t.Fatalf("没归档：%-v %v", g, err)
	}
	if g.Mode != "auto" || g.TaskMode != types.TaskCode {
		t.Errorf("归档应带出跑法，实得 mode=%q task_mode=%q", g.Mode, g.TaskMode)
	}
}
