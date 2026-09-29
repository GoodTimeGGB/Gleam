package webui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/internal/agent"
	"gleam/internal/llm"
)

// ---------- 本次改动：清单 → 对比 → 还原（批次 F14）----------

// 这三条走**真实路由**，不直接调 `Agent.TaskDiff` / `TaskRevert`：
// 端点注册、参数口径（路径走查询参数）、归档回填、重启后读回，
// 每一环都可能在"方法本身是对的"情况下断掉。

// firstChange 从详情响应里取指定路径那一行。
func firstChange(t *testing.T, detail map[string]any, base string) (map[string]any, []map[string]any) {
	t.Helper()
	result, _ := detail["result"].(map[string]any)
	raw, _ := result["changes"].([]any)
	list := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		if m != nil {
			list = append(list, m)
		}
	}
	if len(list) == 0 {
		t.Fatalf("详情里没有改动清单：%v", detail)
	}
	for _, c := range list {
		if filepath.Base(toStr(c["path"])) == base {
			return c, list
		}
	}
	t.Fatalf("清单里没有 %s：%v", base, list)
	return nil, nil
}

func toStr(v any) string {
	s, _ := v.(string)
	return s
}

// TestChanges_FullLoop 改一个已有文件 → 看对比 → 点还原 → 盘上内容真的退回去。
//
// 这条是 F14 的主干：清单不只是"告诉你动了什么"，它得**兜得住**。
// 所以最后一步不看接口回什么，看磁盘上那个文件的内容——
// 界面说"已还原"而文件还是任务写进去的那份，是最坏的一种错。
func TestChanges_FullLoop(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"改写笔记","tool":"file.write","args":{"path":"note.txt","content":"第二版\n多了一行"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"已改写"},"depends_on":["s1"]}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":95,"verdict":"done","reason":"ok"}`}},
	})
	note := filepath.Join(f.ws, "note.txt")
	if err := os.WriteFile(note, []byte("第一版\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := f.call("POST", "/api/goals", map[string]any{"goal": "改写 note.txt", "mode": "auto"})
	id := toStr(out["task_id"])
	if done := f.waitTask(id, 10*time.Second); done["status"] != "success" {
		t.Fatalf("任务没跑成：%v", done)
	}
	if b, _ := os.ReadFile(note); string(b) != "第二版\n多了一行" {
		t.Fatalf("前置条件不成立：文件没被任务改掉，%q", b)
	}

	detail := f.call("GET", "/api/goals/"+id, nil)
	row, _ := firstChange(t, detail, "note.txt")
	if toStr(row["kind"]) != "modified" {
		t.Errorf("原来就有内容，应报 modified，实得 %q", row["kind"])
	}
	if row["reversible"] != true {
		t.Errorf("写前是普通文本，应可还原：%v", row)
	}
	if row["reverted"] == true {
		t.Error("还没点还原，reverted 不该已经为真")
	}
	path := toStr(row["path"])
	if !filepath.IsAbs(path) {
		t.Errorf("清单要给绝对路径（还原时按它定位）：%q", path)
	}

	// ① 对比：写前 → 现在
	diff := f.call("GET", "/api/goals/"+id+"/diff?path="+url.QueryEscape(path), nil)
	if diff["reversible"] != true {
		t.Errorf("对比视图也该带可还原位：%v", diff)
	}
	if toFloat(diff["before_bytes"]) != float64(len("第一版\n")) {
		t.Errorf("before_bytes 应是写前体量，实得 %v", diff["before_bytes"])
	}
	d, _ := diff["diff"].(map[string]any)
	lines, _ := d["lines"].([]any)
	if len(lines) == 0 {
		t.Fatalf("对比应有逐行结果：%v", diff)
	}
	var add, del int
	for _, l := range lines {
		switch toStr(l.(map[string]any)["kind"]) {
		case "add":
			add++
		case "del":
			del++
		}
	}
	if add == 0 || del == 0 {
		t.Errorf("两份内容不同，应同时有增行和删行：add=%d del=%d", add, del)
	}

	// ② 还原
	res := f.call("POST", "/api/goals/"+id+"/revert", map[string]any{"path": path})
	done, _ := res["reverted"].([]any)
	if len(done) != 1 {
		t.Fatalf("还原应回一条结果：%v", res)
	}
	if !strings.Contains(toStr(done[0].(map[string]any)["note"]), "任务开始前") {
		t.Errorf("说明要讲清退到了哪个时刻：%v", done[0])
	}
	b, err := os.ReadFile(note)
	if err != nil {
		t.Fatalf("还原后读文件失败：%v", err)
	}
	if string(b) != "第一版\n" {
		t.Errorf("盘上内容应退回写前那份，实得 %q", b)
	}

	// ③ 清单跟着变：reverted 为真，但 kind 不改（它记的是"本任务当时做了什么"）
	after := f.call("GET", "/api/goals/"+id, nil)
	row2, _ := firstChange(t, after, "note.txt")
	if row2["reverted"] != true {
		t.Errorf("还原后清单要标出已还原：%v", row2)
	}
	if toStr(row2["kind"]) != "modified" {
		t.Errorf("kind 记的是历史，不该被还原改掉：%q", row2["kind"])
	}

	// ④ 归档也要跟着变，并且**重启后仍看得见**：内存表会清空，档案才是 owner
	back, err := agent.ReadTaskResult(f.dataDir, id)
	if err != nil || back == nil {
		t.Fatalf("读回归档失败：%v", err)
	}
	if len(back.Changes) == 0 || !back.Changes[0].Reverted {
		t.Errorf("归档里应带 reverted 位：%+v", back.Changes)
	}
	f.srv.mu.Lock()
	f.srv.tasks = map[string]*taskInfo{}
	f.srv.mu.Unlock()
	restarted := f.call("GET", "/api/goals/"+id, nil)
	row3, _ := firstChange(t, restarted, "note.txt")
	if row3["reverted"] != true {
		t.Errorf("重启后清单丢了已还原标记：%v", row3)
	}
}

// TestChanges_MoveRevertsInPairs 一次移动在清单里是两行，还原必须成对退。
//
// 只退目标端会把文件留在新位置、同时把旧位置也补回来——等于复制成两份，
// 比不还原更坏。
func TestChanges_MoveRevertsInPairs(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"搬文件","tool":"file.move","args":{"src":"old.txt","dst":"new.txt"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"已搬"},"depends_on":["s1"]}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":95,"verdict":"done","reason":"ok"}`}},
	})
	src := filepath.Join(f.ws, "old.txt")
	dst := filepath.Join(f.ws, "new.txt")
	if err := os.WriteFile(src, []byte("原样\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := f.call("POST", "/api/goals", map[string]any{"goal": "把 old.txt 搬到 new.txt", "mode": "auto"})
	id := toStr(out["task_id"])
	if done := f.waitTask(id, 10*time.Second); done["status"] != "success" {
		t.Fatalf("任务没跑成：%v", done)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("前置条件不成立：没搬成：%v", err)
	}

	detail := f.call("GET", "/api/goals/"+id, nil)
	row, list := firstChange(t, detail, "new.txt")
	if toStr(row["kind"]) != "moved" {
		t.Errorf("移动的目标端应报 moved：%v", row)
	}
	if len(list) != 2 {
		t.Fatalf("一次移动应留两行（源端一条 deleted）：%v", list)
	}

	res := f.call("POST", "/api/goals/"+id+"/revert", map[string]any{"path": toStr(row["path"])})
	if res["pairs"] == nil {
		t.Errorf("成对还原要说明带上了哪条路径：%v", res)
	}
	if b, err := os.ReadFile(src); err != nil || string(b) != "原样\n" {
		t.Errorf("源路径内容应回来：err=%v content=%q", err, b)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("目标路径该被删掉（否则等于复制了一份）：%v", err)
	}
}

// TestChanges_RejectsPathsOutsideTheList 还原只认"本任务清单里的路径"。
//
// 这是这道闸的全部意义：它给的是一个能覆盖用户文件的写操作。
// 如果只校验"在 workspace 内"，那么任务只改过 a.txt 时，用户可以（或被诱导）
// 把 b.txt 也退掉——而 b.txt 的"写前"在这个任务里根本没有记录。
func TestChanges_RejectsPathsOutsideTheList(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"listed.txt","content":"任务写的"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"完成"},"depends_on":["s1"]}
		]}`}},
		{Kind: "reflect", Texts: []string{`{"score":95,"verdict":"done","reason":"ok"}`}},
	})
	keep := filepath.Join(f.ws, "unlisted.txt")
	if err := os.WriteFile(keep, []byte("用户自己的、与本任务无关的内容\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := f.call("POST", "/api/goals", map[string]any{"goal": "创建 listed.txt", "mode": "auto"})
	id := toStr(out["task_id"])
	if done := f.waitTask(id, 10*time.Second); done["status"] != "success" {
		t.Fatalf("任务没跑成：%v", done)
	}

	// ① 在 workspace 内、但不在清单里 → 拒
	code, body := f.raw("POST", "/api/goals/"+id+"/revert", map[string]any{"path": keep})
	if code < 400 {
		t.Fatalf("清单外的路径不该能还原，实得 %d：%v", code, body)
	}
	if !strings.Contains(toStr(body["error"]), "清单") {
		t.Errorf("要说清是「不在清单里」被拒，实得 %v", body)
	}
	if b, _ := os.ReadFile(keep); string(b) == "" {
		t.Error("被拒的还原绝不能已经动了文件")
	}
	// ② 对比同样拒（这条更容易被漏：只给还原加守卫的话，清单外路径仍能探测存在性）
	if code, _ := f.raw("GET", "/api/goals/"+id+"/diff?path="+url.QueryEscape(keep), nil); code < 400 {
		t.Errorf("清单外路径的对比也该被拒，实得 %d", code)
	}
	// ③ 相对路径直接拒：清单里给的是绝对路径，退回去等于让用户猜工作区在哪
	if code, _ := f.raw("POST", "/api/goals/"+id+"/revert", map[string]any{"path": "listed.txt"}); code < 400 {
		t.Errorf("相对路径应被拒，实得 %d", code)
	}
	// ④ 非法 taskID 被拒（SafeTaskName 那道闸在还原上同样要成立）
	if code, _ := f.raw("POST", "/api/goals/..%2Fsettings/revert", map[string]any{"path": keep}); code < 400 {
		t.Errorf("非法 taskID 应被拒，实得 %d", code)
	}
	// 全部拒完之后，用户那份文件还是一个字没动
	if b, _ := os.ReadFile(keep); string(b) != "用户自己的、与本任务无关的内容\n" {
		t.Errorf("被拒的还原改动了用户文件：%q", b)
	}
}

// TestChanges_ReadOnlyTaskHasNoActions 只读任务没有改动，也就没有任何可还原的东西。
//
// 空清单不是"没核对"——它本身就是回答。前端据此不渲染这一区，
// 而后端如果被绕过仍必须拒绝每一次还原。
func TestChanges_ReadOnlyTaskHasNoActions(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		{Kind: "plan", Texts: []string{`{"steps":[
			{"id":"s1","description":"回复","tool":"reply","args":{"text":"只是聊聊"}}]}`}},
		{Kind: "reflect", Texts: []string{`{"score":90,"verdict":"done","reason":"ok"}`}},
	})
	out := f.call("POST", "/api/goals", map[string]any{"goal": "聊聊", "mode": "auto"})
	id := toStr(out["task_id"])
	if done := f.waitTask(id, 10*time.Second); done["status"] != "success" {
		t.Fatalf("任务没跑成：%v", done)
	}
	detail := f.call("GET", "/api/goals/"+id, nil)
	result, _ := detail["result"].(map[string]any)
	if raw, has := result["changes"]; has && raw != nil {
		if list, ok := raw.([]any); ok && len(list) != 0 {
			t.Errorf("只读任务不该有改动清单：%v", list)
		}
	}
	if code, _ := f.raw("POST", "/api/goals/"+id+"/revert", map[string]any{"path": filepath.Join(f.ws, "anything.txt")}); code < 400 {
		t.Errorf("没有任何快照时还原必须被拒，实得 %d", code)
	}
}

func toFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}
