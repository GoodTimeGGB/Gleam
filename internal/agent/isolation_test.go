package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/internal/tools/file"
	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// gitRepo 把一个目录变成真有提交的 git 仓库：worktree 是 git 自己的概念，
// 桩不出一个能用的来。身份写进仓库本地配置（我们起独立 git 进程，测试进程的环境变量对它无效）。
func gitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git")
	}
	steps := [][]string{{"init"}, {"config", "user.email", "t@example.invalid"}, {"config", "user.name", "Gleam Test"}}
	for _, a := range steps {
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

// noteTo 把 isolateForTask 要的回调转成记录器：它报的是 (phase, msg, pct, kind)，
// Notifier 收的是事件结构，两者之间只差这一层拼装。
func noteTo(n *recordingNotifier) func(phase, msg string, pct int, kind string) {
	return func(phase, msg string, pct int, kind string) {
		n.OnProgress(types.ProgressEvent{Phase: phase, Message: msg, Progress: pct, Kind: kind})
	}
}

// progressText 把记录下来的进度文案拼起来，供断言"有没有把降级原因说出来"。
func progressText(n *recordingNotifier) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var b strings.Builder
	for _, ev := range n.progress {
		b.WriteString(ev.Kind + ":" + ev.Message + "\n")
	}
	return b.String()
}

// 开关打开且工作区是仓库：任务应当拿到一个副本目录，规划用的 cwd 也在那里。
func TestIsolateForTaskCreatesWorktree(t *testing.T) {
	f := newFixture(t, nil)
	gitRepo(t, f.ws)
	f.a.Cfg.Worktrees.Enabled = true

	iso := f.a.isolateForTask(context.Background(), types.GoalRequest{}, "task1", func(string, string, int, string) {})
	if len(iso.roots) != 1 {
		t.Fatalf("应当拿到一个副本根，实际 %+v", iso)
	}
	want := filepath.Join(f.a.Cfg.DataDir, "worktrees", "task1")
	if filepath.Clean(iso.roots[0]) != filepath.Clean(want) {
		t.Errorf("副本根 = %q，应为 %q", iso.roots[0], want)
	}
	if filepath.Clean(iso.cwd) != filepath.Clean(want) {
		t.Errorf("规划 cwd = %q，应与副本根一致——否则会照着主工作区规划、往副本里写", iso.cwd)
	}
	// 副本真的是仓库的一个工作目录：看得见已提交的文件
	if _, err := os.Stat(filepath.Join(iso.roots[0], "seed.txt")); err != nil {
		t.Errorf("副本里应当有仓库已提交的文件: %v", err)
	}
	// 主工作区不能多出东西
	if _, err := os.Stat(filepath.Join(f.ws, "worktrees")); !os.IsNotExist(err) {
		t.Errorf("副本不该建在工作区里面")
	}
}

// 同一个任务再来一次（重试 / replay）应当复用那一份，而不是建第二个或报错。
func TestIsolateForTaskReusesExisting(t *testing.T) {
	f := newFixture(t, nil)
	gitRepo(t, f.ws)
	f.a.Cfg.Worktrees.Enabled = true

	first := f.a.isolateForTask(context.Background(), types.GoalRequest{}, "task2", func(string, string, int, string) {})
	second := f.a.isolateForTask(context.Background(), types.GoalRequest{}, "task2", func(string, string, int, string) {})
	if len(second.roots) != 1 || filepath.Clean(second.roots[0]) != filepath.Clean(first.roots[0]) {
		t.Errorf("第二次应当复用同一份副本：first=%v second=%v", first.roots, second.roots)
	}
	if got := len(f.a.WorktreesView()["rows"].([]map[string]any)); got != 1 {
		t.Errorf("记录应当只有一条，实际 %d", got)
	}
}

// 工作区不是仓库：**降级为原地执行**，但必须把原因说出来，而且不许失败。
// 隔离是保险，不是前提——为了"必须隔离"而让任务跑不起来，是拿能不能干活去换干得干不干净。
func TestIsolateForTaskDegradesOutsideRepo(t *testing.T) {
	f := newFixture(t, nil)
	f.a.Cfg.Worktrees.Enabled = true
	f.ws = f.a.Cfg.Workspace // 普通目录（fixture 建的就是普通目录）

	iso := f.a.isolateForTask(context.Background(), types.GoalRequest{}, "task3", noteTo(f.notify))
	if len(iso.roots) != 0 {
		t.Errorf("建不了副本时不应给出边界（会变成空边界=拒绝一切），实际 %v", iso.roots)
	}
	if filepath.Clean(iso.cwd) != filepath.Clean(f.a.Cfg.Workspace) {
		t.Errorf("降级后 cwd 应当是工作区，实际 %q", iso.cwd)
	}
	if !strings.Contains(progressText(f.notify), "worktree") {
		t.Errorf("降级必须说出来，实际进度：\n%s", progressText(f.notify))
	}
	if _, err := os.Stat(filepath.Join(f.a.Cfg.DataDir, "worktrees")); !os.IsNotExist(err) {
		t.Errorf("失败时不该留下副本目录")
	}
}

// 默认关：一个目录都不该建（这是默认值，也是最要紧的一条——它决定升级是否改变既有行为）。
func TestIsolateDisabledCreatesNothing(t *testing.T) {
	f := newFixture(t, nil)
	gitRepo(t, f.ws)

	iso := f.a.isolateForTask(context.Background(), types.GoalRequest{}, "task4", noteTo(f.notify))
	if len(iso.roots) != 0 {
		t.Errorf("开关关着就不该隔离，实际 %v", iso.roots)
	}
	if _, err := os.Stat(filepath.Join(f.a.Cfg.DataDir, "worktrees")); !os.IsNotExist(err) {
		t.Errorf("开关关着时不建任何东西")
	}
	if got := progressText(f.notify); got != "" {
		t.Errorf("没什么可交代的时候不该报进度，实际：\n%s", got)
	}
}

// 调用方点名了工作目录（评测用它把任务钉在 fixture 上）：不套副本。
func TestIsolateSkippedWhenCallerPinsCwd(t *testing.T) {
	f := newFixture(t, nil)
	gitRepo(t, f.ws)
	f.a.Cfg.Worktrees.Enabled = true
	pinned := t.TempDir()

	iso := f.a.isolateForTask(context.Background(),
		types.GoalRequest{Context: map[string]any{"cwd": pinned}}, "task5", func(string, string, int, string) {})
	if len(iso.roots) != 0 || filepath.Clean(iso.cwd) != filepath.Clean(pinned) {
		t.Errorf("点名了 cwd 就不该建副本，实际 %+v", iso)
	}
}

// 边界真的按任务走：同一份 file.write，在副本的 ctx 下落到副本里，
// 而写前快照记的也是副本里的那个路径——不然"还原"会去改另一个文件。
func TestExecutorWritesInsideTaskRoots(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	gitRepo(t, ws)
	wt := filepath.Join(dataDir, "worktrees", "task6")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	r := registry.New()
	file.New(ws).RegisterAll(r)
	e := &Executor{
		Reg: r, Gate: safety.New("auto", nil, nil, []string{ws}, 2*time.Second),
		Notifier: NopNotifier{}, StepTimeout: 5 * time.Second, DataDir: dataDir,
	}
	ctx := toolutil.WithRoots(context.Background(), []string{wt})
	plan := types.Plan{Steps: []types.Step{{
		ID: "s1", Tool: "file.write",
		Args: map[string]any{"path": "out.txt", "content": "写在副本里\n"},
	}}}
	exec := e.Execute(ctx, plan, "task6", string(types.ModeAuto), false)
	if exec.Steps[0].Status != types.StepSucceeded {
		t.Fatalf("步骤应当成功（副本根算信任边界，不该卡审批），实际 %+v", exec.Steps[0])
	}
	if _, err := os.Stat(filepath.Join(wt, "out.txt")); err != nil {
		t.Errorf("文件应当落在副本里: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "out.txt")); !os.IsNotExist(err) {
		t.Errorf("主工作区里不该出现这个文件")
	}
	// 快照必须记副本里的路径：还原要按它回写
	recs, err := taskPreImages(dataDir, "task6")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, rec := range recs {
		if filepath.Clean(rec.Path) == filepath.Clean(filepath.Join(wt, "out.txt")) {
			found = true
		}
	}
	if !found {
		t.Errorf("写前快照应当记副本里的绝对路径，实际 %+v", recs)
	}
}

// 收尾：开了自动删除，干净的就删掉，脏的一律留着并说清楚它在哪。
func TestSettleWorktreeRemovesCleanKeepsDirty(t *testing.T) {
	f := newFixture(t, nil)
	gitRepo(t, f.ws)
	f.a.Cfg.Worktrees.Enabled = true
	f.a.Cfg.Worktrees.AutoDelete = true
	ctx := context.Background()

	// 干净的那一个：跑完就没了
	f.a.isolateForTask(ctx, types.GoalRequest{}, "clean1", noteTo(f.notify))
	f.a.settleWorktree("clean1")
	if _, err := os.Stat(filepath.Join(f.a.Cfg.DataDir, "worktrees", "clean1")); !os.IsNotExist(err) {
		t.Errorf("干净的副本应当被自动删掉，stat err = %v", err)
	}

	// 脏的那一个：必须留着，并且要说出来
	iso := f.a.isolateForTask(ctx, types.GoalRequest{}, "dirty1", noteTo(f.notify))
	if err := os.WriteFile(filepath.Join(iso.roots[0], "wip.txt"), []byte("还没提交\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.a.settleWorktree("dirty1")
	if _, err := os.Stat(iso.roots[0]); err != nil {
		t.Errorf("有未提交改动的副本不能自动删: %v", err)
	}
	if !strings.Contains(progressText(f.notify), "未提交") {
		t.Errorf("留下它的原因必须说出来，实际进度：\n%s", progressText(f.notify))
	}
}

// 数量上限：超出时清最旧的，且**一个脏的都不清**（上限不是"到点就丢东西"的许可）。
func TestSettleWorktreePrunesToLimit(t *testing.T) {
	f := newFixture(t, nil)
	gitRepo(t, f.ws)
	f.a.Cfg.Worktrees.Enabled = true
	f.a.Cfg.Worktrees.AutoDelete = false
	f.a.Cfg.Worktrees.MaxCount = 1
	ctx := context.Background()

	old := f.a.isolateForTask(ctx, types.GoalRequest{}, "old1", noteTo(f.notify))
	// 让 old1 变脏，它就不该被清
	if err := os.WriteFile(filepath.Join(old.roots[0], "wip.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // 让时间戳分得开（排序按创建时间）
	f.a.isolateForTask(ctx, types.GoalRequest{}, "new1", noteTo(f.notify))
	f.a.settleWorktree("new1")

	if len(f.a.WorktreesView()["rows"].([]map[string]any)) != 2 {
		t.Errorf("最旧的那份是脏的，应当两份都留着")
	}
	if !strings.Contains(progressText(f.notify), "未提交") {
		t.Errorf("数量还超着的原因必须说出来：\n%s", progressText(f.notify))
	}
}
