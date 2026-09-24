package webui

import (
	"errors"
	"net/http"

	"gleam/internal/harness/space"
)

// requireSpaces 校验空间存储是否已装配；未装配时返回 503。
func (s *Server) requireSpaces(w http.ResponseWriter) bool {
	if s.Agent == nil || s.Agent.Spaces == nil {
		writeErr(w, 503, "空间存储未启用")
		return false
	}
	return true
}

// writeSpaceErr 空间操作错误 → 状态码：非法 ID（含 `..`、`\`、保留名 state）→400、
// 不存在→404、默认空间受保护→400、其余→400 回吐可读原因（如工作文件夹无效）。
func writeSpaceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, space.ErrInvalidID):
		writeErr(w, 400, "%v", err)
	case errors.Is(err, space.ErrNotFound):
		writeErr(w, 404, "空间不存在")
	case errors.Is(err, space.ErrDefaultSpace):
		writeErr(w, 400, "默认空间不能删除")
	default:
		writeErr(w, 400, "%v", err)
	}
}

// handleSpaceList 返回空间视图（空间列表含会话计数、激活空间、工作区）。
func (s *Server) handleSpaceList(w http.ResponseWriter, _ *http.Request) {
	if !s.requireSpaces(w) {
		return
	}
	writeJSON(w, 200, s.Agent.SpaceView())
}

type spaceCreateBody struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleSpaceCreate 新建微光空间（可选绑定工作文件夹并立即切换工作区）。
func (s *Server) handleSpaceCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpaces(w) {
		return
	}
	var body spaceCreateBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求体无效")
		return
	}
	view, err := s.Agent.SpaceCreate(body.Name, body.Path)
	if err != nil {
		writeSpaceErr(w, err)
		return
	}
	writeJSON(w, 200, view)
}

type spaceRenameBody struct {
	Name string `json:"name"`
}

// handleSpaceRename 重命名空间。
func (s *Server) handleSpaceRename(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpaces(w) {
		return
	}
	id := r.PathValue("id")
	var body spaceRenameBody
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "请求体无效")
		return
	}
	view, err := s.Agent.SpaceRename(id, body.Name)
	if err != nil {
		writeSpaceErr(w, err)
		return
	}
	writeJSON(w, 200, view)
}

// handleSpaceActivate 切换激活空间（联动切换绑定的工作文件夹）。
func (s *Server) handleSpaceActivate(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpaces(w) {
		return
	}
	id := r.PathValue("id")
	view, err := s.Agent.SpaceActivate(id)
	if err != nil {
		writeSpaceErr(w, err)
		return
	}
	writeJSON(w, 200, view)
}

// handleSpaceDelete 删除非默认空间（其会话回迁默认空间）。
func (s *Server) handleSpaceDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpaces(w) {
		return
	}
	id := r.PathValue("id")
	view, err := s.Agent.SpaceDelete(id)
	if err != nil {
		writeSpaceErr(w, err)
		return
	}
	writeJSON(w, 200, view)
}
