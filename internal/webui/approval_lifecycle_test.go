package webui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gleam/internal/llm"
)

// 停在审批闸门上的取消。
//
// 这两条测的是同一件事的两半，缺一不可：
//   - 审批名单要说真话：任务停了，`GET /api/approvals` 就不该还挂着"有一件事等你决定"；
//   - 更要真的把引擎叫醒：plan_first 的整计划闸门是**同步**等在 OnApproval 里的，
//     只把登记删掉，那一行不返回，任务就永远停在 running——取消点了没反应，
//     界面上还是"进行中"。
//
// fixture 的审批超时是 5 秒，这里只等 2 秒：能在这个窗口里定格，说明是取消把它
// 叫醒的，不是超时兜的（超时兜的话原因也会写成"审批超时"，一并被下面的断言挑出来）。

func TestCancel_PlanFirstGate(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"写","tool":"file.write","args":{"path":"pf.txt","content":"x"}}]}`}},
	})
	out := f.call("POST", "/api/goals", map[string]any{"goal": "写 pf.txt", "mode": "plan_first"})
	id, _ := out["task_id"].(string)
	ap := f.waitApproval(id)
	if ap["whole_plan"] != true {
		t.Fatalf("plan_first 应在整计划上停一次: %v", ap)
	}

	f.call("POST", "/api/goals/"+id+"/cancel", nil)

	done := f.waitTask(id, 2*time.Second)
	if done["status"] != "cancelled" {
		t.Errorf("取消后状态 = %v", done["status"])
	}
	res, _ := done["result"].(map[string]any)
	if got, _ := res["error"].(string); got != "任务已取消" {
		t.Errorf("取消的原因 = %q，应为「任务已取消」（写成拒绝或超时都是假原因）", got)
	}
	if n := f.pendingApprovals(); n != 0 {
		t.Errorf("取消后仍有 %d 条未决审批", n)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "pf.txt")); !os.IsNotExist(err) {
		t.Error("取消后不应写出产物")
	}
}

// 逐步审批这一路：executor 自己 select ctx，所以引擎不会被按住，但等待器还留在表里
// ——它要一直挂到超时才消失，那期间审批名单在说谎。
func TestCancel_StepGate(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[{"id":"s1","description":"删除","tool":"file.delete","args":{"path":"victim.txt","recursive":false}}]}`}},
	})
	if err := os.WriteFile(filepath.Join(f.ws, "victim.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := f.call("POST", "/api/goals", map[string]any{"goal": "删除 victim.txt", "mode": "auto"})
	id, _ := out["task_id"].(string)
	ap := f.waitApproval(id)
	if ap["whole_plan"] == true {
		t.Fatalf("auto 模式应停在单步上: %v", ap)
	}

	f.call("POST", "/api/goals/"+id+"/cancel", nil)

	done := f.waitTask(id, 2*time.Second)
	if done["status"] != "cancelled" {
		t.Errorf("取消后状态 = %v", done["status"])
	}
	if n := f.pendingApprovals(); n != 0 {
		t.Errorf("取消后仍有 %d 条未决审批", n)
	}
	if _, err := os.Stat(filepath.Join(f.ws, "victim.txt")); err != nil {
		t.Errorf("未批准就取消，产物不该被动过: %v", err)
	}
}
