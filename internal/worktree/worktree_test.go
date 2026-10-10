package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo 建一个真的 git 仓库：worktree 是 git 自己的概念，只有真跑 git 才算验过。
// 身份写进仓库本地配置——我们起的是独立 git 进程，测试进程的环境变量对它无效，
// 干净 CI 上没有全局身份时提交会以「Author identity unknown」失败（这个坑真发生过）。
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Gleam Test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return dir
}

func newManager(t *testing.T, repo string) (*Manager, string) {
	t.Helper()
	dataDir := t.TempDir()
	m := &Manager{DataDir: dataDir, BranchPrefix: func() string { return "gleam/" }}
	return m, repo
}

// listWorktrees 让 git 自己回答"它登记了哪些 worktree"，而不是读我们自己的记录——
// 那正是要验的东西的对立面。
func listWorktrees(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	return string(out)
}

func TestCreateListRemove(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	meta, err := m.Create(ctx, "abc123", repo, "")
	if err != nil {
		t.Fatalf("建副本失败: %v", err)
	}
	if meta.Branch != "gleam/abc123" {
		t.Errorf("分支名 = %q，应为 gleam/abc123", meta.Branch)
	}
	if meta.Repo == "" || !filepath.IsAbs(meta.Repo) {
		t.Errorf("元数据里的仓库路径应是绝对路径，实际 %q", meta.Repo)
	}
	if _, err := os.Stat(meta.Path); err != nil {
		t.Fatalf("副本目录没建出来: %v", err)
	}
	// 副本是仓库的一个工作目录：里面看得见主仓库的文件
	if _, err := os.Stat(filepath.Join(meta.Path, "a.txt")); err != nil {
		t.Errorf("副本里应当有仓库已提交的文件: %v", err)
	}
	if !strings.Contains(listWorktrees(t, repo), filepath.ToSlash(m.Dir("abc123"))) &&
		!strings.Contains(listWorktrees(t, repo), m.Dir("abc123")) {
		t.Errorf("git 自己没登记这个 worktree:\n%s", listWorktrees(t, repo))
	}

	all := m.List()
	if len(all) != 1 || all[0].TaskID != "abc123" {
		t.Fatalf("List = %+v，应恰好一条 abc123", all)
	}

	if err := m.Remove(ctx, "abc123", false); err != nil {
		t.Fatalf("删副本失败: %v", err)
	}
	if _, err := os.Stat(m.Dir("abc123")); !os.IsNotExist(err) {
		t.Errorf("副本目录应当已删除，stat err = %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("记录应当一并删掉，List = %+v", m.List())
	}
	// prune 过之后 git 的登记也该干净了
	if strings.Contains(listWorktrees(t, repo), "abc123") {
		t.Errorf("git 里还留着这个 worktree 的登记:\n%s", listWorktrees(t, repo))
	}
}

// 同一个任务重跑（replay / 重试）会拿着同一个 taskID 再来建一次。
// 必须**明确失败**，不能悄悄把上一份顶掉（那等于丢掉一份可能有改动的副本）。
func TestCreateTwiceDoesNotClobber(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	meta, err := m.Create(ctx, "sameTask", repo, "")
	if err != nil {
		t.Fatalf("第一次建失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta.Path, "a.txt"), []byte("副本里改的\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, "sameTask", repo, ""); !errors.Is(err, ErrExists) && err == nil {
		t.Fatalf("第二次建应当失败并说明已存在，实际 err = %v", err)
	}
	// 上一份的内容必须原样还在
	got, err := os.ReadFile(filepath.Join(meta.Path, "a.txt"))
	if err != nil || string(got) != "副本里改的\n" {
		t.Errorf("第二次创建动了已有副本的内容: %q, err = %v", got, err)
	}
}

// 删除的底线：有未提交改动就不自动删。force 才是"我知道会丢"。
func TestDirtyBlocksRemovalUntilForced(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	meta, err := m.Create(ctx, "dirty1", repo, "")
	if err != nil {
		t.Fatalf("建副本失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta.Path, "a.txt"), []byte("没提交的改动\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, detail, err := Dirty(ctx, meta.Path)
	if err != nil || !dirty {
		t.Fatalf("应当判为脏，dirty=%v detail=%q err=%v", dirty, detail, err)
	}
	if err := m.Remove(ctx, "dirty1", false); !errors.Is(err, ErrNeedsForce) {
		t.Fatalf("脏副本必须拒绝删除，实际 err = %v", err)
	}
	if _, err := os.Stat(meta.Path); err != nil {
		t.Errorf("被拒绝之后目录必须还在: %v", err)
	}
	if err := m.Remove(ctx, "dirty1", true); err != nil {
		t.Fatalf("force 删除应当成功: %v", err)
	}
}

// 未跟踪的新文件也算脏：那是"你还没纳入版本控制的东西"，一样不该被静默回收。
func TestUntrackedCountsAsDirty(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	meta, err := m.Create(ctx, "newfile", repo, "")
	if err != nil {
		t.Fatalf("建副本失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(meta.Path, "brand-new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, detail, err := Dirty(ctx, meta.Path)
	if err != nil || !dirty {
		t.Fatalf("新文件应当判为脏，dirty=%v detail=%q err=%v", dirty, detail, err)
	}
	if !strings.Contains(detail, "新文件") {
		t.Errorf("说明里应当区分出未跟踪的新文件，实际 %q", detail)
	}
}

// 副本被外部删掉（用户手删、清理工具）：删除操作视为已达成，并把 git 的登记收敛掉。
func TestRemoveAfterExternalDeletion(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	if _, err := m.Create(ctx, "gone", repo, ""); err != nil {
		t.Fatalf("建副本失败: %v", err)
	}
	if err := os.RemoveAll(m.Dir("gone")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "gone", false); err != nil {
		t.Fatalf("目录已不在时应视为已达成（主仓库还在，能判断干净），实际 err = %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("记录应当清掉，List = %+v", m.List())
	}
	if strings.Contains(listWorktrees(t, repo), "gone") {
		t.Errorf("git 的登记没收敛:\n%s", listWorktrees(t, repo))
	}
}

// 主仓库被删掉：git 已经无从判断副本干不干净，于是**也不许不问就删**——
// 那会把"我不知道有没有改动"当成"没关系"。给一句确认之后，目录仍由 Gleam 清掉。
func TestRemoveWhenRepoGone(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	if _, err := m.Create(ctx, "orphan", repo, ""); err != nil {
		t.Fatalf("建副本失败: %v", err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	err := m.Remove(ctx, "orphan", false)
	if !errors.Is(err, ErrNeedsForce) {
		t.Fatalf("主仓库不在时应当要求确认后才删，实际 err = %v", err)
	}
	if !strings.Contains(err.Error(), "主仓库") {
		t.Errorf("错误里要说明主仓库已不在，实际 %q", err.Error())
	}
	if _, statErr := os.Stat(m.Dir("orphan")); statErr != nil {
		t.Errorf("被拒绝之后目录必须还在: %v", statErr)
	}
	if err := m.Remove(ctx, "orphan", true); err != nil {
		t.Fatalf("确认之后应当能删掉: %v", err)
	}
	if _, statErr := os.Stat(m.Dir("orphan")); !os.IsNotExist(statErr) {
		t.Errorf("目录是我们自己建的，应当能清掉，stat err = %v", statErr)
	}
	if len(m.List()) != 0 {
		t.Errorf("记录应当清掉，List = %+v", m.List())
	}
}

// 不是仓库：建不了副本，而且原因要说清楚（调用方据此降级为原地执行）。
func TestCreateOutsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git")
	}
	dataDir := t.TempDir()
	m := &Manager{DataDir: dataDir}
	plain := dirOutsideRepo(t) // 刻意不 git init，且必须在仓库之外
	if _, err := m.Create(context.Background(), "norepo", plain, ""); err == nil {
		t.Fatal("不是仓库时必须失败")
	} else if !strings.Contains(err.Error(), "仓库") {
		t.Errorf("错误里要说明「不在仓库里」，实际 %q", err.Error())
	}
	if _, err := os.Stat(m.Dir("norepo")); !os.IsNotExist(err) {
		t.Errorf("失败时不该留下目录，stat err = %v", err)
	}
}

// 上限裁剪：只清最早且干净的；脏的一个都不清——上限不是"到点就丢东西"的许可。
func TestPruneCleanSkipsDirty(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	// 按创建时间排序要可预期：借元数据里的时间戳区分先后不太好控，
	// 所以按顺序建三个，再把中间那个弄脏。
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, err := m.Create(ctx, id, repo, ""); err != nil {
			t.Fatalf("建 %s 失败: %v", id, err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.Dir("t2"), "wip.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, keptDirty := m.PruneClean(ctx, 1)
	if len(removed) == 0 {
		t.Fatalf("上限 1 而存在 3 个，应当清掉一些；removed=%v keptDirty=%v", removed, keptDirty)
	}
	for _, r := range removed {
		if r.TaskID == "t2" {
			t.Errorf("脏的那份不该被清掉")
		}
	}
	if _, err := os.Stat(m.Dir("t2")); err != nil {
		t.Errorf("脏副本必须留着: %v", err)
	}
	// 新的一份一定在
	if _, err := os.Stat(m.Dir("t3")); err != nil {
		t.Errorf("最新的一份必须留着: %v", err)
	}
	if len(keptDirty) != 1 || keptDirty[0].TaskID != "t2" {
		t.Errorf("因为脏而留下的应当是 t2，实际 %+v", keptDirty)
	}
}

// 上限为 0 表示不限制：一个都不删（这是默认值，别把"关掉裁剪"实现成"只留 0 个"）。
func TestPruneCleanZeroMeansUnlimited(t *testing.T) {
	repo := newRepo(t)
	m, _ := newManager(t, repo)
	ctx := context.Background()

	for _, id := range []string{"t1", "t2"} {
		if _, err := m.Create(ctx, id, repo, ""); err != nil {
			t.Fatalf("建 %s 失败: %v", id, err)
		}
	}
	removed, _ := m.PruneClean(ctx, 0)
	if len(removed) != 0 || len(m.List()) != 2 {
		t.Errorf("上限 0 应当不限制，removed=%v 剩余 %d", removed, len(m.List()))
	}
}

// 任务 ID 会直接当目录名用：带分隔符或 .. 的一律拒，别让它在数据目录之外建东西。
func TestTaskIDGuard(t *testing.T) {
	bad := []string{"", ".", "..", "a/b", `a\b`, "../x", "a..b"}
	for _, s := range bad {
		if validTaskID(s) {
			t.Errorf("%q 不该被当作合法任务 ID", s)
		}
	}
	for _, s := range []string{"abc123", "A-b_c9"} {
		if !validTaskID(s) {
			t.Errorf("%q 应当合法", s)
		}
	}
}

// 远端主机名的解析只从**本机配置**的地址里取，不出网。
func TestHostOfURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:org/repo.git":          "github.com",
		"https://github.com/org/repo.git":      "github.com",
		"ssh://git@gitlab.example.com:2222/x":  "gitlab.example.com:2222",
		"https://user:pw@example.com/org/repo": "example.com",
		"":                                     "",
		"/local/path":                          "",
	}
	for in, want := range cases {
		if got := hostOfURL(in); got != want {
			t.Errorf("hostOfURL(%q) = %q，应为 %q", in, got, want)
		}
	}
}

// fetch 出网要记一笔：不然「创建前先 fetch」就是个看不见流量的开关。
func TestFetchRecordsEgress(t *testing.T) {
	repo := newRepo(t)
	dataDir := t.TempDir()
	var got int
	m := &Manager{
		DataDir: dataDir,
		Fetch:   func() bool { return true },
		Egress:  func(host string, n int) { got++ },
	}
	// 没有远端，fetch 一定失败——这正是要验的：失败也要留痕（它确实尝试连了）。
	if _, err := m.Create(context.Background(), "f1", repo, ""); err != nil {
		t.Fatalf("fetch 失败不该阻断建副本: %v", err)
	}
	if got != 1 {
		t.Errorf("fetch 尝试了就该记一笔，实际记了 %d 次", got)
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
