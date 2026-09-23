package webui

import "net/http"

// handleReadiness 九坑就绪自检（只读）。
//
// 它不做任何写操作，也不发模型请求：随时可跑，用来回答"这套 Agent 现在能不能上生产"。
func (s *Server) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	if s.Agent == nil {
		writeErr(w, 503, "引擎尚未就绪")
		return
	}
	rep := s.Agent.Readiness()
	writeJSON(w, 200, map[string]any{
		"report":  rep,
		"summary": rep.SummaryLine(),
	})
}
