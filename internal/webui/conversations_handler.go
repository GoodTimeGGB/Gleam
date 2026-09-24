package webui

import (
	"errors"
	"net/http"

	"gleam/internal/harness/conversation"
)

// writeConvoErr 把会话存储错误映射到合适状态码：非法 ID→400、不存在→404、
// 其余→500。此前一律 404 且原样回吐 %v，会把 OS 报错（含数据目录文件路径）泄漏给前端。
func writeConvoErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, conversation.ErrInvalidID):
		writeErr(w, 400, "%v", err)
	case errors.Is(err, conversation.ErrNotFound):
		writeErr(w, 404, "会话不存在")
	default:
		writeErr(w, 500, "会话操作失败")
	}
}

// requireConvos 校验会话存储是否已装配；未装配时返回 503。
func (s *Server) requireConvos(w http.ResponseWriter) bool {
	if s.Agent == nil || s.Agent.Convos == nil {
		writeErr(w, 503, "会话存储未启用")
		return false
	}
	return true
}

// handleConversationList 列出全部会话摘要（按最近更新倒序）。
func (s *Server) handleConversationList(w http.ResponseWriter, r *http.Request) {
	if !s.requireConvos(w) {
		return
	}
	items, err := s.Agent.Convos.List()
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	if items == nil {
		items = []conversation.Summary{}
	}
	writeJSON(w, 200, map[string]any{"conversations": items})
}

// handleConversationCreate 新建空会话并清空当前短期上下文。
// 请求体可带 space_id 指定所属微光空间；缺省归属当前激活空间。
func (s *Server) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireConvos(w) {
		return
	}
	var body struct {
		SpaceID string `json:"space_id"`
	}
	_ = readJSON(r, &body) // body 可空，忽略解析错误
	c, err := s.Agent.NewConversation(body.SpaceID)
	if err != nil {
		writeErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, c)
}

// handleConversationGet 读取单个会话完整消息（历史详情）。
func (s *Server) handleConversationGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireConvos(w) {
		return
	}
	id := r.PathValue("id")
	c, err := s.Agent.Convos.Get(id)
	if err != nil {
		writeConvoErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

// handleConversationActivate 切换到某会话：载入其历史到短期上下文，便于继续对话。
func (s *Server) handleConversationActivate(w http.ResponseWriter, r *http.Request) {
	if !s.requireConvos(w) {
		return
	}
	id := r.PathValue("id")
	c, err := s.Agent.LoadConversationContext(id)
	if err != nil {
		writeConvoErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

type conversationRenameBody struct {
	Title string `json:"title"`
}

// handleConversationRename 重命名会话。
func (s *Server) handleConversationRename(w http.ResponseWriter, r *http.Request) {
	if !s.requireConvos(w) {
		return
	}
	id := r.PathValue("id")
	var body conversationRenameBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求体无效")
		return
	}
	c, err := s.Agent.Convos.Rename(id, body.Title)
	if err != nil {
		writeConvoErr(w, err)
		return
	}
	writeJSON(w, 200, c)
}

// handleConversationDelete 删除会话。
func (s *Server) handleConversationDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireConvos(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.Agent.Convos.Delete(id); err != nil {
		writeConvoErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
