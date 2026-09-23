// Package server 实现 Gleam 与宿主（编辑器插件 / CLI）的 JSON-RPC 2.0 通信层：
// 换行分隔的 ndjson 帧格式，双向请求（宿主可应答 Gleam 的审批请求）。
package server

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 版本与错误码。
const (
	Version            = "2.0"
	CodeParse          = -32700
	CodeInvalidReq     = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// Request 入站请求（id 缺省则为通知）。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 出站响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError JSON-RPC 错误对象。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

// EncodeRequest 编码请求/通知为一行 JSON。
func EncodeRequest(id json.RawMessage, method string, params any) ([]byte, error) {
	r := Request{JSONRPC: Version, ID: id, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		r.Params = b
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// EncodeResponse 编码响应为一行 JSON。
func EncodeResponse(id json.RawMessage, result any, rpcErr *RPCError) ([]byte, error) {
	if result == nil && rpcErr == nil {
		result = map[string]any{}
	}
	b, err := json.Marshal(Response{JSONRPC: Version, ID: id, Result: result, Error: rpcErr})
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Errf 快速构造 RPCError。
func Errf(code int, format string, args ...any) *RPCError {
	return &RPCError{Code: code, Message: fmt.Sprintf(format, args...)}
}
