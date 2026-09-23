// Package shell 实现命令执行工具（高风险，始终需要人工批准）。
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

const maxOutput = 64 * 1024 // 输出截断上限

// Tool shell.exec 工具。
type Tool struct {
	DefaultTimeout time.Duration
	// Roots 允许作为工作目录的根（与文件工具同一套边界）。
	// 为空时**拒绝任何 cwd 参数**——失败要朝着安全的方向失败。
	Roots []string
}

func New(defaultTimeout time.Duration) *Tool { return &Tool{DefaultTimeout: defaultTimeout} }

// NewWithRoots 带工作区边界创建：cwd 必须落在 roots 之内。
func NewWithRoots(defaultTimeout time.Duration, roots ...string) *Tool {
	return &Tool{DefaultTimeout: defaultTimeout, Roots: roots}
}

func (t *Tool) Name() string { return "shell.exec" }
func (t *Tool) Description() string {
	return "在系统 Shell 中执行命令并返回输出（高风险操作，需要用户批准）"
}
func (t *Tool) Permission() types.Permission {
	return types.PermissionFullAccess
}
func (t *Tool) Schema() map[string]any {
	return toolutil.Schema("执行 Shell 命令", []string{"command"}, map[string]any{
		"command":     toolutil.SchemaProp("要执行的命令", "string"),
		"cwd":         toolutil.SchemaProp("工作目录（可选，须在工作区内）", "string"),
		"timeout_sec": toolutil.SchemaProp("超时秒数（默认 30）", "integer"),
	})
}
func (t *Tool) Execute(ctx context.Context, args map[string]any) (any, error) {
	command, err := toolutil.RequireStr(args, "command")
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(toolutil.Int(args, "timeout_sec", int(t.DefaultTimeout/time.Second))) * time.Second
	if timeout <= 0 {
		timeout = t.DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// cwd 走与文件工具同一套边界校验（P5-1）：命令的工作目录不能是"任何地方"，
	// 否则模型可以用 `cd /` 把后续相对路径操作带到工作区之外。
	cwd, err := t.resolveCWD(toolutil.Str(args, "cwd"))
	if err != nil {
		return nil, err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}
	// 子进程环境：剔除凭证类变量（P5-1）。
	// 模型可以把任何命令的输出读回来，所以凭证只要进了子进程环境就等于可能进了上下文——
	// 这是一条"把凭证从模型的射程里拿开"的隔离，不是沙箱。
	cmd.Env = sanitizedEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// WaitDelay：进程退出后，等 I/O 管道关闭的**上限**。
	//
	// 为什么必须有：`Stdout`/`Stderr` 不是 `*os.File` 时，`os/exec` 会另起 goroutine
	// 把管道内容拷进 buffer，而 `Wait` 要等这些 goroutine 结束。管道写端只要还有
	// **任何**进程持有，这个拷贝就永远不返回——而 `CommandContext` 在 Windows 上
	// 只杀**直接子进程**，孙进程会存活并继续持有继承来的管道句柄。
	// 症状不是"这条命令慢"，而是**整个测试包挂到超时**（实测出现过一次：
	// `go test ./...` 跑到 `internal/tools/shell` 卡满 300s 超时，单跑该包 10s 通过）。
	// 而一个偶发失败的闸门很快就会被忽略——这正是它必须修而不是"重跑一次"的原因。
	//
	// 到点后 `Wait` 返回 `exec.ErrWaitDelay`，由下面的分支如实报告（见 pipeStuck）。
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()

	// 超时/取消优先于退出码判定：进程被杀在 Windows 上表现为普通 ExitError（exit 1），
	// 若先判 ExitError 会把超时误报成普通失败。
	if ctx.Err() != nil {
		return nil, fmt.Errorf("命令被取消: %w", ctx.Err())
	}
	if cctx.Err() != nil {
		return nil, fmt.Errorf("命令超时（%v）", timeout)
	}

	exitCode := 0
	pipeStuck := false
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case asExit(err, &exitErr):
			exitCode = exitErr.ExitCode()
		case errors.Is(err, exec.ErrWaitDelay):
			// 命令本身结束了，但有子进程仍持有输出管道。**必须说出来**：
			// 这时 stdout/stderr 很可能是不完整的，把它当完整输出用就是"静默截断"——
			// 读的人会以为命令只输出了这些，然后照着残缺的输出下结论。
			pipeStuck = true
		default:
			// 进程未能启动等非退出类错误（原实现会被静默吞掉）
			return nil, fmt.Errorf("命令执行失败: %w", err)
		}
	}
	out := map[string]any{
		"command":   command,
		"exit_code": exitCode,
		"stdout":    truncate(stdout.String()),
		"stderr":    truncate(stderr.String()),
		"error":     errString(err, exitCode),
	}
	if pipeStuck {
		out["error"] = "命令已结束，但有子进程仍持有输出管道：输出可能不完整"
		out["output_incomplete"] = true
	}
	return out, nil
}

// resolveCWD 校验工作目录：空值放行（继承进程当前目录）；非空必须落在工作区内。
func (t *Tool) resolveCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", nil
	}
	abs, err := toolutil.ResolveInRoots(cwd, t.Roots)
	if err != nil {
		return "", fmt.Errorf("工作目录不可用：%w", err)
	}
	return abs, nil
}

// credentialSuffixes 凭证类环境变量的后缀/子串特征（大小写不敏感）。
// 只拦"几乎确定是凭证"的模式：宽到把 PATH 也拦了，命令就跑不起来了。
var credentialMarkers = []string{
	"_API_KEY", "_APIKEY", "API_KEY", "_TOKEN", "ACCESS_TOKEN", "_SECRET",
	"PASSWORD", "PASSWD", "CREDENTIAL", "_PRIVATE_KEY",
}

// sanitizedEnv 从环境变量里剔除凭证类条目。
// 保留其余全部变量：这是个"拿开凭证"的动作，不是"清空环境"——
// 后者会让用户的命令在一个陌生的环境里运行，故障排查成本远高于收益。
func sanitizedEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isCredentialVar(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isCredentialVar(name string) bool {
	up := strings.ToUpper(name)
	for _, m := range credentialMarkers {
		if strings.Contains(up, m) {
			return true
		}
	}
	return false
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// Outcome 实现 types.OutcomeReporter：命令以非零码退出是「跑完了但没做成」，不是成功。
//
// 为什么必须由工具自己报：exit_code 与 error 只存在于返回的 payload 里，
// 执行器拿到的 err 是 nil，于是 `exit 1` 的步骤被标成 succeeded、整个任务被判成 success。
// 「完成率」这个指标失真，根因就在这里——不是统计口径问题，是数据源头不对。
func (t *Tool) Outcome(args map[string]any, out any) (types.StepOutcome, string) {
	m, ok := out.(map[string]any)
	if !ok {
		return types.OutcomeOK, ""
	}
	code := toolutil.IntOf(m["exit_code"])
	if code == 0 {
		return types.OutcomeOK, ""
	}
	return types.OutcomeFailed, fmt.Sprintf("命令以退出码 %d 结束", code)
}

func errString(err error, code int) string {
	if err == nil || code == 0 {
		return ""
	}
	return err.Error()
}

func truncate(s string) string {
	if len(s) > maxOutput {
		return s[:maxOutput] + "\n…（输出已截断）"
	}
	return s
}
