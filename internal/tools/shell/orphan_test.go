package shell

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// orphanHolder 造一条命令：它自己立刻结束，但留下一个**活着的子进程**，
// 且那个子进程继承着输出管道。
//
// 为什么需要这个形状：`Stdout`/`Stderr` 是 `bytes.Buffer` 时，`os/exec` 会另起
// goroutine 把管道内容拷进 buffer，而 `Wait` 要等这些 goroutine 结束。
// 管道写端只要还有任何进程持有，拷贝就永远不返回——而 `CommandContext` 在
// Windows 上只杀**直接子进程**，孙进程会存活并继续持有继承来的句柄。
func orphanHolder(seconds int) string {
	if runtime.GOOS == "windows" {
		// start /b 起一个后台进程，cmd 自己立刻退出；ping 不重定向输出，
		// 于是它继承着我们的 stdout/stderr 管道。
		return "start /b ping -n " + itoa(seconds+1) + " 127.0.0.1"
	}
	// sh 退出后 sleep 被 init 收养，但继承的 stdout/stderr 仍在。
	return "sleep " + itoa(seconds) + " & echo done"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestShell_OrphanChildDoesNotHangWait 命令结束后，孤儿孙进程不能把 Wait 拖住。
//
// **这条是回归测试**，来自一次真实的偶发失败：`go test ./...` 跑到本包时卡满
// 300s 超时（单跑本包 10s 通过）。根因不是"这条命令慢"，而是
// `bytes.Buffer` 接管道 + `CommandContext` 只杀直接子进程 ⇒ 拷贝 goroutine 永不返回
// ⇒ `Wait` 一直等 ⇒ **整个测试包挂到超时**。
//
// 为什么必须修而不是"重跑一次"：一个偶发失败的闸门很快就会被忽略，
// 而忽略之后它连"哪一条失败了"都不再提供。修复是 `cmd.WaitDelay`（见 shell.go）。
func TestShell_OrphanChildDoesNotHangWait(t *testing.T) {
	const orphanLife = 30 // 孤儿进程活 30 秒，远超 WaitDelay
	tool := New(60 * time.Second)

	start := time.Now()
	out, err := tool.Execute(context.Background(), map[string]any{
		"command": orphanHolder(orphanLife),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("命令本身应正常返回（不该报错），实际：%v", err)
	}
	// WaitDelay 是 5s，留足余量；关键是远小于孤儿进程的 30s 寿命。
	if elapsed > 20*time.Second {
		t.Fatalf("被孤儿进程拖住了：耗时 %v（说明 WaitDelay 没生效，会挂到包超时）", elapsed)
	}

	// 输出很可能不完整——必须**显式说出来**，不能把残缺输出当完整的用。
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("结果应为 map，实际 %T", out)
	}
	if m["output_incomplete"] != true {
		t.Errorf("应标记输出可能不完整，实际 %v（静默截断比报错更坏）", m["output_incomplete"])
	}
	if s, _ := m["error"].(string); s == "" {
		t.Error("应在 error 字段里说明原因，否则读的人不知道输出为什么少了")
	}
}

// TestShell_NormalCommandNotMarkedIncomplete 正常命令不能被误标成"输出不完整"。
//
// 反向控制：`output_incomplete` 如果恒为真，这条标记就没有信息量，
// 而"永远亮着的告警"和没有告警是一回事。
func TestShell_NormalCommandNotMarkedIncomplete(t *testing.T) {
	tool := New(10 * time.Second)
	out, err := tool.Execute(context.Background(), map[string]any{"command": echoCmd("ok")})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	m := out.(map[string]any)
	if m["output_incomplete"] == true {
		t.Error("正常结束的命令不该被标记为输出不完整")
	}
	if s, _ := m["stdout"].(string); s == "" {
		t.Error("正常命令的输出应被完整读到")
	}
}
