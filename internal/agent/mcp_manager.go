package agent

import (
	"fmt"
	"strings"
	"sync"

	"gleam/internal/tools/mcp"
)

// MCPManager 管理运行中的 MCP 连接（热安装/卸载，无需重启进程）。
type MCPManager struct {
	mu      sync.Mutex
	clients map[string]*mcp.Client // server name -> client
}

// NewMCPManager 创建管理器。
func NewMCPManager() *MCPManager {
	return &MCPManager{clients: map[string]*mcp.Client{}}
}

// Add 登记一个连接。
func (m *MCPManager) Add(c *mcp.Client) {
	if m == nil || c == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[c.Name] = c
}

// AddAll 批量登记。
func (m *MCPManager) AddAll(cs []*mcp.Client) {
	for _, c := range cs {
		m.Add(c)
	}
}

// Get 取指定服务器的连接。
func (m *MCPManager) Get(name string) (*mcp.Client, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[name]
	return c, ok
}

// Remove 取出并关闭指定服务器的连接。
func (m *MCPManager) Remove(name string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	c, ok := m.clients[name]
	if ok {
		delete(m.clients, name)
	}
	m.mu.Unlock()
	if ok {
		c.Close()
	}
	return ok
}

// CloseAll 关闭全部连接（进程退出时）。
func (m *MCPManager) CloseAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cs := m.clients
	m.clients = map[string]*mcp.Client{}
	m.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

// mcpToolPrefix MCP 工具的注册名前缀：mcp.<server>.
func mcpToolPrefix(server string) string {
	return fmt.Sprintf("mcp.%s.", strings.TrimSpace(server))
}
