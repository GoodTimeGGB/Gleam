package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
)

// HandlerMethod 入站方法处理器：返回 (结果, 错误)。
type HandlerMethod func(ctx context.Context, params json.RawMessage) (any, *RPCError)

// incoming 统一入站消息（请求/通知/响应共用）。
type incoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Conn 一条双向 JSON-RPC 连接（ndjson 帧）。
type Conn struct {
	w      io.Writer
	wmu    sync.Mutex
	reader *bufio.Scanner

	methods map[string]HandlerMethod

	pendingMu sync.Mutex
	pending   map[int64]chan Response
	outID     int64

	closed chan struct{}
	once   sync.Once

	OnError func(err error) // 非致命错误回调（可为空）
}

// NewConn 基于读写流创建连接。
func NewConn(r io.Reader, w io.Writer) *Conn {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	return &Conn{
		w:       w,
		reader:  sc,
		methods: map[string]HandlerMethod{},
		pending: map[int64]chan Response{},
		closed:  make(chan struct{}),
	}
}

// Handle 注册入站方法处理器。
func (c *Conn) Handle(method string, h HandlerMethod) {
	c.methods[method] = h
}

// Serve 阻塞读循环；请求在独立 goroutine 处理。
func (c *Conn) Serve() error {
	for {
		line, err := c.nextLine()
		if err != nil {
			c.closeOnce()
			return err
		}
		var msg incoming
		if err := json.Unmarshal(line, &msg); err != nil {
			c.writeResponse(json.RawMessage("null"), nil, Errf(CodeParse, "无法解析消息: %v", err))
			continue
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0: // 请求
			req := Request{JSONRPC: msg.JSONRPC, ID: msg.ID, Method: msg.Method, Params: msg.Params}
			go c.dispatch(req)
		case msg.Method != "": // 通知
			req := Request{JSONRPC: msg.JSONRPC, Method: msg.Method, Params: msg.Params}
			go c.dispatch(req)
		case len(msg.ID) > 0: // 对端对我们请求的响应
			c.routeResponse(msg.ID, msg.Result, msg.Error)
		default:
			c.writeResponse(json.RawMessage("null"), nil, Errf(CodeInvalidReq, "无法识别的消息"))
		}
	}
}

func (c *Conn) nextLine() ([]byte, error) {
	for c.reader.Scan() {
		line := c.reader.Bytes()
		if len(line) == 0 {
			continue
		}
		return append([]byte(nil), line...), nil
	}
	if err := c.reader.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (c *Conn) dispatch(req Request) {
	h, ok := c.methods[req.Method]
	if !ok {
		if len(req.ID) > 0 {
			c.writeResponse(req.ID, nil, Errf(CodeMethodNotFound, "未知方法 %q", req.Method))
		}
		return
	}
	result, rpcErr := h(context.Background(), req.Params)
	if len(req.ID) > 0 {
		c.writeResponse(req.ID, result, rpcErr)
	}
}

func (c *Conn) routeResponse(id json.RawMessage, result json.RawMessage, rpcErr *RPCError) {
	var idNum int64
	if err := json.Unmarshal(id, &idNum); err != nil {
		return
	}
	c.pendingMu.Lock()
	ch := c.pending[idNum]
	delete(c.pending, idNum)
	c.pendingMu.Unlock()
	if ch == nil {
		return
	}
	resp := Response{ID: id}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	select {
	case ch <- resp:
	case <-c.closed:
	}
}

func (c *Conn) writeResponse(id json.RawMessage, result any, rpcErr *RPCError) {
	b, err := EncodeResponse(id, result, rpcErr)
	if err != nil {
		if c.OnError != nil {
			c.OnError(err)
		}
		return
	}
	c.write(b)
}

func (c *Conn) write(b []byte) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(b); err != nil && c.OnError != nil {
		c.OnError(err)
	}
}

// Notify 推送通知给对端。
func (c *Conn) Notify(method string, params any) {
	b, err := EncodeRequest(nil, method, params)
	if err != nil {
		if c.OnError != nil {
			c.OnError(err)
		}
		return
	}
	c.write(b)
}

// Call 向对端发起请求并等待响应（用于审批回路 goal/ask_approval）。
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	c.pendingMu.Lock()
	c.outID++
	id := c.outID
	ch := make(chan Response, 1)
	c.pending[id] = ch
	c.pendingMu.Unlock()

	idRaw, _ := json.Marshal(id)
	b, err := EncodeRequest(idRaw, method, params)
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return err
	}
	c.write(b)

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil {
			switch t := resp.Result.(type) {
			case json.RawMessage:
				return json.Unmarshal(t, out)
			case nil:
				return nil
			default:
				b, merr := json.Marshal(t)
				if merr != nil {
					return merr
				}
				return json.Unmarshal(b, out)
			}
		}
		return nil
	}
}

// Closed 连接是否已关闭。
func (c *Conn) Closed() <-chan struct{} { return c.closed }

func (c *Conn) closeOnce() {
	c.once.Do(func() {
		c.pendingMu.Lock()
		defer c.pendingMu.Unlock()
		for id, ch := range c.pending {
			ch <- Response{Error: Errf(CodeInternal, "连接已关闭")}
			delete(c.pending, id)
		}
		close(c.closed)
	})
}
