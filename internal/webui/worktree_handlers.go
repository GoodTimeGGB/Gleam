// worktree_handlers.go 设置页「Worktrees」一页的两个端点：列出由 Gleam 管理的副本、删掉一个。
//
// 为什么这两个端点在 webui 而不是 agent：这里只做"把请求翻译成一次调用、把结论翻译成状态码"，
// 事实全在 agent.WorktreesView / agent.RemoveWorktree 里——同一份事实若在这里再算一遍，
// 界面与命令行就会各说一套。
package webui

import (
	"errors"
	"net/http"

	"gleam/internal/worktree"
)

// handleWorktreeList 列出由 Gleam 管理的 worktree，外加"当前工作区能不能建副本"这两个事实。
//
// 后者必须一起回：只有列表的话，"一个都没有"与"这里建不了副本"在界面上长得一样，
// 而它们要用户做的事完全不同（去跑个任务 / 去换个工作区）。
func (s *Server) handleWorktreeList(w http.ResponseWriter, r *http.Request) {
	if s.Agent == nil {
		writeErr(w, http.StatusServiceUnavailable, "引擎未就绪")
		return
	}
	writeJSON(w, http.StatusOK, s.Agent.WorktreesView())
}

// handleWorktreeRemove 删掉一个任务的副本。
//
// 默认**不丢改动**：副本里有未提交的改动、或根本读不出它干不干净（主仓库已不在）时返回 409，
// 由界面把原因显示出来、再要一次明确确认；带 force=1 才是"我知道会丢，删"。
func (s *Server) handleWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	if s.Agent == nil {
		writeErr(w, http.StatusServiceUnavailable, "引擎未就绪")
		return
	}
	id := r.PathValue("id")
	force := r.URL.Query().Get("force") == "1"
	if err := s.Agent.RemoveWorktree(id, force); err != nil {
		switch {
		case errors.Is(err, worktree.ErrNeedsForce):
			writeErr(w, http.StatusConflict, "%v", err)
		case errors.Is(err, worktree.ErrNoDataDir):
			writeErr(w, http.StatusServiceUnavailable, "%v", err)
		default:
			writeErr(w, http.StatusInternalServerError, "删除失败：%v", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_id": id, "removed": true, "force": force})
}
