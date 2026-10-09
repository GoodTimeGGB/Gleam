// Package git 提供 Gleam 自己的 git 动作：建分支 / 提交 / 推送。
//
// 为什么不让模型自己拼 shell 命令：这三件事各有一条**必须稳定**的规矩——分支要带前缀、
// 推送要用 --force-with-lease、提交说明要按用户写下的写法要求。交给模型自由发挥，
// 规矩就是时有时无；做成工具，规矩留在代码里，模型只填变量那部分。
//
// 两条安全边界：
//  1. **不走 shell**：参数一律作为 argv 传给 git，不做字符串拼接。所以提交说明里带
//     `$(...)` 也只是一段普通文本，不存在命令注入。
//  2. **只认工作区里的仓库**：cwd 走与文件工具同一套 ResolveInRoots。
//     权限仍由门控逐条裁决（推送是写远端，最重）。
//
// 环境变量**不过滤**：git 正是那个合法需要凭证的进程（SSH agent、credential helper），
// 把它自己的凭证从它手里拿走，等于让推送永远失败。这与 shell.exec 的取舍不同——
// 那边模型能借环境读到凭证，这边不能。
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// Config 三个用户偏好 + 工作区边界。零值可用（前缀空 = 不加）。
type Config struct {
	BranchPrefix       string   // 由 Gleam 创建的分支统一加这个前缀
	ForcePush          bool     // 推送时用 --force-with-lease
	CommitInstructions string   // 生成提交说明时的写法要求（注入任务指引，不在这里拼）
	Roots              []string // 允许操作的工作区根
}

// Provider 每次动作时现取配置。不缓存快照：分支前缀、强制推送这些开关
// 用户改完应当下一次动作就生效，而不是等重启。
type Provider func() Config

const defaultTimeout = 60 * time.Second

// run 执行一条 git 命令。参数是分离的 argv，永不经过 shell。
func (c Config) run(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(cctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// 把 stdout 也带上：git 有些失败只把话说在标准输出里
		if out != "" {
			msg = out + "\n" + msg
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

// resolveDir 把工作目录收敛到工作区内。
func (c Config) resolveDir(raw string) (string, error) {
	if strings.TrimSpace(c.Roots[0]) == "" && strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("没有工作区，也没有指定目录：git 动作需要一个仓库")
	}
	base := strings.TrimSpace(raw)
	if base == "" {
		base = c.Roots[0]
	}
	dir, err := toolutil.ResolveInRoots(base, c.Roots)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// isRepo 判定目录是不是 git 仓库根或其下（git 自己会往上找）。
func (c Config) isRepo(ctx context.Context, dir string) error {
	if _, err := c.run(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("%s 不在 git 仓库里：%v", dir, err)
	}
	return nil
}

// isValidRefName 拦掉注定失败、或会被 git 当成选项的名字。
// 允许中文与常见符号（git 支持），只挡住空白、控制字符、前导 - 与 git 明确禁止的字符。
func isValidRefName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		switch r {
		case ' ', '\t', '\n', '~', '^', ':', '?', '*', '[', '\\':
			return false
		}
		if r < 0x20 {
			return false
		}
	}
	return !strings.Contains(name, "..") && !strings.HasSuffix(name, "/")
}

// withPrefix 给分支名加上配置的前缀。已经有了就不重复加。
func (c Config) withPrefix(name string) string {
	p := strings.TrimSpace(c.BranchPrefix)
	if p == "" || strings.HasPrefix(name, p) {
		return name
	}
	return p + name
}

// ---------- git.branch ----------

type branchTool struct{ p Provider }

func NewBranch(p Provider) types.Tool { return &branchTool{p} }

func (t *branchTool) Name() string { return "git.branch" }
func (t *branchTool) Description() string {
	return "在工作区仓库里新建并切出一个分支（自动带上配置的分支前缀）。已存在则直接切过去，不重置它。"
}
func (t *branchTool) Permission() types.Permission { return types.PermissionUserApproved }
func (t *branchTool) Schema() map[string]any {
	return toolutil.Schema("新建并切换到分支", []string{"name"}, map[string]any{
		"name": toolutil.SchemaProp("分支名（不含前缀，工具会自动加）", "string"),
		"from": toolutil.SchemaProp("从哪个起点建（可选，默认当前 HEAD）", "string"),
		"dir":  toolutil.SchemaProp("仓库目录（可选，须在工作区内）", "string"),
	})
}

func (t *branchTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	c := t.p()
	raw, err := toolutil.RequireStr(args, "name")
	if err != nil {
		return nil, err
	}
	name := c.withPrefix(strings.TrimSpace(raw))
	if !isValidRefName(name) {
		return nil, fmt.Errorf("分支名 %q 不合法", name)
	}
	dir, err := c.resolveDir(toolutil.Str(args, "dir"))
	if err != nil {
		return nil, err
	}
	if err := c.isRepo(ctx, dir); err != nil {
		return nil, err
	}

	from := strings.TrimSpace(toolutil.Str(args, "from"))
	if from != "" && !isValidRefName(from) {
		return nil, fmt.Errorf("起点 %q 不合法", from)
	}

	// 已存在就切过去，不 -B：-B 会把那条分支上的提交抹掉，那是破坏性的
	if _, err := c.run(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		if _, err := c.run(ctx, dir, "checkout", name); err != nil {
			return nil, err
		}
		return map[string]any{"branch": name, "created": false, "switched": true}, nil
	}
	argv := []string{"checkout", "-b", name}
	if from != "" {
		argv = append(argv, from)
	}
	if _, err := c.run(ctx, dir, argv...); err != nil {
		return nil, err
	}
	return map[string]any{"branch": name, "created": true, "switched": true, "from": from}, nil
}

// ---------- git.commit ----------

type commitTool struct{ p Provider }

func NewCommit(p Provider) types.Tool { return &commitTool{p} }

func (t *commitTool) Name() string { return "git.commit" }
func (t *commitTool) Description() string {
	return "把工作区的改动提交到当前分支。说明由你按配置的写法要求写好传进来（工具不会替你生成）。"
}
func (t *commitTool) Permission() types.Permission { return types.PermissionUserApproved }
func (t *commitTool) Schema() map[string]any {
	return toolutil.Schema("提交改动", []string{"message"}, map[string]any{
		"message": toolutil.SchemaProp("提交说明", "string"),
		"paths":   toolutil.SchemaProp("只提交这些路径（可选，默认全部改动）", "array"),
		"dir":     toolutil.SchemaProp("仓库目录（可选，须在工作区内）", "string"),
	})
}

func (t *commitTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	c := t.p()
	msg, err := toolutil.RequireStr(args, "message")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(msg) == "" {
		return nil, fmt.Errorf("提交说明不能为空")
	}
	dir, err := c.resolveDir(toolutil.Str(args, "dir"))
	if err != nil {
		return nil, err
	}
	if err := c.isRepo(ctx, dir); err != nil {
		return nil, err
	}

	var paths []string
	if arr, ok := args["paths"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				paths = append(paths, strings.TrimSpace(s))
			}
		}
	}
	addArgv := []string{"add", "-A"}
	if len(paths) > 0 {
		// 显式列出路径：git 把 -- 之后的都当路径，模型传 "-x" 也只会被当成路径名（然后报错）
		addArgv = append([]string{"add", "--"}, paths...)
	}
	if _, err := c.run(ctx, dir, addArgv...); err != nil {
		return nil, err
	}
	// 没有暂存内容时给一句人话，而不是把 git 的退出码原样抛出去
	if out, err := c.run(ctx, dir, "diff", "--cached", "--name-only"); err == nil && strings.TrimSpace(out) == "" {
		return map[string]any{"committed": false, "reason": "没有已暂存的改动，无需提交"}, nil
	}
	if _, err := c.run(ctx, dir, "commit", "-m", msg); err != nil {
		return nil, err
	}
	sha, _ := c.run(ctx, dir, "rev-parse", "--short", "HEAD")
	return map[string]any{"committed": true, "commit": sha}, nil
}

// ---------- git.push ----------

type pushTool struct{ p Provider }

func NewPush(p Provider) types.Tool { return &pushTool{p} }

func (t *pushTool) Name() string { return "git.push" }
func (t *pushTool) Description() string {
	return "把当前分支推到远端。配置里开了「始终强制推送」时会带 --force-with-lease（比裸 --force 安全：远端有新提交就拒绝）。"
}
func (t *pushTool) Permission() types.Permission { return types.PermissionFullAccess }
func (t *pushTool) Schema() map[string]any {
	return toolutil.Schema("推送到远端", nil, map[string]any{
		"remote":       toolutil.SchemaProp("远端名（默认 origin）", "string"),
		"branch":       toolutil.SchemaProp("分支名（默认当前分支）", "string"),
		"set_upstream": toolutil.SchemaProp("同时建立上游跟踪（新分支首次推送用）", "boolean"),
		"dir":          toolutil.SchemaProp("仓库目录（可选，须在工作区内）", "string"),
	})
}

func (t *pushTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	c := t.p()
	dir, err := c.resolveDir(toolutil.Str(args, "dir"))
	if err != nil {
		return nil, err
	}
	if err := c.isRepo(ctx, dir); err != nil {
		return nil, err
	}
	remote := strings.TrimSpace(toolutil.Str(args, "remote"))
	if remote == "" {
		remote = "origin"
	}
	if !isValidRefName(remote) {
		return nil, fmt.Errorf("远端名 %q 不合法", remote)
	}
	branch := strings.TrimSpace(toolutil.Str(args, "branch"))
	if branch == "" {
		if branch, err = c.run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
			return nil, err
		}
	}
	if branch == "HEAD" {
		return nil, fmt.Errorf("当前是游离 HEAD，推之前先切到一个分支")
	}
	if !isValidRefName(branch) {
		return nil, fmt.Errorf("分支名 %q 不合法", branch)
	}

	argv := []string{"push"}
	forced := false
	if c.ForcePush {
		argv = append(argv, "--force-with-lease")
		forced = true
	}
	if toolutil.Bool(args, "set_upstream") {
		argv = append(argv, "-u")
	}
	argv = append(argv, remote, branch)

	if _, err := c.run(ctx, dir, argv...); err != nil {
		return nil, err
	}
	return map[string]any{"pushed": true, "remote": remote, "branch": branch, "force_with_lease": forced}, nil
}
