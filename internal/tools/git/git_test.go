package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsValidRefName(t *testing.T) {
	ok := []string{"feature-x", "修复登录", "a/b/c", "v1.0"}
	bad := []string{"", "-x", ".hidden", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a..b", "a/", "a\\b"}
	for _, s := range ok {
		if !isValidRefName(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range bad {
		if isValidRefName(s) {
			t.Errorf("%q 应被拒绝", s)
		}
	}
}

func TestWithPrefix(t *testing.T) {
	c := Config{BranchPrefix: "gleam/"}
	if got := c.withPrefix("fix"); got != "gleam/fix" {
		t.Errorf("got %q", got)
	}
	if got := c.withPrefix("gleam/fix"); got != "gleam/fix" {
		t.Errorf("已有前缀不该重复加，got %q", got)
	}
	if got := (Config{}).withPrefix("fix"); got != "fix" {
		t.Errorf("没配前缀应原样，got %q", got)
	}
}

// 端到端：真建一个仓库，走一遍分支 → 提交。
// 跳过没有 git 的环境（CI 里应当有；没有就明说跳过，而不是假装通过）。
func TestBranchAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	// 身份写进仓库本地配置：工具自己起 git 进程（见 git.go 的 run），测试进程的环境变量
	// 与它无关；干净 CI 上没有全局身份，提交会以「Author identity unknown」失败——真发生过。
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Gleam Test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{BranchPrefix: "gleam/", Roots: []string{dir}}
	prov := func() Config { return cfg } // 闭包读当前值：改了 cfg 下一次动作就生效
	ctx := context.Background()

	out, err := NewBranch(prov).Execute(ctx, map[string]any{"name": "fix-thing"})
	if err != nil {
		t.Fatalf("建分支失败: %v", err)
	}
	if got := out.(map[string]any)["branch"]; got != "gleam/fix-thing" {
		t.Errorf("分支名 = %v，应带前缀", got)
	}

	out2, err := NewCommit(prov).Execute(ctx, map[string]any{"message": "feat: 初始化"})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if committed, _ := out2.(map[string]any)["committed"].(bool); !committed {
		t.Errorf("应提交成功: %v", out2)
	}

	// 再提交一次：没有改动可提交，要给一句人话而不是报错
	out3, err := NewCommit(prov).Execute(ctx, map[string]any{"message": "空提交"})
	if err != nil {
		t.Fatalf("空提交不该报错: %v", err)
	}
	if committed, _ := out3.(map[string]any)["committed"].(bool); committed {
		t.Errorf("没有改动时不该产生提交: %v", out3)
	}

	// 工作区外的目录必须被拒
	if _, err := NewBranch(prov).Execute(ctx, map[string]any{"name": "x", "dir": filepath.Dir(dir)}); err == nil {
		t.Error("工作区外的目录应被拒绝")
	}
}

// 推送：这里只验「强制推送时带不带 --force-with-lease」这条规矩，
// 不真连远端——没有远端也不该让这个判断失真。
func TestPushArgvForceWithLease(t *testing.T) {
	// 用一个假远端目录当 origin，就能在不联网的前提下走完整条推送链路
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("环境里没有 git")
	}
	work := t.TempDir()
	bare := t.TempDir()
	mk := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mk(bare, "init", "--bare")
	mk(work, "init")
	// 同上：身份落在仓库本地配置里，别依赖 runner 的全局配置。
	mk(work, "config", "user.email", "test@example.invalid")
	mk(work, "config", "user.name", "Gleam Test")
	mk(work, "remote", "add", "origin", bare)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := Config{Roots: []string{work}, ForcePush: true}
	prov := func() Config { return cfg }
	if _, err := NewCommit(prov).Execute(ctx, map[string]any{"message": "init"}); err != nil {
		t.Fatal(err)
	}
	out, err := NewPush(prov).Execute(ctx, map[string]any{"set_upstream": true})
	if err != nil {
		t.Fatalf("推送失败: %v", err)
	}
	if f, _ := out.(map[string]any)["force_with_lease"].(bool); !f {
		t.Error("开了强制推送，结果里应标出用了 --force-with-lease")
	}

	// 关掉之后不该再带
	cfg.ForcePush = false
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCommit(prov).Execute(ctx, map[string]any{"message": "second"}); err != nil {
		t.Fatal(err)
	}
	out2, err := NewPush(prov).Execute(ctx, nil)
	if err != nil {
		t.Fatalf("推送失败: %v", err)
	}
	if f, _ := out2.(map[string]any)["force_with_lease"].(bool); f {
		t.Error("关了强制推送还带 --force-with-lease")
	}
}
