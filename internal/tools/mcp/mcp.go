// Package mcp 实现轻量级 MCP（Model Context Protocol）连接器：
// 以 stdio 或 streamable-http 两种传输做 JSON-RPC 2.0（initialize / tools/list / tools/call），
// 并把对端工具适配为 Gleam 的 types.Tool 注册进注册表。纯标准库实现。
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gleam/internal/buildinfo"
	"gleam/pkg/types"
)

const protocolVersion = "2024-11-05"

// ServerConfig MCP 服务器配置。
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	// Env 追加到子进程环境（在继承的父环境之上）。API_KEY 这类凭据就走它。
	Env map[string]string `json:"env,omitempty"`
	// URL 非空 = 远端 streamable-http 服务器（不起进程）；此时 Command/Args/Env 都不看。
	URL string `json:"url,omitempty"`
	// Headers 远端请求头。凭据（Authorization 之类）走这里，只进请求头，不写日志。
	Headers map[string]string `json:"headers,omitempty"`
	Trust   string            `json:"trust,omitempty"` // readonly | user_approved | full_access
	Enabled bool              `json:"enabled,omitempty"`
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

// Client 与一个 MCP 服务器的连接（本机 stdio 子进程，或远端 streamable-http）。
//
// 握手、工具列表、工具调用对两种形态完全一样，差异都在 transport 里。
type Client struct {
	Name  string
	Info  map[string]any // initialize 返回的 serverInfo/capabilities
	Tools []ToolDef

	tr   transport
	logf func(format string, args ...any)
}

// Start 连上 MCP 服务器（按 cfg.URL 是否为空选择远端 / 本机）并完成 initialize 握手。
func Start(ctx context.Context, cfg ServerConfig, logf func(string, ...any)) (*Client, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var tr transport
	var err error
	if strings.TrimSpace(cfg.URL) != "" {
		tr, err = newHTTPTransport(cfg, logf)
	} else {
		tr, err = newStdioTransport(cfg, logf)
	}
	if err != nil {
		return nil, err
	}
	c := &Client{Name: cfg.Name, tr: tr, logf: logf}

	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var initResult json.RawMessage
	if err := c.call(hctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gleam", "version": buildinfo.Version},
	}, &initResult); err != nil {
		c.Close()
		return nil, fmt.Errorf("mcp: %s initialize 失败: %w", cfg.Name, err)
	}
	var info map[string]any
	_ = json.Unmarshal(initResult, &info)
	c.Info = info
	_ = c.notify("notifications/initialized", nil)

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

func (c *Client) notify(method string, params any) error {
	return c.tr.notify(method, params)
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	return c.tr.call(ctx, method, params, out)
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

// Close 关掉这条连接（本机进程会被杀掉；远端只丢弃会话）。
func (c *Client) Close() {
	if c.tr != nil {
		_ = c.tr.close()
	}
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
