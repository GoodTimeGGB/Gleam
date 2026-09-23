// Package mcp 实现轻量级 MCP（Model Context Protocol）连接器：
// 通过 stdio JSON-RPC 2.0 与外部 MCP 服务器通信（initialize / tools/list / tools/call），
// 并把远端工具适配为 Gleam 的 types.Tool 注册进注册表。纯标准库实现。
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gleam/pkg/types"
)

const protocolVersion = "2024-11-05"

// ServerConfig MCP 服务器配置。
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Trust   string   `json:"trust,omitempty"` // readonly | user_approved | full_access
	Enabled bool     `json:"enabled,omitempty"`
}

// ToolDef MCP 远端工具定义。
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Client 与单个 MCP 服务器进程的连接。
type Client struct {
	Name  string
	Info  map[string]any // initialize 返回的 serverInfo/capabilities
	Tools []ToolDef

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
	closed  bool

	logf func(format string, args ...any)
}

// Start 启动 MCP 服务器进程并完成 initialize 握手。
func Start(ctx context.Context, cfg ServerConfig, logf func(string, ...any)) (*Client, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: 启动服务器 %q 失败: %w", cfg.Name, err)
	}
	c := &Client{
		Name:    cfg.Name,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReaderSize(stdout, 1024*1024),
		stderr:  stderr,
		pending: map[int64]chan rpcMessage{},
		logf:    logf,
	}
	// 丢弃 stderr 日志（避免阻塞子进程）
	if stderr != nil {
		go func() {
			buf := make([]byte, 4096)
			for {
				if _, err := stderr.Read(buf); err != nil {
					return
				}
			}
		}()
	}
	go func() { _ = cmd.Wait() }() // 回收子进程，避免僵尸
	go c.readLoop()

	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var initResult json.RawMessage
	if err := c.call(hctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gleam", "version": "0.1.0"},
	}, &initResult); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: %s initialize 失败: %w", cfg.Name, err)
	}
	var info map[string]any
	_ = json.Unmarshal(initResult, &info)
	c.Info = info
	c.notify("notifications/initialized", nil)

	var list struct {
		Tools []ToolDef `json:"tools"`
	}
	lctx, lcancel := context.WithTimeout(ctx, 10*time.Second)
	defer lcancel()
	if err := c.call(lctx, "tools/list", map[string]any{}, &list); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: %s tools/list 失败: %w", cfg.Name, err)
	}
	c.Tools = list.Tools
	return c, nil
}

// readLoop 读取子进程的 JSON-RPC 消息并分发。
func (c *Client) readLoop() {
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			c.failPending(fmt.Errorf("mcp: %s 连接关闭", c.Name))
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if len(msg.ID) > 0 {
			var id int64
			if err := json.Unmarshal(msg.ID, &id); err == nil {
				c.mu.Lock()
				ch := c.pending[id]
				delete(c.pending, id)
				c.mu.Unlock()
				if ch != nil {
					ch <- msg
				}
				continue
			}
		}
		// 服务器发起的通知/请求：MVP 只记录日志
		if msg.Method != "" {
			c.logf("mcp[%s] 收到 %s", c.Name, msg.Method)
		}
	}
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		ch <- rpcMessage{Error: &rpcError{Code: -32000, Message: err.Error()}}
		delete(c.pending, id)
	}
}

func (c *Client) notify(method string, params any) {
	c.send(rpcMessage{JSONRPC: "2.0", Method: method, Params: paramsRaw(params)})
}

func paramsRaw(p any) json.RawMessage {
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	return b
}

func (c *Client) send(msg rpcMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("mcp: %s 已关闭", c.Name)
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// call 发送请求并等待响应。
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("mcp: %s 已关闭", c.Name)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw, _ := json.Marshal(id)
	if err := c.send(rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: paramsRaw(params)}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case msg := <-ch:
		if msg.Error != nil {
			return fmt.Errorf("mcp: %s %s 错误(%d): %s", c.Name, method, msg.Error.Code, msg.Error.Message)
		}
		if out != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, out)
		}
		return nil
	}
}

// CallTool 调用远端工具，拼接文本内容返回。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	tctx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		tctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	if err := c.call(tctx, "tools/call", map[string]any{"name": name, "arguments": args}, &result); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, part := range result.Content {
		if part.Text != "" {
			b.WriteString(part.Text)
		}
	}
	text := b.String()
	if result.IsError {
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		return nil, fmt.Errorf("mcp: %s 工具 %s 返回错误: %s", c.Name, name, text)
	}
	return map[string]any{"text": text}, nil
}

// Close 关闭连接并结束子进程。
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.failPending(fmt.Errorf("mcp: %s 已关闭", c.Name))
}

// ---------- Gleam 工具适配 ----------

// Tool 将 MCP 远端工具适配为 types.Tool，命名 mcp.<server>.<tool>。
type Tool struct {
	client *Client
	def    ToolDef
	perm   types.Permission
}

func NewTool(client *Client, def ToolDef, perm types.Permission) *Tool {
	return &Tool{client: client, def: def, perm: perm}
}

func (t *Tool) Name() string { return "mcp." + t.client.Name + "." + t.def.Name }
func (t *Tool) Description() string {
	return fmt.Sprintf("[MCP:%s] %s", t.client.Name, t.def.Description)
}
func (t *Tool) Permission() types.Permission { return t.perm }

func (t *Tool) Schema() map[string]any {
	schema := map[string]any{"type": "object", "description": t.def.Description}
	if len(t.def.InputSchema) > 0 {
		var parsed map[string]any
		if err := json.Unmarshal(t.def.InputSchema, &parsed); err == nil {
			for k, v := range parsed {
				if k == "type" || k == "properties" || k == "required" {
					schema[k] = v
				}
			}
		}
	}
	return schema
}

func (t *Tool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return t.client.CallTool(ctx, t.def.Name, args)
}

// permissionFromTrust 将配置中的信任级别映射为权限。
func permissionFromTrust(trust string) types.Permission {
	switch strings.TrimSpace(trust) {
	case "readonly", "read_only":
		return types.PermissionReadOnly
	case "full_access":
		return types.PermissionFullAccess
	default:
		return types.PermissionUserApproved
	}
}

// PermissionFromTrust 将配置中的信任级别映射为权限（热安装入口使用）。
func PermissionFromTrust(trust string) types.Permission {
	return permissionFromTrust(trust)
}

// RegisterAll 连接全部已启用的 MCP 服务器并把工具注册进注册表。
// 返回成功连接的服务器列表；单个服务器失败仅记录日志不阻断。
func RegisterAll(ctx context.Context, reg interface {
	Register(t types.Tool) error
}, cfgs []ServerConfig, logf func(string, ...any)) []*Client {
	var clients []*Client
	for _, cfg := range cfgs {
		if !cfg.Enabled || cfg.Command == "" {
			continue
		}
		client, err := Start(ctx, cfg, logf)
		if err != nil {
			logf("mcp: 跳过服务器 %s: %v", cfg.Name, err)
			continue
		}
		perm := permissionFromTrust(cfg.Trust)
		for _, def := range client.Tools {
			_ = reg.Register(NewTool(client, def, perm))
		}
		logf("mcp: 已连接 %s（%d 个工具）", cfg.Name, len(client.Tools))
		clients = append(clients, client)
	}
	return clients
}
