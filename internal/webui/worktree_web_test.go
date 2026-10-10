package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gleam/internal/worktree"
)

// worktreeFixture 一个最小 Web UI + 一个工作区目录（是不是仓库由用例决定）。
func worktreeFixture(t *testing.T) (srv *Server, dataDir, ws string) {
	t.Helper()
	srv, dataDir = newArchiveFixture(t)
	ws = dirOutsideRepo(t)
	srv.Agent.Cfg.Workspace = ws
	srv.Agent.Cfg.Worktrees.Enabled = true
	return srv, dataDir, ws
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git")
	}
	for _, a := range [][]string{
		{"init"}, {"config", "user.email", "t@example.invalid"}, {"config", "user.name", "Gleam Test"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "-A"}, {"commit", "-m", "seed"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

// 在盘上按同一套布局建一份副本：接口只认 worktree 包那份元数据，用例也走同一个入口。
func seedWorktree(t *testing.T, dataDir, repo, taskID string) worktree.Meta {
	t.Helper()
	m := &worktree.Manager{DataDir: dataDir, BranchPrefix: func() string { return "gleam/" }}
	meta, err := m.Create(context.Background(), taskID, repo, "")
	if err != nil {
		t.Fatalf("建副本失败: %v", err)
	}
	return meta
}

// 工作区不是仓库时，这一页必须**说清建不了**，而不是画一个空列表——
// 空列表会被读成"还没建过副本"，而它们要用户做的事完全不同。
func TestWorktreeList_NotARepoIsSaidOutLoud(t *testing.T) {
	srv, _, ws := worktreeFixture(t)
	ts := newTokenTestServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/worktrees")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Enabled    bool             `json:"enabled"`
		Workspace  string           `json:"workspace"`
		Repository bool             `json:"repository"`
		Rows       []map[string]any `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Error("开关已在配置里打开，接口要如实回传")
	}
	if got.Repository {
		t.Error("普通目录不该被报成 git 仓库")
	}
	if got.Workspace != ws {
		t.Errorf("workspace = %q，应为 %q", got.Workspace, ws)
	}
	if len(got.Rows) != 0 {
		t.Errorf("不该有副本，实际 %+v", got.Rows)
	}
}

func TestWorktreeListAndCleanRemove(t *testing.T) {
	srv, dataDir, ws := worktreeFixture(t)
	gitInit(t, ws)
	meta := seedWorktree(t, dataDir, ws, "wt1")
	ts := newTokenTestServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/worktrees")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Repository bool `json:"repository"`
		Rows       []struct {
			TaskID string `json:"task_id"`
			Branch string `json:"branch"`
			Path   string `json:"path"`
			Dirty  bool   `json:"dirty"`
		} `json:"rows"`
	}
	err = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Repository {
		t.Error("这个工作区是仓库，应当如实说")
	}
	if len(got.Rows) != 1 || got.Rows[0].TaskID != "wt1" || got.Rows[0].Branch != "gleam/wt1" {
		t.Fatalf("rows = %+v", got.Rows)
	}
	if got.Rows[0].Dirty {
		t.Error("刚建出来的副本应当是干净的")
	}

	// 干净的：直接删掉
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/worktrees/wt1", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("干净副本应当能直接删，实得 %d", resp2.StatusCode)
	}
	if _, err := os.Stat(meta.Path); !os.IsNotExist(err) {
		t.Errorf("目录应当已删掉，stat err = %v", err)
	}
}

// 有未提交改动的副本：默认删不掉（409），带 force=1 才是"我知道会丢"。
// 这条界线是"不静默丢弃用户改动"的落点，必须在接口这一层也成立。
func TestWorktreeRemoveDirtyNeedsForce(t *testing.T) {
	srv, dataDir, ws := worktreeFixture(t)
	gitInit(t, ws)
	meta := seedWorktree(t, dataDir, ws, "wt2")
	if err := os.WriteFile(filepath.Join(meta.Path, "wip.txt"), []byte("没提交\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := newTokenTestServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/worktrees/wt2", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var denied struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&denied)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("有未提交改动时应当回 409（要用户确认），实得 %d", resp.StatusCode)
	}
	if denied.Error == "" {
		t.Error("拒绝时必须把原因说清楚，界面靠它显示给用户")
	}
	if _, err := os.Stat(meta.Path); err != nil {
		t.Errorf("被拒绝之后目录必须还在: %v", err)
	}

	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/worktrees/wt2?force=1", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("force 删除应当成功，实得 %d", resp2.StatusCode)
	}
	if _, err := os.Stat(meta.Path); !os.IsNotExist(err) {
		t.Errorf("force 之后目录应当没了，stat err = %v", err)
	}
}

// dirOutsideRepo 造一个**在仓库之外**的临时目录。
//
// GHA 的 Windows runner 把 TEMP 设成 <workspace>/.ci-tmp（见 .github/workflows/ci.yml，
// 为躲开 RUNNER~1 短路径），于是 t.TempDir() 造出来的"普通目录"本身就在 git 仓库里——
// git 向上就能找到外层仓库，"不是仓库"的断言于是全部反过来。改在缓存目录下建，
// 就与 TEMP 怎么设无关了。
func dirOutsideRepo(t *testing.T) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("没有可用的缓存目录，无法保证临时目录落在仓库之外：%v", err)
	}
	d, err := os.MkdirTemp(base, "gleam-norepo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}
