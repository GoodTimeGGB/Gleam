// stream.go 流式读取的看门狗：三级超时。
//
// 为什么需要：只设 http.Client{Timeout} 是**整请求总时长**，抓不住"每 60 秒吐一个字"。
// 默认总超时 60 秒下，一个每 30 秒吐 1 字的响应会被判成功，而用户体验是卡死。
// 所以流式路径单独计时：
//
//	一级  TTFB        首字节到达时限——连接建了、请求发了，但一个字都不吐
//	二级  InterChunk  相邻数据块间隔时限——吐着吐着停了（比"从没开始"更常见的卡死形态）
//	三级  总时长      http.Client{Timeout}（既有机制，保留兜底）
//
// 命中任一级即中断，错误文本带"超时"，上层按可重试分类（timeout 类退避重试）。
package llm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// 看门狗时限。用 var 而非 const：单测要把它压到毫秒级才能测出超时路径。
// 生产环境不要改这两个值——要调就调它们的默认值并跑一遍评测。
var (
	// StreamTTFBTimeout 首字节时限：超过它说明连接层面就卡住了。
	StreamTTFBTimeout = 30 * time.Second
	// StreamStallTimeout 块间时限：超过它说明流"吐着吐着停了"。
	StreamStallTimeout = 15 * time.Second
)

var (
	// ErrStreamTTFB 首字节超时（可重试：换一次连接大概率能好）。
	ErrStreamTTFB = errors.New("llm: 流式响应首字节超时（TTFB）")
	// ErrStreamStall 块间超时（可重试：服务端偶发卡流）。
	ErrStreamStall = errors.New("llm: 流式响应长时间无数据（块间超时）")
)

// IsStreamTimeout 判断错误是否为流式看门狗命中的超时（上层据此按可重试分类）。
func IsStreamTimeout(err error) bool {
	return errors.Is(err, ErrStreamTTFB) || errors.Is(err, ErrStreamStall)
}

// scanStream 在三级超时看门狗下逐行读取流，对每行调用 onLine。
//
// onLine 返回 false 表示**处理者要求停止**（如收到 [DONE]），这是正常结束而非错误。
// 返回 nil 表示读尽或被 onLine 停止；返回 ErrStreamTTFB / ErrStreamStall 表示超时。
//
// 读取放在独立 goroutine：scanner.Scan() 会阻塞，主循环只有拿到控制权才能检查计时器。
// goroutine 在 ctx 取消或流读尽时退出，不会泄漏。
func scanStream(ctx context.Context, rc io.Reader, onLine func(line string) bool) error {
	type lineMsg struct {
		line string
		err  error
	}
	lines := make(chan lineMsg)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(rc)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case lines <- lineMsg{line: scanner.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case lines <- lineMsg{err: err}:
			case <-ctx.Done():
			}
		}
	}()

	// 首个数据块用 TTFB 计时，之后每收到一块重置为块间时限。
	wait := StreamTTFBTimeout
	if wait <= 0 {
		wait = 30 * time.Second
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	first := true

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if first {
				return ErrStreamTTFB
			}
			return ErrStreamStall
		case m, ok := <-lines:
			if !ok {
				return nil // 流正常读尽
			}
			if m.err != nil {
				return fmt.Errorf("llm: 流式读取中断: %w", m.err)
			}
			first = false
			// 重置计时：先停表再重置，避免上一次的到期信号被误读成这一次的超时
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			stall := StreamStallTimeout
			if stall <= 0 {
				stall = 15 * time.Second
			}
			timer.Reset(stall)
			if onLine != nil && !onLine(m.line) {
				return nil
			}
		}
	}
}
