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

	"gleam/internal/agent"
	"gleam/internal/config"
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

// writeArchive 按 cmd/gleam/main.go 与 internal/server/service.go 的写法落一份终态记录。
func writeArchive(t *testing.T, dataDir, id string, g types.GoalResult) {
	t.Helper()
	b, err := json.MarshalIndent(g, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "tasks", id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
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
