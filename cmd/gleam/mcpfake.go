package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// runMCPFakeServer 内置 MCP 测试服务器：供 MCP 连接器测试与端到端自测使用。
// 提供三个工具：echo（回显）、add（求和）、fail（模拟错误）。
func runMCPFakeServer() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	writeMsg := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := out.Write(append(b, '\n')); err != nil {
			return err
		}
		return out.Flush()
	}

	type reqMsg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req reqMsg
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if req.Method == "" {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "gleam-fake-mcp", "version": "0.1.0"},
			}
		case "tools/list":
			result = map[string]any{
				"tools": []map[string]any{
					{
						"name":        "echo",
						"description": "原样返回输入文本",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"text": map[string]any{"type": "string", "description": "要回显的文本"}},
							"required":   []string{"text"},
						},
					},
					{
						"name":        "add",
						"description": "两数相加",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"}},
							"required":   []string{"a", "b"},
						},
					},
					{
						"name":        "fail",
						"description": "总是返回错误（测试用）",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					},
				},
			}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			switch p.Name {
			case "echo":
				var args struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(p.Arguments, &args)
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": "echo: " + args.Text}}}
			case "add":
				var args struct {
					A float64 `json:"a"`
					B float64 `json:"b"`
				}
				_ = json.Unmarshal(p.Arguments, &args)
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("sum: %g", args.A+args.B)}}}
			case "fail":
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": "模拟失败"}}, "isError": true}
			default:
				_ = writeMsg(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32602, "message": "未知工具 " + p.Name},
				})
				continue
			}
		case "ping":
			result = map[string]any{}
		default:
			_ = writeMsg(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "未知方法 " + req.Method},
			})
			continue
		}
		if err := writeMsg(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	return scanner.Err()
}
