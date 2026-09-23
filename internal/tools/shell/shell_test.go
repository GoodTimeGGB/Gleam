package shell

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"gleam/pkg/types"
)

func echoCmd(text string) string {
	if runtime.GOOS == "windows" {
		return "echo " + text
	}
	return "printf '" + text + "'"
}

func failingCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 3"
	}
	return "exit 3"
}

// execTimeout 是这几条测试的兜底超时，**不是被测对象**。
//
// 为什么不用 10s：它们断言的是「退出码怎么报」（0 与非 0 都要返回 nil error、
// 失败事实留在 payload 里），与耗时无关。而 10s 会让它们在**全量并发跑**时假红——
// 实测 `go test ./...` 里那条 `cmd /c exit 3` 花了 15.28s（机器一忙，进程创建会慢一个量级），
// 于是 TestShell_ExecExitCode 报「非零退出码不应返回 error: 命令超时（10s）」，
// 而单独跑该包 3.8s 就过。**一个偶发假红的闸门很快就会被忽略**，所以把兜底放宽。
//
// 超时行为本身由 TestShell_ExecTimeout 覆盖（它显式传 timeout_sec: 1），
// 所以这里放宽到 60s 不会少测任何东西。
const execTimeout = 60 * time.Second

func TestShell_ExecEcho(t *testing.T) {
	tool := New(execTimeout)
	out, err := tool.Execute(context.Background(), map[string]any{
		"command": echoCmd("hello-gleam"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	m := out.(map[string]any)
	if !strings.Contains(m["stdout"].(string), "hello-gleam") {
		t.Errorf("stdout = %q", m["stdout"])
	}
	if m["exit_code"] != 0 {
		t.Errorf("exit_code = %v", m["exit_code"])
	}
}

func TestShell_ExecExitCode(t *testing.T) {
	tool := New(execTimeout)
	out, err := tool.Execute(context.Background(), map[string]any{"command": failingCmd()})
	if err != nil {
		t.Fatalf("非零退出码不应返回 error: %v", err)
	}
	m := out.(map[string]any)
	if m["exit_code"] == 0 {
		t.Errorf("exit_code = %v", m["exit_code"])
	}
	if m["error"] == "" {
		t.Error("error 字段应有描述")
	}
}

// TestShell_OutcomeReportsNonZeroExit 非零退出码是「跑完了但没做成」，不是成功。
//
// 这条是"完成率失真"的源头断言：Execute 返回的 err 是 nil（调用本身没炸），
// 失败事实只留在 payload 的 exit_code/error 里。没有 OutcomeReporter，
// 执行器就会把 `exit 1` 的步骤标成 succeeded，整个任务被判成 success。
func TestShell_OutcomeReportsNonZeroExit(t *testing.T) {
	tool := New(execTimeout)

	okOut, err := tool.Execute(context.Background(), map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("echo 应成功: %v", err)
	}
	if oc, note := tool.Outcome(nil, okOut); oc != types.OutcomeOK || note != "" {
		t.Errorf("exit 0 应报 ok，实际 %q/%q", oc, note)
	}

	badOut, err := tool.Execute(context.Background(), map[string]any{"command": failingCmd()})
	if err != nil {
		t.Fatalf("非零退出码不应返回 error: %v", err)
	}
	oc, note := tool.Outcome(nil, badOut)
	if oc != types.OutcomeFailed {
		t.Errorf("非零退出应报 failed，实际 %q", oc)
	}
	if !strings.Contains(note, "退出码") {
		t.Errorf("应说明退出码，实际 %q", note)
	}
}

func TestShell_ExecTimeout(t *testing.T) {
	tool := New(execTimeout)
	// 注意：Git Bash 环境中 coreutils 的 timeout 会抢占 Windows timeout.exe，
	// 故用 ping -n 作为跨环境可靠的休眠命令（System32 自带）。
	sleepCmd := "ping -n 6 127.0.0.1 >nul"
	if runtime.GOOS != "windows" {
		sleepCmd = "sleep 5"
	}
	out, err := tool.Execute(context.Background(), map[string]any{
		"command":     sleepCmd,
		"timeout_sec": 1,
	})
	if err == nil {
		t.Fatalf("超时应报错，实际返回: %v", out)
	}
	if !strings.Contains(err.Error(), "超时") && !strings.Contains(err.Error(), "取消") {
		t.Errorf("错误信息应提示超时: %v", err)
	}
}

func TestShell_MissingCommand(t *testing.T) {
	tool := New(time.Second)
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Error("缺少 command 应报错")
	}
}

func TestShell_PermissionAndSchema(t *testing.T) {
	tool := New(time.Second)
	if tool.Name() != "shell.exec" {
		t.Errorf("name = %s", tool.Name())
	}
	schema := tool.Schema()
	if _, ok := schema["properties"].(map[string]any)["command"]; !ok {
		t.Error("schema 缺少 command 属性")
	}
}
