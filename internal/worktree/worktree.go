// Package worktree 管理「每个任务一个 git worktree」的生命周期：建 / 列 / 查 / 删 / 裁剪。
//
// 为什么单独一个包，而不是塞进 tools/git 或 agent：
//   - tools/git 里那三个工具是**模型可以调的**（建分支、提交、推送）；worktree 是引擎行为，
//     模型不该有机会自己去建一个任务目录；
//   - agent 已经背着规划/执行/反思，而"任务在哪个目录里跑"是另一件事，有它自己的失败模式
//     （仓库没了、目录被外部删了、有未提交改动），值得一处集中处置。
//
// 它复用 tools/git 的命令执行（argv 分离、不过 shell、统一超时与错误携带），不自己再写一份。
//
// 位置：worktree 落在 <数据目录>/worktrees/<任务ID>，元数据是同级的 <任务ID>.meta.json。
// **刻意不放进工作区**：放进去就等于在用户仓库里凭空多出一个目录，`git status` 看得见、
// 任务还可能顺手 `git add` 进去；而它本来就该是"不属于这个仓库的东西"。
//
// 元数据里记着主仓库路径与分支名，因为**列出/删除都离不开它们**：只凭 worktree 目录本身，
// 无法知道该去哪条仓库上执行 `worktree remove`，也无法知道它对应哪条分支。
package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/atomicfile"
	gitcmd "gleam/internal/tools/git"
)

// Meta 一条由 Gleam 管理的 worktree。
type Meta struct {
	TaskID    string `json:"task_id"`
	Repo      string `json:"repo"`     // 主仓库的绝对路径（执行 worktree 命令的地方）
	Path      string `json:"path"`     // worktree 的绝对路径（任务文件的真实落点）
	Branch    string `json:"branch"`   // 建出来的分支名
	BaseRef   string `json:"base_ref"` // 从哪个起点建的
	CreatedAt string `json:"created_at"`
}

// Manager 生命周期 owner。零值不可用，必须给 DataDir。
type Manager struct {
	DataDir string
	// BranchPrefix 每次现取：用户改了前缀，下一个任务就该按新的建。
	BranchPrefix func() string
	// Fetch 每次现取：创建前要不要先同步远端（会出网）。
	Fetch func() bool
	// Egress 可选：fetch 真的发出去时记一笔（kind 由调用方定，见 agent 的 git.remote 台账行）。
	// 记的是**主机名**，不记 URL 全量；拿不到主机名就传空串。
	Egress func(host string, nbytes int)
}

// Root worktree 的根目录。未配数据目录时返回空串（调用方据此拒绝建）。
func (m *Manager) Root() string {
	if strings.TrimSpace(m.DataDir) == "" {
		return ""
	}
	return filepath.Join(m.DataDir, "worktrees")
}

// Dir 某个任务的 worktree 路径。
func (m *Manager) Dir(taskID string) string { return filepath.Join(m.Root(), taskID) }

func (m *Manager) metaPath(taskID string) string {
	return filepath.Join(m.Root(), taskID+".meta.json")
}

// ErrNoDataDir 没有数据目录时建不了（也不该建）。
var ErrNoDataDir = fmt.Errorf("没有数据目录，无法管理 worktree")

// ErrExists 目标已经存在：**不覆盖、不 -B**。同名要么是重跑同一个任务，要么是撞了 ID，
// 两种都该让人看见，而不是悄悄把旧的那份顶掉。
var ErrExists = fmt.Errorf("该任务的 worktree 已经存在")

// ErrNeedsForce 这次删除需要用户明确点一次「确认丢弃」才会执行。
//
// 两种情形都归到它：副本里有未提交的改动，或者**读不出**它干不干净
// （主仓库被删了、git 不在 PATH 上——`git status` 也要读主仓库的元数据）。
// 后一种照样拒绝：不知道有没有改动时就删，等于把"我不知道"当成"没关系"。
var ErrNeedsForce = errors.New("这个副本需要确认才能删除")

// validTaskID 任务 ID 会直接当一个目录名与一段分支名，所以只接受"一个安全的路径段"。
// 任务 ID 由 types.NewID 生成（16 位十六进制），这里的判据比它宽一点但不放行任何分隔符与 ..
func validTaskID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// IsRepo 目录是不是在一个 git 仓库里（git 自己会往上找）。
func IsRepo(ctx context.Context, dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	_, err := gitcmd.Run(ctx, dir, "rev-parse", "--git-dir")
	return err == nil
}

// RepoRoot 取仓库根（worktree 命令要在主仓库上跑）。不在仓库里返回错误。
func RepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := gitcmd.Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("不在 git 仓库里：%w", err)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("拿不到仓库根目录")
	}
	return root, nil
}

// Create 为任务建一个 worktree 并落一条元数据。
//
// repo 是工作区（会被 git 自己收敛到仓库根）。baseRef 传空表示从当前 HEAD 建。
// 失败一律返回错误、不留下半个状态：调用方据此**降级为在工作区里直接执行**，
// 而不是让任务跑不起来（隔离是保险，不是前提——同写前快照的取舍）。
func (m *Manager) Create(ctx context.Context, taskID, repo, baseRef string) (Meta, error) {
	if m.Root() == "" {
		return Meta{}, ErrNoDataDir
	}
	if !validTaskID(taskID) {
		return Meta{}, fmt.Errorf("任务 ID %q 不能当目录名用", taskID)
	}
	root, err := RepoRoot(ctx, repo)
	if err != nil {
		return Meta{}, err
	}
	path := m.Dir(taskID)
	branch := m.branchFor(taskID)
	if !gitcmd.ValidRefName(branch) {
		return Meta{}, fmt.Errorf("分支名 %q 不合法", branch)
	}
	if _, err := os.Stat(path); err == nil {
		return Meta{}, fmt.Errorf("%w：%s", ErrExists, path)
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if !gitcmd.ValidRefName(baseRef) {
		return Meta{}, fmt.Errorf("起点 %q 不合法", baseRef)
	}
	if err := os.MkdirAll(m.Root(), 0o755); err != nil {
		return Meta{}, err
	}
	if m.Fetch != nil && m.Fetch() {
		if err := m.fetch(ctx, root); err != nil {
			// fetch 失败不阻断建 worktree：本地 HEAD 仍然是个有效的起点，
			// 而"同步不到远端"是常有的事（离线、没配远端、凭证过期）。
			// 但要说出来——静默忽略会让"创建前先 fetch"这个开关变成一个摆设。
			fmt.Fprintf(os.Stderr, "[gleam] worktree 创建前 fetch 失败（继续用本地 HEAD）：%v\n", err)
		}
	}
	if _, err := gitcmd.Run(ctx, root, "worktree", "add", "-b", branch, path, baseRef); err != nil {
		return Meta{}, err
	}
	meta := Meta{
		TaskID: taskID, Repo: root, Path: path, Branch: branch, BaseRef: baseRef,
		// 纳秒精度：同一秒内连建几个（跑测试、或连着提几个任务）要能排出先后，
		// "最旧的先被清掉"这条规则才有确定的落点。
		CreatedAt: time.Now().Format(time.RFC3339Nano),
	}
	if err := m.save(meta); err != nil {
		// 记录写不进去 = 这个 worktree 之后没人能列出来、也删不干净。
		// 与其留一个"存在但不可见"的目录，不如当场撤掉它。
		_, _ = gitcmd.Run(ctx, root, "worktree", "remove", "--force", path)
		return Meta{}, err
	}
	return meta, nil
}

// fetch 同步远端。只记主机与字节（不记 URL 全量）——与门控里其他出网留痕同一口径。
func (m *Manager) fetch(ctx context.Context, root string) error {
	if m.Egress != nil {
		host := remoteHost(ctx, root)
		defer func() { m.Egress(host, 0) }()
	}
	_, err := gitcmd.Run(ctx, root, "fetch", "--prune")
	return err
}

// remoteHost 从**本地**配置里取远端主机名（不出网）：`git remote get-url origin`。
// 拿不到就返回空串——台账那一行会显示"主机名未知"，而不是编一个。
func remoteHost(ctx context.Context, root string) string {
	out, err := gitcmd.Run(ctx, root, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return hostOfURL(strings.TrimSpace(out))
}

// hostOfURL 从 `git@github.com:o/r.git`、`https://github.com/o/r.git` 这类地址里取主机。
//
// 只认这三类远端 URL 的写法。没有 scheme 也没有冒号的（`/srv/repo`、`../repo`）是**本地远端**，
// 那种情况下本来就没有主机可言——返回空串，台账那一格会显示"主机名未知"，
// 而不是把一个本地路径印成"你连了这里"。
func hostOfURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexByte(s, '/'); j >= 0 {
			s = s[:j]
		}
		if i := strings.LastIndexByte(s, '@'); i >= 0 {
			s = s[i+1:]
		}
		return s
	}
	// scp 风格 user@host:path。没有冒号 = 本地路径，不是远端。
	host, _, found := strings.Cut(s, ":")
	if !found {
		return ""
	}
	if i := strings.LastIndexByte(host, '@'); i >= 0 {
		host = host[i+1:]
	}
	return host
}

func (m *Manager) branchFor(taskID string) string {
	prefix := ""
	if m.BranchPrefix != nil {
		prefix = strings.TrimSpace(m.BranchPrefix())
	}
	if prefix == "" {
		return taskID
	}
	return prefix + taskID
}

func (m *Manager) save(meta Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(m.metaPath(meta.TaskID), data, 0o644)
}

// Get 读一个任务的元数据。目录被删了但留了记录也算存在——删除要靠这条记录。
func (m *Manager) Get(taskID string) (Meta, bool) {
	if m.Root() == "" || !validTaskID(taskID) {
		return Meta{}, false
	}
	data, err := os.ReadFile(m.metaPath(taskID))
	if err != nil {
		return Meta{}, false
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, false
	}
	return meta, true
}

// List 列出全部由 Gleam 管理的 worktree，新的在前。
//
// 排序按创建时间：数量上限要删的是**最旧**的，界面上人看的顺序也是"最近的在上面"。
func (m *Manager) List() []Meta {
	if m.Root() == "" {
		return nil
	}
	entries, err := filepath.Glob(filepath.Join(m.Root(), "*.meta.json"))
	if err != nil {
		return nil
	}
	out := make([]Meta, 0, len(entries))
	for _, p := range entries {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var meta Meta
		if err := json.Unmarshal(data, &meta); err != nil || meta.TaskID == "" {
			continue
		}
		out = append(out, meta)
	}
	sort.SliceStable(out, func(i, j int) bool { return metaTime(out[i]).After(metaTime(out[j])) })
	return out
}

// metaTime 解析创建时间。解析不了就归零（沉到列表末尾）：
// 那种记录多半是被手改过或来自更早的版本，让它排在新建的那些中间只会让"最旧的先清"失去依据。
func metaTime(m Meta) time.Time {
	t, err := time.Parse(time.RFC3339Nano, m.CreatedAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Dirty 判 worktree 里是否有未提交或未跟踪的改动，并给一句人话说明。
//
// 删除的底线：**有未提交改动就不自动删**。自动删掉它等于把用户还没看过的改动丢了，
// 而"worktree 会被回收"这件事用户不一定记得。
func Dirty(ctx context.Context, path string) (bool, string, error) {
	out, err := gitcmd.Run(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, "", err
	}
	var changed, untracked int
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked++
		} else {
			changed++
		}
	}
	if changed == 0 && untracked == 0 {
		return false, "", nil
	}
	parts := make([]string, 0, 2)
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%d 个文件有未提交的改动", changed))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d 个新文件还没纳入版本控制", untracked))
	}
	return true, strings.Join(parts, "，"), nil
}

// Remove 删掉一个任务的 worktree。
//
// force=false 且里面有未提交改动时**拒绝删除**，由调用方去问用户要一句"确认丢弃"。
// 目录已经不在（被外部删了）时视为已达成：git 的内部登记用 prune 收敛，不算失败。
// 主仓库不在了也不阻死：只删目录，并如实说明 git 那边没能更新。
func (m *Manager) Remove(ctx context.Context, taskID string, force bool) error {
	meta, ok := m.Get(taskID)
	if !ok {
		return fmt.Errorf("没有 %s 的 worktree 记录", taskID)
	}
	if _, err := os.Stat(meta.Path); os.IsNotExist(err) {
		m.forget(ctx, meta)
		return nil
	}
	repoGone := false
	if _, err := os.Stat(meta.Repo); os.IsNotExist(err) {
		repoGone = true
	}
	if !force {
		if repoGone {
			return fmt.Errorf("%w：主仓库 %s 已不在，没法判断副本里有没有未提交的改动", ErrNeedsForce, meta.Repo)
		}
		dirty, detail, err := Dirty(ctx, meta.Path)
		if err != nil {
			return fmt.Errorf("%w：读不出副本的状态（%v）", ErrNeedsForce, err)
		}
		if dirty {
			return fmt.Errorf("%w：%s", ErrNeedsForce, detail)
		}
	}
	if repoGone {
		// 主仓库没了，git 已经无从下手；目录是 Gleam 自己建的，我们仍能清理它。
		if err := os.RemoveAll(meta.Path); err != nil {
			return err
		}
		_ = os.Remove(m.metaPath(taskID))
		return nil
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, meta.Path)
	if _, err := gitcmd.Run(ctx, meta.Repo, args...); err != nil {
		// git 删不掉但目录真在：不强行 os.RemoveAll——那会留下 git 里一条指向空目录的登记。
		return err
	}
	_, _ = gitcmd.Run(ctx, meta.Repo, "worktree", "prune")
	_ = os.Remove(m.metaPath(taskID))
	return nil
}

// forget 目录已不在时收尾：只收敛 git 登记与元数据。
func (m *Manager) forget(ctx context.Context, meta Meta) {
	if _, err := os.Stat(meta.Repo); err == nil {
		_, _ = gitcmd.Run(ctx, meta.Repo, "worktree", "prune")
	}
	_ = os.Remove(m.metaPath(meta.TaskID))
}

// PruneClean 把数量收敛到 keep（keep<=0 表示不限制），先删最旧且干净的。
//
// 脏的一律跳过——包括"最旧但脏"的那条，于是实际数量可能停在上限之上。
// 这是刻意的：上限是"别把盘占满"的兜底，不是"到点就丢东西"的许可。
// 返回删掉的、以及因为脏而留下的（让界面能说清为什么数量还超着）。
func (m *Manager) PruneClean(ctx context.Context, keep int) (removed []Meta, keptDirty []Meta) {
	if keep <= 0 {
		return nil, nil
	}
	all := m.List() // 新的在前
	if len(all) <= keep {
		return nil, nil
	}
	for _, meta := range all[keep:] {
		if err := m.Remove(ctx, meta.TaskID, false); err != nil {
			keptDirty = append(keptDirty, meta)
			continue
		}
		removed = append(removed, meta)
	}
	return removed, keptDirty
}
