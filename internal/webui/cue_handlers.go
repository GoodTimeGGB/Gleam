package webui

import (
	"net/http"
)

// handleCues 返回候补目标：从本机任务归档里浮出来的「你可能想动一下」。
//
// **为什么由后端算**：这一页说的每一句话都得能指着某份归档复现。前端若自己拉
// /api/tasks 再数一遍失败次数，它就和 trace_id 的分组口径成了两套账——漂移的方向
// 恰好是「用户以为没人管」。
//
// 为什么**不在这里执行任何东西**：提议层没有手。POST adopt 也只改状态，
// 真正的执行是用户把那句话发出去的那一下。
func (s *Server) handleCues(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Agent.CueView())
}

// handleCueDismiss 记下「别再提这一条」。
func (s *Server) handleCueDismiss(w http.ResponseWriter, r *http.Request) {
	if err := s.Agent.CueDismiss(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"muted": true})
}

// handleCueAdopt 记下「这条已经交给用户了」。**它不提交任务**。
func (s *Server) handleCueAdopt(w http.ResponseWriter, r *http.Request) {
	if err := s.Agent.CueAdopt(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"adopted": true})
}

// handleCueUnsuppress 撤销一条处置：那张卡会重新浮出来。
//
// 为什么要留撤销：「别再提」点错了就变成永久静默，而永久静默的下一步是用户
// 再也不信这一页会替他盯着什么。
func (s *Server) handleCueUnsuppress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体格式无效")
		return
	}
	if err := s.Agent.CueUnsuppress(body.Fingerprint); err != nil {
		writeErr(w, http.StatusNotFound, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}
