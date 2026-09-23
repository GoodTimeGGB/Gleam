package server

import (
	"io"
	"os"

	"gleam/internal/agent"
)

// RunStdio 在 stdin/stdout 上启动目标模式 JSON-RPC 服务（阻塞直到 EOF）。
// 这是编辑器插件接入 Gleam 的标准方式。
func RunStdio(a *agent.Agent) error {
	svc := NewService(a)
	conn := NewConn(os.Stdin, os.Stdout)
	svc.Bind(conn)
	return conn.Serve()
}

// Serve 在任意读写流上启动服务（供测试与嵌入式宿主使用）。
func Serve(a *agent.Agent, r io.Reader, w io.Writer) error {
	svc := NewService(a)
	conn := NewConn(r, w)
	svc.Bind(conn)
	return conn.Serve()
}
