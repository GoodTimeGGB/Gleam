package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// P5-1：cwd 不能是"任何地方"——否则模型可以用 cd / 把后续相对路径操作带出工作区。
func TestExecute_CWDOutsideWorkspaceRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	tool := NewWithRoots(5*time.Second, root)

	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hi", "cwd": outside,
	})
	if err == nil {
		t.Fatal("工作区外的 cwd 应被拒绝")
	}
	if !strings.Contains(err.Error(), "超出允许的工作区范围") {
		t.Errorf("错误应说明越界，实际 %q", err.Error())
	}
}

// 未配置边界时拒绝任何 cwd：没配边界就放行，等于把"忘了配置"变成一次越权。
func TestExecute_NoRootsRejectsCWD(t *testing.T) {
	tool := New(5 * time.Second)
	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hi", "cwd": t.TempDir(),
	})
	if err == nil {
		t.Fatal("未配置工作区时 cwd 应被拒绝")
	}
}

func TestExecute_CWDInsideWorkspaceAllowed(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewWithRoots(10*time.Second, root)
	out, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hi", "cwd": sub,
	})
	if err != nil {
		t.Fatalf("工作区内 cwd 不该被拒: %v", err)
	}
	m := out.(map[string]any)
	if m["exit_code"].(int) != 0 {
		t.Errorf("命令应成功: %v", m)
	}
}

// 无 cwd 时继承进程目录（既有行为不能因为加了校验而改变）。
func TestExecute_NoCWDUnaffected(t *testing.T) {
	tool := NewWithRoots(10*time.Second, t.TempDir())
	out, err := tool.Execute(context.Background(), map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("不带 cwd 不该报错: %v", err)
	}
	if out.(map[string]any)["exit_code"].(int) != 0 {
		t.Errorf("命令应成功: %v", out)
	}
}

// P5-1：凭证类环境变量不进子进程——模型能读回命令输出，凭证进了子进程就等于进了上下文。
func TestSanitizedEnv_StripsCredentials(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"OPENAI_API_KEY=sk-secret",
		"GITHUB_TOKEN=ghp_xxx",
		"MY_SECRET=hunter2",
		"DB_PASSWORD=pw",
		"AWS_CREDENTIAL=cred",
		"NOT_A_CRED=x",
	}
	got := sanitizedEnv(env)
	joined := strings.Join(got, "\n")
	for _, bad := range []string{"sk-secret", "ghp_xxx", "hunter2", "pw", "cred"} {
		if strings.Contains(joined, bad) {
			t.Errorf("凭证 %q 不该进子进程环境: %v", bad, got)
		}
	}
	for _, keep := range []string{"PATH=/usr/bin", "HOME=/home/u", "NOT_A_CRED=x"} {
		if !strings.Contains(joined, keep) {
			t.Errorf("非凭证变量 %q 应保留（清空环境会让命令跑在陌生环境里）: %v", keep, got)
		}
	}
}

// 只拦"几乎确定是凭证"的模式：宽到把 PATH 也拦了，命令就跑不起来了。
func TestIsCredentialVar_DoesNotOverBlock(t *testing.T) {
	for _, name := range []string{"PATH", "HOME", "PWD", "TOKENIZER_PATH", "TEMP"} {
		if isCredentialVar(name) {
			t.Errorf("%q 不该被判为凭证变量", name)
		}
	}
	for _, name := range []string{"OPENAI_API_KEY", "api_key", "HF_TOKEN", "X_SECRET", "MYSQL_PASSWORD"} {
		if !isCredentialVar(name) {
			t.Errorf("%q 应被判为凭证变量", name)
		}
	}
}
