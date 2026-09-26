package webui

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/config"
	"gleam/pkg/types"
)

// ---------- 工作区（任务文件夹） ----------

func TestWebUI_WorkspaceViewAndSet(t *testing.T) {
	f := newFixture(t, nil)
	oldWs := f.ws

	// 切换到新目录
	newWs := t.TempDir()
	out := f.call("POST", "/api/workspace", map[string]any{"path": newWs})
	if out["workspace"] != filepath.Clean(newWs) {
		t.Errorf("workspace = %v", out["workspace"])
	}
	recents := out["recents"].([]any)
	if len(recents) == 0 || recents[0] != filepath.Clean(newWs) {
		t.Errorf("recents = %v", recents)
	}
	// 旧工作区保留在最近列表（重命判定：内容不同即可）
	// 运行时生效：cfg + 文件工具边界 + 门控信任路径
	if f.agent.Cfg.Workspace != filepath.Clean(newWs) {
		t.Error("cfg.Workspace 未更新")
	}
	if len(f.agent.FileTools.Roots) != 1 || f.agent.FileTools.Roots[0] != filepath.Clean(newWs) {
		t.Errorf("FileTools.Roots = %v", f.agent.FileTools.Roots)
	}

	// 文件边界跟随：区内路径不触发审批且写入成功；区外路径必须被拒绝
	noApproval := func(types.ApprovalRequest) types.ApprovalResponse {
		t.Error("工作区内路径不应触发审批")
		return types.ApprovalResponse{Approved: false}
	}
	if _, err := f.agent.DirectToolCall(context.Background(), "file.write",
		map[string]any{"path": "inside.txt", "content": "ok"}, noApproval); err != nil {
		t.Fatalf("新工作区内写入失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newWs, "inside.txt")); err != nil {
		t.Error("文件未写入新工作区")
	}

	deny := func(types.ApprovalRequest) types.ApprovalResponse {
		return types.ApprovalResponse{Approved: false, Note: "测试拒绝"}
	}
	outside := filepath.Join(oldWs, "escape.txt")
	if _, err := f.agent.DirectToolCall(context.Background(), "file.write",
		map[string]any{"path": outside, "content": "escape"}, deny); err == nil || !strings.Contains(err.Error(), "拒绝") {
		t.Errorf("写入旧工作区应被拒绝: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newWs, "inside.txt")); err != nil {
		t.Error("文件未写入新工作区")
	}

	// 持久化到覆盖层
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, filepath.Join(f.dataDir, config.OverlayFile)); err != nil {
		t.Fatal(err)
	}
	if fresh.Workspace != filepath.Clean(newWs) || len(fresh.WorkspaceRecents) == 0 {
		t.Errorf("覆盖层 = %s %v", fresh.Workspace, fresh.WorkspaceRecents)
	}
}

func TestWebUI_WorkspaceInvalid(t *testing.T) {
	f := newFixture(t, nil)
	resp, err := http.Post(f.ts.URL+"/api/workspace", "application/json", strings.NewReader(`{"path":"不存在的目录xyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("不存在目录应 400, got %d", resp.StatusCode)
	}
}

func TestWebUI_FsBrowse(t *testing.T) {
	f := newFixture(t, nil)
	// 浏览工作区目录（临时目录，通常无子目录但应返回合法结构）
	out := f.call("GET", "/api/fs?path="+f.ws, nil)
	if _, ok := out["dirs"]; !ok {
		t.Errorf("browse = %v", out)
	}
	// 空路径返回根列表（Windows 盘符 / Unix 根）
	roots := f.call("GET", "/api/fs", nil)
	if len(roots["dirs"].([]any)) == 0 {
		t.Error("根列表不应为空")
	}
	// 不存在路径 → 400
	resp, err := http.Get(f.ts.URL + "/api/fs?path=Z:/no/such/dir")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("不存在目录应 400, got %d", resp.StatusCode)
	}
}

func TestWebUI_SubmitWithoutKeyWarns(t *testing.T) {
	f := newFixture(t, nil)
	f.agent.Cfg.LLM.Provider = "glm" // 真实模型但未配 Key
	f.agent.Cfg.LLM.APIKey = ""
	out := f.call("POST", "/api/goals", map[string]any{"goal": "你好", "task_mode": "chat"})
	if out["warning"] == "" || !strings.Contains(out["warning"].(string), "API Key") {
		t.Errorf("应返回无 Key 警告: %v", out)
	}
	// mock 提供商不告警
	f2 := newFixture(t, nil)
	out2 := f2.call("POST", "/api/goals", map[string]any{"goal": "你好", "task_mode": "chat"})
	if _, has := out2["warning"]; has {
		t.Errorf("mock 不应告警: %v", out2)
	}
	// 两次提交都要收尾再返回：告警只看响应，但后台那一趟会往 t.TempDir() 里写归档，
	// 不等它就清理目录，等于让这条测试偶尔红在"目录删不掉"上。
	id, _ := out["task_id"].(string)
	id2, _ := out2["task_id"].(string)
	f.waitTask(id, 10*time.Second)
	f2.waitTask(id2, 10*time.Second)
}

func TestWebUI_ToolPermissionSetAndPersist(t *testing.T) {
	f := newFixture(t, nil)
	// 设置 shell.exec 为只读放行（默认是需批准/完全访问）
	out := f.call("POST", "/api/tools/permission", map[string]any{"name": "file.write", "permission": "readonly"})
	if out["overridden"] != true {
		t.Errorf("set = %v", out)
	}
	// 工具列表带覆盖标记
	tools := f.call("GET", "/api/tools", nil)
	found := false
	for _, it := range tools["tools"].([]any) {
		tm := it.(map[string]any)
		if tm["name"] == "file.write" {
			found = true
			if tm["permission"] != "readonly" || tm["overridden"] != true {
				t.Errorf("shell.exec = %v", tm)
			}
		}
	}
	if !found {
		t.Fatal("列表缺少 shell.exec")
	}
	// 持久化到覆盖层
	fresh := config.Default()
	if err := config.LoadOverlay(fresh, filepath.Join(f.dataDir, config.OverlayFile)); err != nil {
		t.Fatal(err)
	}
	if fresh.Safety.ToolPermissions["file.write"] != "readonly" {
		t.Errorf("覆盖层 = %v", fresh.Safety.ToolPermissions)
	}
	// 恢复默认
	out2 := f.call("POST", "/api/tools/permission", map[string]any{"name": "file.write", "permission": "default"})
	if out2["overridden"] != false {
		t.Errorf("default 应清除覆盖: %v", out2)
	}
	// 非法权限 400
	resp, err := http.Post(f.ts.URL+"/api/tools/permission", "application/json", strings.NewReader(`{"name":"file.write","permission":"god"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("非法权限应 400, got %d", resp.StatusCode)
	}
}
