package auth

import (
	"fmt"
	"net"
)

// loopbackListener 在 127.0.0.1 上绑定一个随机可用端口，用于接收 OAuth 回调。
type loopbackListener struct {
	net.Listener
	port int
}

func newLoopbackListener() (*loopbackListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("无法启动本地登录回调服务: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("无法解析本地回调端口")
	}
	return &loopbackListener{Listener: ln, port: addr.Port}, nil
}

func (l *loopbackListener) Port() int { return l.port }
