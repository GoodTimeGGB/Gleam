// isolation.go 决定「这次任务在哪跑」，以及跑完之后它的副本怎么办。
//
// 名词先对齐：worktree 是 git 的「同一仓库的第二个工作目录」。Gleam 为每个任务建一个，
// 任务在它里面读文件、写文件、跑命令；主工作区全程不动。这样两个任务同时开跑，
// 改的是两份不同的文件——在接入之前，它们是同一份。
package agent

import (
	"context"
	"os"
	"strconv"
	"strings"

	"gleam/internal/worktree"
	"gleam/pkg/types"
)

// isolation 一次任务的隔离结论。
type isolation struct {
	// roots 交给工具的文件边界；空表示不隔离（在工作区里直接执行）。
	roots []string
	// cwd 规划与执行的工作目录。
	cwd string
}

// WorktreesView 设置页「Worktrees」一页要的全部事实。
//
// 为什么把「工作区是不是仓库」也一并回传：这一页最容易犯的错是把空态画成一个
// 没内容的列表——读者会读成"还没建过副本"，而事实可能是"这个工作区根本建不了副本"。
// 两句话背后的动作完全不同（去跑个任务 vs 换工作区），所以必须分开说。
func (a *Agent) WorktreesView() map[string]any {
	ctx := context.Background()
	rows := []map[string]any{}
	if m := a.worktreeManager(); m != nil {
		for _, meta := range m.List() {
			row := map[string]any{
				"task_id": meta.TaskID, "path": meta.Path, "branch": meta.Branch,
				"repo": meta.Repo, "created_at": meta.CreatedAt, "exists": true,
			}
			switch _, err := os.Stat(meta.Path); {
			case os.IsNotExist(err):
				row["exists"] = false
				row["dirty_detail"] = "目录已经不在了（被外部删掉）；删掉这条记录会顺带收敛 git 自己的登记"
			default:
				dirty, detail, derr := worktree.Dirty(ctx, meta.Path)
				switch {
				case derr != nil:
					row["dirty"] = true
					row["dirty_detail"] = "读不出状态：" + derr.Error()
				default:
					row["dirty"] = dirty
					row["dirty_detail"] = detail
				}
			}
			rows = append(rows, row)
		}
	}
	var enabled, fetch, autoDelete bool
	var maxCount int
	ws := ""
	if a.Cfg != nil {
		ws = a.Cfg.Workspace
		enabled = a.Cfg.Worktrees.Enabled
		fetch = a.Cfg.Worktrees.FetchBeforeCreate
		autoDelete = a.Cfg.Worktrees.AutoDelete
		maxCount = a.Cfg.Worktrees.MaxCount
	}
	return map[string]any{
		"enabled": enabled, "fetch_before_create": fetch,
		"auto_delete": autoDelete, "max_count": maxCount,
		"workspace":  ws,
		"repository": ws != "" && worktree.IsRepo(ctx, ws),
		"rows":       rows,
	}
}

// RemoveWorktree 删掉某个任务的副本。force=false 时，里面有未提交改动就拒绝
// （由界面去问用户要一句"确认丢弃"，见 worktree.ErrDirty）。
func (a *Agent) RemoveWorktree(taskID string, force bool) error {
	m := a.worktreeManager()
	if m == nil {
		return worktree.ErrNoDataDir
	}
	return m.Remove(context.Background(), taskID, force)
}

// worktreeManager 生命周期 owner：每次现取配置，用户改了设置下一个任务就按新的来。
func (a *Agent) worktreeManager() *worktree.Manager {
	if a.Cfg == nil || strings.TrimSpace(a.Cfg.DataDir) == "" {
		return nil
	}
	return &worktree.Manager{
		DataDir:      a.Cfg.DataDir,
		BranchPrefix: func() string { return a.Cfg.Git.BranchPrefix },
		Fetch:        func() bool { return a.Cfg.Worktrees.FetchBeforeCreate },
		Egress: func(host string, n int) {
			if a.Gate != nil {
				a.Gate.RecordEgress("git.remote", host, n)
			}
		},
	}
}

// isolateForTask 决定这次任务在哪跑，并把结论如实说给用户听。
//
// 四种结局：
//  1. 开关关着 → 不隔离，没什么可交代的；
//  2. 开着且工作区是仓库 → 在 <数据目录>/worktrees/<任务ID> 里跑；
//  3. 开着且这个任务建过 → 复用那一份（重跑同一个任务不该再建一个）；
//  4. 开着但建不了（不是仓库、没装 git、目录被占）→ **降级为不隔离**，把原因说出来。
//
// 第 4 条是刻意的：隔离是保险，不是前提。为了"必须隔离"而让任务跑不起来，
// 是拿"能不能干活"去换"干得干不干净"——与写前快照同一个取舍（见 preimage.go 的开头）。
func (a *Agent) isolateForTask(ctx context.Context, req types.GoalRequest, taskID string, notify func(phase, msg string, pct int, kind string)) isolation {
	cwd := a.workspaceOf(req)
	if a.Cfg == nil || !a.Cfg.Worktrees.Enabled {
		return isolation{cwd: cwd}
	}
	// 调用方点名了工作目录（评测用它把任务钉在 fixture 上）。套一层 worktree 会把它
	// 指到别处，那不是它要的——这种时候隔离的含义已经由调用方自己定了。
	if v, ok := req.Context["cwd"].(string); ok && strings.TrimSpace(v) != "" {
		return isolation{cwd: cwd}
	}
	ws := strings.TrimSpace(a.Cfg.Workspace)
	if ws == "" {
		notify("plan", "已开启 worktree 隔离，但还没选工作区：这次没有可建副本的仓库，在工作区里直接执行", 2, "warn")
		return isolation{cwd: cwd}
	}
	m := a.worktreeManager()
	if m == nil {
		return isolation{cwd: cwd}
	}
	if meta, ok := m.Get(taskID); ok {
		notify("plan", "这次任务在它的 worktree 里执行："+meta.Path, 2, "info")
		return isolation{roots: []string{meta.Path}, cwd: meta.Path}
	}
	meta, err := m.Create(ctx, taskID, ws, "")
	if err != nil {
		notify("plan", "worktree 没建成（"+err.Error()+"），这次在工作区里直接执行", 2, "warn")
		return isolation{cwd: cwd}
	}
	notify("plan", "这次任务在它的 worktree 里执行："+meta.Path+"（分支 "+meta.Branch+"）", 2, "info")
	return isolation{roots: []string{meta.Path}, cwd: meta.Path}
}

// settleWorktree 一次任务跑完之后处置它的 worktree。
//
// 用**不受任务取消影响**的 ctx：任务失败或被取消时正是最需要收尾的时候，
// 而那时候任务自己的 ctx 已经 done 了，拿它去跑 git 只会得到一串"context canceled"。
//
// 删除只发生在干净的时候（remove 自己会拒），脏的一律留着——用户还没看过的改动
// 不该被一次收尾悄悄丢掉，这里只说清它在哪、分支叫什么。
func (a *Agent) settleWorktree(taskID string) {
	if a.Cfg == nil || !a.Cfg.Worktrees.Enabled {
		return
	}
	m := a.worktreeManager()
	if m == nil {
		return
	}
	meta, ok := m.Get(taskID)
	if !ok {
		return
	}
	ctx := context.Background()
	if a.Cfg.Worktrees.AutoDelete {
		dirty, detail, err := worktree.Dirty(ctx, meta.Path)
		switch {
		case err != nil:
			a.notifyWorktree("收尾时没读出来 worktree 的状态（"+err.Error()+"），这次不删它", "warn")
		case dirty:
			a.notifyWorktree("worktree 里有未提交的改动（"+detail+"），没有自动删除："+meta.Path+"（分支 "+meta.Branch+"）", "warn")
		default:
			if err := m.Remove(ctx, taskID, false); err != nil {
				a.notifyWorktree("worktree 没能自动删掉："+err.Error(), "warn")
			}
		}
	}
	if keep := a.Cfg.Worktrees.MaxCount; keep > 0 {
		removed, keptDirty := m.PruneClean(ctx, keep)
		if len(removed) > 0 {
			a.notifyWorktree("worktree 超过上限，已清掉最旧的 "+strconv.Itoa(len(removed))+" 个", "info")
		}
		// 数量还超着，必须说清为什么：上限不是"到点就丢东西"的许可。
		if len(keptDirty) > 0 {
			a.notifyWorktree("还有 "+strconv.Itoa(len(keptDirty))+" 个 worktree 因为里面有未提交的改动而没被清理，超过上限的部分先留着", "warn")
		}
	}
}

// notifyWorktree 把 worktree 的收尾结论报到进度里。
// 阶段用 done：这些事发生在任务跑完之后，不该挤进执行中的那几条进度里。
func (a *Agent) notifyWorktree(msg, kind string) {
	if a.Notifier == nil {
		return
	}
	a.Notifier.OnProgress(types.ProgressEvent{Phase: "done", Message: msg, Progress: 100, Kind: kind})
}
