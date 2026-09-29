package webui

import (
	"net/http"
)

// handleConnections 返回「连接与出网」台账：本机每一条常驻边界，通到哪、谁能触发、
// 什么东西会离开本机、留痕在哪、想关掉动哪里。
//
// **为什么这一行数据由后端算**：这张表的全部价值在于"它是实测的"。若前端自己拼
// （从 /api/info 拿版本、从 /api/mcp 拿服务器、自己判地址是不是回环），那它就是一份
// 靠约定维持的说明文——每个字段都可能与后端不同步，而不同步的方向是"看起来一切正常"。
// 引擎那边已经有的事实（配置、注册表、门控留痕、凭证）只能在这里一次性取齐。
//
// 为什么放在「安全」页而不是新起一个"连接器"页：这张表回答的不是"我还能连什么"，
// 而是"我允许了什么"——那是安全口径，不是货架。
func (s *Server) handleConnections(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Agent.ConnectionView(s.BindAddr))
}
