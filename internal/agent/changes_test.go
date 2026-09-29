package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gleam/internal/llm"
	"gleam/pkg/types"
)

// ---------- 改动清单：两份来源怎么合成一份"能动什么"的回答 ----------

// preRec 造一条写前快照记录（清单的快照主干侧）。
func preRec(seq int, stepID, tool, path string, exists bool, contentFile string) preImage {
	r := preImage{Seq: seq, StepID: stepID, Tool: tool, Path: path, Exists: exists, File: contentFile}
	if exists {
		r.Bytes = 15
	}
	return r
}

// TestBuildChanges_Kinds 一行的类型要问齐三件事：哪个工具、期望什么形态、写前在不在。
//
// 光看期望形态分不出"新建"还是"覆盖"（那要问写前），也分不出"删除"还是"移动走了"
// （那要问工具）。而界面上「新建」与「修改」是两句不同的话——用户关心原来那份还在不在。
func TestBuildChanges_Kinds(t *testing.T) {
	ws := t.TempDir()
	newFile := filepath.Join(ws, "new.txt")
	oldFile := filepath.Join(ws, "old.txt")
	goneFile := filepath.Join(ws, "gone.txt")
	subDir := filepath.Join(ws, "outdir")
	dstFile := filepath.Join(ws, "dst.txt")
	srcFile := filepath.Join(ws, "src.txt")

	for p, content := range map[string]string{oldFile: "写前的内容", dstFile: "搬来的", goneFile: "将被删"} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(srcFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	steps := []types.StepResult{
		writeStep("s1", newFile, "新写", false),
		writeStep("s2", oldFile, "覆盖写", false),
		mkdirStep("s3", subDir),
		deleteStep("s4", goneFile),
		moveStep("s5", srcFile, dstFile),
	}
	recs := []preImage{
		preRec(1, "s1", "file.write", newFile, false, ""), // 写前不存在 → 新建
		preRec(2, "s2", "file.write", oldFile, true, "2.pre"),
		preRec(3, "s3", "file.mkdir", subDir, false, ""),
		preRec(4, "s4", "file.delete", goneFile, true, "4.pre"),
		preRec(5, "s5", "file.move", srcFile, true, "5.pre"),
		preRec(6, "s5", "file.move", dstFile, false, ""),
	}

	got := map[string]types.FileChange{}
	list := buildChanges(steps, recs)
	for _, c := range list {
		got[filepath.Base(c.Path)] = c
	}
	if len(list) != len(got) {
		t.Fatalf("有路径被数成两行：%d 行 / %d 个路径", len(list), len(got))
	}
	for p, want := range map[string]string{
		"new.txt":  kindAdded,
		"old.txt":  kindModified,
		"outdir":   kindDir,
		"gone.txt": kindDeleted,
		"src.txt":  kindDeleted, // 移动的源端：写前有、现在没了
		"dst.txt":  kindMoved,
	} {
		c, ok := got[p]
		if !ok {
			t.Fatalf("清单少了 %s：%+v", p, list)
		}
		if c.Kind != want {
			t.Errorf("%s 的 kind = %q，期望 %q", p, c.Kind, want)
		}
	}
	// 一次移动在清单里是两行，但界面上必须看得出它们同属一步（还原要成对退）
	if got["src.txt"].StepID != "s5" || got["dst.txt"].StepID != "s5" {
		t.Errorf("移动的两行应共享 step_id：%+v %+v", got["src.txt"], got["dst.txt"])
	}
}

// TestBuildChanges_ConvergesSamePath 同一路径被写两次是正常流程，清单只能有一行。
//
// 逐条列会把"被后续步骤覆盖"报成两处改动，用户会以为任务多动了手；
// 而写前的那份记录必须取**最早**那条，否则还原只退到中间状态。
func TestBuildChanges_ConvergesSamePath(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, "twice.txt")
	if err := os.WriteFile(p, []byte("第三版"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := []types.StepResult{writeStep("s1", p, "第一版", false), writeStep("s2", p, "第三版", false)}
	recs := []preImage{
		{Seq: 1, StepID: "s1", Tool: "file.write", Path: p, Exists: true, Bytes: 9, File: "1.pre"},
		{Seq: 2, StepID: "s2", Tool: "file.write", Path: p, Exists: true, Bytes: 999, File: "2.pre"},
	}
	list := buildChanges(steps, recs)
	if len(list) != 1 {
		t.Fatalf("同一路径应收敛成一行，实得 %d 行：%+v", len(list), list)
	}
	if list[0].PrevBytes != 9 {
		t.Errorf("prev_bytes 应取**任务开始前**那份（9），实得 %d", list[0].PrevBytes)
	}
}

// TestBuildChanges_SlashVariantsAreOneRow 斜杠写法不同的同一个路径不能分成两行。
//
// 路径来自模型给的参数，`a//b` 与 `a/b` 都会出现。分成两行的后果不只是难看：
// 还原时用户点其中一行，另一行在清单里仍显示"可还原"，而盘上那份已经退了。
func TestBuildChanges_SlashVariantsAreOneRow(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, "a", "b.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 步骤侧给出规范写法、快照侧给出带冗余分隔符的写法——**同一个路径的两种字面**。
	// 核对与快照各拿一种，不先归一再比就会数成两行。
	steps := []types.StepResult{writeStep("s1", filepath.Join(ws, "a", "b.txt"), "内容", false)}
	recs := []preImage{
		preRec(1, "s1", "file.write", filepath.Clean(ws+`/a//b.txt`), true, "1.pre"),
	}
	list := buildChanges(steps, recs)
	if len(list) != 1 {
		t.Fatalf("应只有一行，实得 %+v", list)
	}
	if list[0].Reversible != true {
		t.Errorf("快照明明在，不该报不可还原：%+v", list[0])
	}
}

// TestBuildChanges_FailedWriteStillListed 这一步没成功，盘上也可能已经留下改动。
//
// **这条是本文件里最要紧的一条**：`file.write` 是先 `O_TRUNC` 再写，
// 一步失败的写入完全可能已经把原文件清空。核对只看成功的步骤，
// 所以只按核对列清单就会漏报——而漏报的方向是"用户以为盘上没动过"，比误报更坏。
func TestBuildChanges_FailedWriteStillListed(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, "half.txt")
	if err := os.WriteFile(p, []byte("被截断后剩下的"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed := types.StepResult{
		StepID: "s1", Tool: "file.write", Status: types.StepFailed,
		Error:     "写入中断",
		FinalArgs: map[string]any{"path": p, "content": "完整内容没写进去"},
	}
	list := buildChanges([]types.StepResult{failed},
		[]preImage{preRec(1, "s1", "file.write", p, true, "1.pre")})
	if len(list) != 1 {
		t.Fatalf("失败的写入也要进清单，实得 %+v", list)
	}
	c := list[0]
	if c.Kind != kindTouched {
		t.Errorf("没有核对结论时不该硬判形态，应落 touched，实得 %q", c.Kind)
	}
	if !c.Reversible || c.Blocked != "" {
		t.Errorf("写前内容留住了就该说可还原：%+v", c)
	}
	if !strings.Contains(c.Note, "未成功") {
		t.Errorf("要说清这一步没成功：%q", c.Note)
	}
	// 现字节数没人实测过，必须现取。报 0 会被读成"这个文件空了"——凭空造出来的错误结论。
	if c.Bytes != int64(len("被截断后剩下的")) {
		t.Errorf("Bytes 应为实测 %d，实得 %d", len("被截断后剩下的"), c.Bytes)
	}
}

// TestBuildChanges_BlockedReasonIsWhatUserSees 退不回去时必须说清为什么。
//
// "不可还原"四个字不够：用户接下来要知道是该重试还是该认命，
// 而原因（超过上限 / 二进制 / 没配数据目录）恰好就是这个区别。
func TestBuildChanges_BlockedReasonIsWhatUserSees(t *testing.T) {
	ws := t.TempDir()
	big := filepath.Join(ws, "big.bin")
	noSnap := filepath.Join(ws, "nosnap.txt")
	for _, p := range []string{big, noSnap} {
		if err := os.WriteFile(p, []byte("内容"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	steps := []types.StepResult{writeStep("s1", big, "内容", false), writeStep("s2", noSnap, "内容", false)}
	list := buildChanges(steps, []preImage{
		{Seq: 1, StepID: "s1", Tool: "file.write", Path: big, Exists: true, Bytes: 9e9,
			Reason: "原文件 8.4 MB，超过单个快照上限 1 MB"},
		// s2 故意不给快照：未配置数据目录时就是这样
	})
	byName := map[string]types.FileChange{}
	for _, c := range list {
		byName[filepath.Base(c.Path)] = c
	}
	b := byName["big.bin"]
	if b.Reversible {
		t.Errorf("内容没留住却报可还原：%+v", b)
	}
	if b.Blocked != "原文件 8.4 MB，超过单个快照上限 1 MB" {
		t.Errorf("应原样转述快照给出的原因，实得 %q", b.Blocked)
	}
	n := byName["nosnap.txt"]
	if n.Reversible {
		t.Errorf("没有快照不可能可还原：%+v", n)
	}
	if !strings.Contains(n.Blocked, "没有写前快照") {
		t.Errorf("要说清是没有快照，实得 %q", n.Blocked)
	}
	// 不可还原不是不可列出：两行都必须在清单里，否则用户根本不知道动了什么
	if len(list) != 2 {
		t.Fatalf("两行都要列出，实得 %+v", list)
	}
}

// TestBuildChanges_ReadOnlyTaskHasNilList 没动文件时返回 nil，而不是空切片。
//
// "这个任务没动文件"与"动了但列不出来"在界面上是两句话；而且字段是 omitempty，
// 返回空切片会让 JSON 里出现一个 `"changes": []`，前端反而要区分"没这键"和"空数组"。
func TestBuildChanges_ReadOnlyTaskHasNilList(t *testing.T) {
	reply := types.StepResult{StepID: "s1", Tool: "reply", Status: types.StepSucceeded}
	if got := buildChanges([]types.StepResult{reply}, nil); got != nil {
		t.Errorf("只读任务的清单应是 nil，实得 %+v", got)
	}
	b, err := json.Marshal(types.GoalResult{TaskID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"changes"`) {
		t.Errorf("无改动时不该出现 changes 键：%s", b)
	}
}

// TestChanges_SurviveArchiveRoundTrip 清单要能进归档、读回来，字段一个不丢。
//
// 归档是"清单现在长什么样"的唯一 owner：还原后那一位置在归档里，重启后才看得见。
// 所以这条验的是**落盘格式**，不是内存里的结构体——只在本次运行里对的清单没有价值。
func TestChanges_SurviveArchiveRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := &types.GoalResult{
		TaskID: "chg-1", Goal: "改两个文件", Status: types.GoalSuccess,
		Changes: []types.FileChange{{
			Path: `C:\ws\a.txt`, Kind: kindModified, Tool: "file.write", StepID: "s1",
			OK: true, Bytes: 20, PrevBytes: 15, Reversible: true,
		}, {
			Path: filepath.Join(t.TempDir(), "b.bin"), Kind: kindTouched, Tool: "file.write",
			StepID: "s2", Reversible: false, Blocked: "二进制内容，没有对比价值",
		}},
	}
	if err := SaveTaskResult(dataDir, g); err != nil {
		t.Fatalf("归档失败：%v", err)
	}
	back, err := ReadTaskResult(dataDir, "chg-1")
	if err != nil {
		t.Fatalf("读回归档失败：%v", err)
	}
	if len(back.Changes) != 2 {
		t.Fatalf("清单没读回来：%+v", back.Changes)
	}
	first, second := back.Changes[0], back.Changes[1]
	if first.Kind != kindModified || first.PrevBytes != 15 || first.Bytes != 20 {
		t.Errorf("体量/形态没保住：%+v", first)
	}
	if !first.Reversible || first.Blocked != "" {
		t.Errorf("可还原的位丢了：%+v", first)
	}
	if second.Reversible || second.Blocked != "二进制内容，没有对比价值" {
		t.Errorf("不可还原及其原因丢了：%+v", second)
	}
}

// TestRunGoal_ChangesReachResult 走完整目标：清单必须出现在终态结果里。
//
// 前面几条测的是组装，这条测**接线**——`buildChanges` 有没有真的被终态构造调用、
// `DataDir` 有没有真的传到执行器（快照是清单的主干）。判据对但线没接是本仓库栽过多次的坑。
func TestRunGoal_ChangesReachResult(t *testing.T) {
	f := newFixture(t, []llm.Scripted{
		planScript(`{"steps":[
			{"id":"s1","description":"写文件","tool":"file.write","args":{"path":"chg.txt","content":"第一版"}},
			{"id":"s2","description":"回复","tool":"reply","args":{"text":"完成"},"depends_on":["s1"]}
		]}`),
		reflectScript(95, "done", "ok"),
	})
	res := f.a.RunGoal(context.Background(), types.GoalRequest{
		Goal: "创建 chg.txt", Mode: string(types.ModeAuto),
	})
	if res.Status != types.GoalSuccess {
		t.Fatalf("任务没跑成：%+v", res)
	}
	if len(res.Changes) != 1 {
		t.Fatalf("应有 1 条改动，实得 %+v", res.Changes)
	}
	c := res.Changes[0]
	if filepath.Base(c.Path) != "chg.txt" {
		t.Errorf("路径不对：%+v", c)
	}
	if c.Kind != kindAdded {
		t.Errorf("这是新建，kind 应是 added，实得 %q", c.Kind)
	}
	if !c.Reversible {
		t.Errorf("写前快照没生效，清单会整排报「不可还原」：%+v", c)
	}
	// 快照真的落到了盘上——不然"可还原"是一句空头承诺
	files, err := os.ReadDir(filepath.Join(SnapshotRoot(f.a.Cfg.DataDir), res.TaskID))
	if err != nil || len(files) == 0 {
		t.Fatalf("可还原却没有任何快照文件在盘上：err=%v files=%v", err, files)
	}
}

// ---------- 测试装配用的步骤构造 ----------

func mkdirStep(id, path string) types.StepResult {
	return types.StepResult{
		StepID: id, Tool: "file.mkdir", Status: types.StepSucceeded,
		Output:    map[string]any{"path": path},
		FinalArgs: map[string]any{"path": path},
	}
}

func deleteStep(id, path string) types.StepResult {
	return types.StepResult{
		StepID: id, Tool: "file.delete", Status: types.StepSucceeded,
		Output:    map[string]any{"path": path},
		FinalArgs: map[string]any{"path": path},
	}
}

func moveStep(id, src, dst string) types.StepResult {
	return types.StepResult{
		StepID: id, Tool: "file.move", Status: types.StepSucceeded,
		Output:    map[string]any{"src": src, "dst": dst},
		FinalArgs: map[string]any{"src": src, "dst": dst},
	}
}
