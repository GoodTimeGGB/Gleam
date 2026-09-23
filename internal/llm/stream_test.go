package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// withShortWatchdog 把看门狗压到毫秒级并返回还原函数。
// 时限是包级 var 就是为了这个：不改生产默认值也能测出超时路径。
func withShortWatchdog(t *testing.T, ttfb, stall time.Duration) {
	t.Helper()
	oldTTFB, oldStall := StreamTTFBTimeout, StreamStallTimeout
	StreamTTFBTimeout, StreamStallTimeout = ttfb, stall
	t.Cleanup(func() { StreamTTFBTimeout, StreamStallTimeout = oldTTFB, oldStall })
}

// 一级超时：连接建了、一个字都不吐——总超时 60 秒下这种会被判成功，体验却是卡死。
func TestScanStream_TTFBTimeout(t *testing.T) {
	withShortWatchdog(t, 40*time.Millisecond, time.Second)
	pr, pw := io.Pipe()
	defer pw.Close() // 永不写入

	err := scanStream(context.Background(), pr, func(string) bool { return true })
	if !errors.Is(err, ErrStreamTTFB) {
		t.Fatalf("应报首字节超时，实际 %v", err)
	}
	if !IsStreamTimeout(err) {
		t.Error("IsStreamTimeout 应识别首字节超时")
	}
	// 错误文本必须能让上层按「可重试」分类：ClassifyError 认"超时"关键词
	if !strings.Contains(err.Error(), "超时") {
		t.Errorf("错误文本应含「超时」以便归类为可重试: %q", err.Error())
	}
}

// 二级超时：吐了一块之后停了——比"从没开始"更常见的卡死形态。
func TestScanStream_StallTimeout(t *testing.T) {
	withShortWatchdog(t, time.Second, 40*time.Millisecond)
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		_, _ = pw.Write([]byte("data: 第一块\n"))
		// 之后不再写：模拟服务端吐到一半卡住
	}()

	var got []string
	err := scanStream(context.Background(), pr, func(line string) bool {
		got = append(got, line)
		return true
	})
	if !errors.Is(err, ErrStreamStall) {
		t.Fatalf("应报块间超时，实际 %v", err)
	}
	if len(got) != 1 || got[0] != "data: 第一块" {
		t.Errorf("超时前已到的行不该丢: %v", got)
	}
	if !IsStreamTimeout(err) {
		t.Error("IsStreamTimeout 应识别块间超时")
	}
}

// 正常路径：读尽返回 nil，不因看门狗误伤。
func TestScanStream_NormalCompletion(t *testing.T) {
	withShortWatchdog(t, time.Second, time.Second)
	var got []string
	err := scanStream(context.Background(), strings.NewReader("a\nb\nc\n"), func(line string) bool {
		got = append(got, line)
		return true
	})
	if err != nil {
		t.Fatalf("正常读完不该报错: %v", err)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("行 = %v", got)
	}
}

// onLine 返回 false = 处理者要求停止（收到 [DONE]），这是正常结束而非错误。
func TestScanStream_StopByHandler(t *testing.T) {
	withShortWatchdog(t, time.Second, time.Second)
	var got []string
	err := scanStream(context.Background(), strings.NewReader("a\n[DONE]\nb\n"), func(line string) bool {
		if line == "[DONE]" {
			return false
		}
		got = append(got, line)
		return true
	})
	if err != nil {
		t.Fatalf("主动停止不该报错: %v", err)
	}
	if strings.Join(got, ",") != "a" {
		t.Errorf("停止后的行不该再被处理: %v", got)
	}
}

// ctx 取消优先于看门狗：取消是用户意图，不该被报成超时。
func TestScanStream_ContextCancel(t *testing.T) {
	withShortWatchdog(t, time.Second, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := scanStream(ctx, pr, func(string) bool { return true })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应报 context.Canceled，实际 %v", err)
	}
	if IsStreamTimeout(err) {
		t.Error("取消不该被当成流式超时")
	}
}
