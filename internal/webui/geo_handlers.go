package webui

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gleam/internal/agent/geo"
	"gleam/pkg/types"
)

// maxGEOAnalyzeRunes 单次手动分析的输入上限，避免把整本书塞进一次请求。
const maxGEOAnalyzeRunes = 20000

// handleGEOHistory 返回 GEO 准则、历史记录与汇总统计（GEO 板块首屏）。
func (s *Server) handleGEOHistory(w http.ResponseWriter, r *http.Request) {
	n := 0
	if v := strings.TrimSpace(r.URL.Query().Get("n")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	records := []geo.Record{}
	var total, best int
	var avg float64
	if s.Agent.GEO != nil {
		records = s.Agent.GEO.List(n)
		total, avg, best = s.Agent.GEO.Stats()
	}
	enabled := true
	if s.Agent.Cfg != nil {
		enabled = s.Agent.Cfg.Agent.GEOEnabled
	}
	writeJSON(w, 200, map[string]any{
		"records":    records,
		"principles": geo.Principles(),
		"enabled":    enabled,
		"stats": map[string]any{
			"total":      total,
			"avg_score":  avg,
			"best_score": best,
		},
	})
}

// handleGEOAnalyze 手动分析一段内容：返回结构化建议并留档到历史。
func (s *Server) handleGEOAnalyze(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
		Goal    string `json:"goal"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "参数解析失败")
		return
	}
	content := strings.TrimSpace(body.Content)
	if len([]rune(content)) < 10 {
		writeErr(w, 400, "内容太短，至少 10 个字")
		return
	}
	if len([]rune(content)) > maxGEOAnalyzeRunes {
		writeErr(w, 400, "内容过长，请控制在 %d 字以内", maxGEOAnalyzeRunes)
		return
	}
	if s.Agent.LLM == nil {
		writeErr(w, 503, "未配置模型，无法分析")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	suggestion, err := geo.NewAnalyzer(s.Agent.LLM).Analyze(ctx, content)
	if err != nil {
		writeErr(w, 502, "%v", err)
		return
	}
	var record *geo.Record
	if s.Agent.GEO != nil {
		saved := s.Agent.GEO.Add(geo.Record{
			Goal:        types.Shorten(strings.TrimSpace(body.Goal), 140),
			Score:       suggestion.Score,
			Summary:     suggestion.Summary,
			Strengths:   suggestion.Strengths,
			Weaknesses:  suggestion.Weaknesses,
			Actionables: suggestion.Actionables,
			Source:      "manual",
		})
		record = &saved
	}
	writeJSON(w, 200, map[string]any{
		"suggestion": suggestion,
		"record":     record,
		"text":       geo.Format(suggestion),
	})
}

// handleGEOClear 清空 GEO 历史。
func (s *Server) handleGEOClear(w http.ResponseWriter, _ *http.Request) {
	if s.Agent.GEO != nil {
		s.Agent.GEO.Clear()
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
