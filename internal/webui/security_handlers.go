package webui

import (
	"net/http"
	"strconv"
)

// handleSecurityAudit 返回安全门控的最近留痕：
// 哪些操作被拦下了、哪些被放行了、哪些是审核模型加拦的。
// 没人看过的拦截等于没发生过——这条接口就是给"事后复盘"用的。
func (s *Server) handleSecurityAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	var entries []map[string]any
	if s.Agent != nil && s.Agent.Gate != nil {
		for _, e := range s.Agent.Gate.RecentAudit(limit) {
			entries = append(entries, map[string]any{
				"time":   e.Time,
				"tool":   e.Tool,
				"risk":   e.Risk,
				"action": e.Action,
				"reason": e.Reason,
				"detail": e.Detail,
			})
		}
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(entries), "entries": entries})
}
