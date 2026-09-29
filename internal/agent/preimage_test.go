package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gleam/internal/harness/registry"
	"gleam/internal/harness/safety"
	"gleam/pkg/types"
)

// 写前快照（批次 F14）。
//
// 这组测试盯的是**保险在不在**：快照层坏掉的形态不是报错、不是崩，而是用户点了
// "还原"之后盘上什么也没回去。所以每条断言都从执行器进、从磁盘回，
// 不去直接调 `snapshotter.one`——判据对、线没接是本仓库栽过多次的坑。

func snapExecutor(t *testing.T, dataDir, ws string, tools ...types.Tool) *Executor {
	t.Helper()
	r := registry.New()
	for _, tl := range tools {
		r.MustRegister(tl)
	}
	r.MustRegister(&funcTool{name: "reply", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) { return args, nil }})
	return &Executor{
		Reg: r, Gate: safety.New("auto", nil, nil, []string{ws}, 2*time.Second),
		Notifier: NopNotifier{}, StepTimeout: 2 * time.Second, DataDir: dataDir,
	}
}

// TestSnapshot_SurvivesReplan 一次目标跑两轮 Execute 时，上一轮的快照必须还在清单里。
//
// 为什么盯这个：规划可以失败重来（重规划、回落直聊），每轮各建一次执行器状态。
// 如果快照跟着"这一轮"走，第二轮只看得到自己——上一轮真实写出去的文件就从改动清单
// 里消失了，界面于是对着一份确实被写过的盘说"没动过"。这是这一层唯一不许出现的方向。
// 另一半更要紧：`firstPreImageOf` 取**最早**那一份，接不上上一轮，第二轮的"写前"
// 就是第一轮写出来的内容，还原只能退到任务中间（真机走查 f14check2 抓到的就是这个）。
func TestSnapshot_SurvivesReplan(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	target := filepath.Join(ws, "note.md")
	const old = "任务开始前的内容\n"
	if err := os.WriteFile(target, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	write := &funcTool{name: "wtest", perm: types.PermissionUserApproved,
		paths: func(map[string]any) []string { return []string{target} },
		fn: func(_ context.Context, _ map[string]any) (any, error) {
			return map[string]any{"written": true}, os.WriteFile(target, []byte("第一轮写的\n"), 0o644)
		}}
	reply := &funcTool{name: "rtest", perm: types.PermissionReadOnly,
		fn: func(_ context.Context, args map[string]any) (any, error) { return args, nil }}
	e := snapExecutor(t, dataDir, ws, write, reply)

	first := e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "wtest"}}}, "t1", string(types.ModeAuto), false)
	if len(first.PreImages) != 1 {
		t.Fatalf("第一轮该有一条快照：%+v", first.PreImages)
	}
	// 第二轮：计划里只剩一次回复（重规划后回落直聊的典型形态），什么都没写。
	second := e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "rtest"}}}, "t1", string(types.ModeAuto), false)
	if len(second.Steps) != 1 || second.Steps[0].Tool != "rtest" || second.Steps[0].Status != types.StepSucceeded {
		t.Fatalf("第二轮该只跑一步回复且成功：%+v", second.Steps)
	}

	if len(second.PreImages) != 1 || second.PreImages[0].Tool != "wtest" {
		t.Errorf("第二轮的清单把上一轮的写入弄丢了：%+v", second.PreImages)
	}
	// 清单侧同样不能漏：这一轮没有核对结论，但盘上真的多了一份改动。
	changes := buildChanges(second.Steps, second.PreImages)
	if len(changes) != 1 || changes[0].Path != target {
		t.Fatalf("改动清单该列出那个被写过的路径（漏报＝用户以为盘上没动过）：%+v", changes)
	}
	if !changes[0].Reversible {
		t.Errorf("上一轮留了写前内容，就该报可还原：%+v", changes[0])
	}
	// 落盘的那份也要接着上一轮，而不是被第二轮覆盖掉。
	recs, err := taskPreImages(dataDir, "t1")
	if err != nil {
		t.Fatalf("读回清单失败：%v", err)
	}
	if len(recs) != 1 || recs[0].Seq != 1 || recs[0].Tool != "wtest" {
		t.Fatalf("磁盘清单该留着第一轮那条：%+v", recs)
	}
	content, err := preImageContent(dataDir, "t1", recs[0])
	if err != nil {
		t.Fatalf("读快照内容失败：%v", err)
	}
	if string(content) != old {
		t.Errorf("写前内容必须是**任务开始之前**那一份，不是第一轮写出来的：%q", content)
	}
}

// TestSnapshot_CapturesBeforeWrite 写之前那份内容必须留下来，且留下的就是一字不差的旧内容。
func TestSnapshot_CapturesBeforeWrite(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	target := filepath.Join(ws, "note.md")
	const old = "旧内容\n第二行\n"
	if err := os.WriteFile(target, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	write := &funcTool{name: "wtest", perm: types.PermissionUserApproved,
		paths: func(map[string]any) []string { return []string{target} },
		fn: func(_ context.Context, _ map[string]any) (any, error) {
			return map[string]any{"written": true}, os.WriteFile(target, []byte("新内容\n"), 0o644)
		}}
	e := snapExecutor(t, dataDir, ws, write)
	res := e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "wtest"}}}, "t1", string(types.ModeAuto), false)
	if res.Succeeded != 1 {
		t.Fatalf("这一步本身应成功（快照不该影响执行）：%+v", res.Steps)
	}

	recs, err := taskPreImages(dataDir, "t1")
	if err != nil {
		t.Fatalf("读回清单失败：%v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("应留一条快照，实际 %d：%+v", len(recs), recs)
	}
	rec := recs[0]
	if !rec.Exists || rec.Bytes != int64(len(old)) || !rec.reversible() {
		t.Errorf("快照没记下写前的样子：%+v", rec)
	}
	b, err := preImageContent(dataDir, "t1", rec)
	if err != nil {
		t.Fatalf("读回写前内容失败：%v", err)
	}
	if string(b) != old {
		t.Errorf("写前内容 = %q，应为 %q", b, old)
	}
	// 内存里也要带着走：改动清单在收尾时用它，不重新读盘（收尾时盘上已经是新内容了）
	if len(res.PreImages) != 1 {
		t.Errorf("Result 里应带快照记录，实际 %d 条", len(res.PreImages))
	}
}

// TestSnapshot_ContentFileIsPrivateAndInsideRoot 快照文件既不能跑出快照根目录，也不能给别人读。
//
// 内容文件里是用户**刚被覆盖掉**的那份文本，可能就是配置或笔记；0600 与"留在根内"
// 是同一件事的两半：位置不对会被别的目录扫到，权限不对会被同机的别人读走。
func TestSnapshot_ContentFileIsPrivateAndInsideRoot(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	target := filepath.Join(ws, "p.txt")
	if err := os.WriteFile(target, []byte("机密草稿"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := &funcTool{name: "wpriv", perm: types.PermissionUserApproved,
		paths: func(map[string]any) []string { return []string{target} },
		fn: func(_ context.Context, _ map[string]any) (any, error) {
			return "ok", os.WriteFile(target, []byte("覆盖"), 0o600)
		}}
	e := snapExecutor(t, dataDir, ws, write)
	e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "wpriv"}}}, "t1", string(types.ModeAuto), false)

	recs, _ := taskPreImages(dataDir, "t1")
	if len(recs) != 1 {
		t.Fatalf("应留一条快照：%+v", recs)
	}
	full, err := preImagePath(dataDir, "t1", recs[0])
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(SnapshotRoot(dataDir))
	if !strings.HasPrefix(filepath.Clean(full), root+string(filepath.Separator)) {
		t.Errorf("快照内容跑到根目录外了：%s", full)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(full)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("快照内容权限 = %v，应为 -rw-------（里面是用户刚被覆盖的内容）", info.Mode().Perm())
		}
	}
}

// TestSnapshot_ReadOnlyStepCapturesNothing 只读步骤不留快照。
//
// 判据用**工具自己声明的权限**，不是门控当下的有效权限——后者会随用户把某个写工具
// 手动设成只读而变，那种时候关掉保险等于把"你把它设成只读了"当成"你不需要后路"。
func TestSnapshot_ReadOnlyStepCapturesNothing(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	target := filepath.Join(ws, "r.txt")
	if err := os.WriteFile(target, []byte("只读"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := &funcTool{name: "rtest", perm: types.PermissionReadOnly,
		paths: func(map[string]any) []string { return []string{target} },
		fn:    func(_ context.Context, _ map[string]any) (any, error) { return os.ReadFile(target) }}
	e := snapExecutor(t, dataDir, ws, read)
	e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "rtest"}}}, "t1", string(types.ModeAuto), false)

	if _, err := os.Stat(filepath.Join(SnapshotRoot(dataDir), "t1")); !os.IsNotExist(err) {
		t.Errorf("只读步骤不该建快照目录（每个只读任务都留一份等于把缓存当归档）")
	}
	recs, err := taskPreImages(dataDir, "t1")
	if err != nil || len(recs) != 0 {
		t.Errorf("只读步骤的清单应为空，实际 %+v err=%v", recs, err)
	}
}

// TestSnapshot_KeepsReasonWhenItCannot 留不住内容时必须说清为什么，而不是安静地少一行。
//
// 三种留不住（超大、二进制、目录）在界面上是三种不同的解释：用户看到"不可还原"
// 的第一个问题永远是"为什么不可还原"。
func TestSnapshot_KeepsReasonWhenItCannot(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	big := filepath.Join(ws, "big.txt")
	if err := os.WriteFile(big, make([]byte, snapshotMaxBytes+10), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(ws, "raw.bin")
	if err := os.WriteFile(bin, append([]byte("head\x00tail"), make([]byte, 32)...), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ws, "folder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	all := []string{big, bin, dir}
	write := &funcTool{name: "wthree", perm: types.PermissionUserApproved,
		paths: func(map[string]any) []string { return all },
		fn:    func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil }}
	e := snapExecutor(t, dataDir, ws, write)
	e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "wthree"}}}, "t1", string(types.ModeAuto), false)

	recs, err := taskPreImages(dataDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("三个路径都该有记录（记录本身不占内容额度）：%+v", recs)
	}
	byPath := map[string]preImage{}
	for _, r := range recs {
		byPath[filepath.Clean(r.Path)] = r
	}
	wants := []struct {
		path  string
		says  string // 原因里该出现的词：界面对用户说的就是这一句
		bytes int64
	}{
		{big, "上限", snapshotMaxBytes + 10},
		{bin, "二进制", 0},
		{dir, "目录", 0},
	}
	for _, w := range wants {
		r, ok := byPath[filepath.Clean(w.path)]
		if !ok {
			t.Fatalf("路径 %s 没有记录", w.path)
		}
		if r.File != "" {
			t.Errorf("%s 留住了内容（%s），本该只记体量", w.path, r.File)
		}
		if r.reversible() {
			t.Errorf("%s 被判成可还原，实际没留住内容：%+v", w.path, r)
		}
		if !strings.Contains(r.Reason, w.says) {
			t.Errorf("%s 的原因里该有 %q，实得 %q", w.path, w.says, r.Reason)
		}
		if w.bytes > 0 && r.Bytes != w.bytes {
			t.Errorf("%s 的体量该照记：%d，应为 %d", w.path, r.Bytes, w.bytes)
		}
	}
}

// TestSnapshot_NonAbsoluteArgSkipped 工具解析失败时原样回吐参数值，那种路径不该被当成快照目标。
func TestSnapshot_NonAbsoluteArgSkipped(t *testing.T) {
	ws, dataDir := t.TempDir(), t.TempDir()
	write := &funcTool{name: "wrel", perm: types.PermissionUserApproved,
		paths: func(map[string]any) []string { return []string{"relative/thing.txt", ""} },
		fn:    func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil }}
	e := snapExecutor(t, dataDir, ws, write)
	e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "wrel"}}}, "t1", string(types.ModeAuto), false)
	if recs, _ := taskPreImages(dataDir, "t1"); len(recs) != 0 {
		t.Errorf("非绝对路径不该留快照：%+v", recs)
	}
}

// TestSnapshot_NoDataDirDoesNotBreakRun 没配数据目录时任务照常跑完，只是没有保险。
//
// 快照是保险不是前提：为了留后路而让任务跑不完，是拿"能不能干活"换"能不能回头"。
func TestSnapshot_NoDataDirDoesNotBreakRun(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "x.txt")
	if err := os.WriteFile(target, []byte("旧"), 0o644); err != nil {
		t.Fatal(err)
	}
	write := &funcTool{name: "wnodir", perm: types.PermissionUserApproved,
		paths: func(map[string]any) []string { return []string{target} },
		fn:    func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil }}
	e := snapExecutor(t, "", ws, write)
	res := e.Execute(context.Background(), types.Plan{Steps: []types.Step{{ID: "s1", Tool: "wnodir"}}}, "t1", string(types.ModeAuto), false)
	if res.Succeeded != 1 {
		t.Fatalf("未配置数据目录不该让步骤失败：%+v", res.Steps)
	}
	if len(res.PreImages) != 0 {
		t.Errorf("没有数据目录就没有快照，实得 %d 条", len(res.PreImages))
	}
}

// TestFirstPreImageOf_TakesEarliest 同一路径被写三次时，还原要退到**任务开始前**。
//
// 斜杠与冗余分隔符不同的两种写法必须算同一个路径，否则会出现"清单里明明有，
// 却回答不在清单里"——那是一条用户没做错却被拒的还原。
func TestFirstPreImageOf_TakesEarliest(t *testing.T) {
	ws := filepath.Clean(t.TempDir())
	target := filepath.Join(ws, "a", "b.txt")
	recs := []preImage{
		{Seq: 1, StepID: "s1", Tool: "file.write", Path: target, Exists: true, Bytes: 10, File: "1.pre"},
		{Seq: 2, StepID: "s2", Tool: "file.write", Path: target, Exists: true, Bytes: 20, File: "2.pre"},
		{Seq: 3, StepID: "s3", Tool: "file.write", Path: target, Exists: true, Bytes: 30, File: "3.pre"},
	}
	got, ok := firstPreImageOf(recs, ws+"//a/b.txt")
	if !ok {
		t.Fatal("同一个路径换了斜杠写法就不认识了")
	}
	if got.Seq != 1 || got.Bytes != 10 {
		t.Errorf("应取最早那条，实得 %+v", got)
	}
	if _, ok := firstPreImageOf(recs, filepath.Join(ws, "c.txt")); ok {
		t.Error("不在清单里的路径不能认识")
	}
}

// TestPreImageGroup_PairsMove 一次移动按组成对还原。
//
// 只退一端等于把文件留在半路上：还源端是凭空删掉整份内容，还目标端是凭空多出一份。
func TestPreImageGroup_PairsMove(t *testing.T) {
	ws := t.TempDir()
	src := filepath.Join(ws, "src.txt")
	dst := filepath.Join(ws, "dst.txt")
	recs := []preImage{
		{Seq: 1, StepID: "s1", Tool: "file.write", Path: src, Exists: true, Bytes: 3, File: "1.pre"},
		{Seq: 2, StepID: "s9", Tool: "file.move", Path: src, Exists: true, Bytes: 4, File: "2.pre"},
		{Seq: 3, StepID: "s9", Tool: "file.move", Path: dst, Exists: false},
	}
	group := preImageGroup(recs, dst)
	if len(group) != 2 {
		t.Fatalf("移动的目标端该带出两个路径：%+v", group)
	}
	if group[0].Path != src || group[1].Path != dst {
		t.Errorf("组内该是源端与目标端各一条，实得 %+v / %+v", group[0], group[1])
	}
	if group[0].Seq != 1 {
		// 源端取的是**本任务开始前**那条（seq=1），不是"移动之前"那条（seq=2）：
		// 同一路径被这个任务写过再移走，还原的口径始终是"退到任务开始之前"。
		t.Errorf("源端该取本任务最早那条快照：%+v", group[0])
	}
	if only := preImageGroup(recs, filepath.Join(ws, "src.txt")); len(only) != 1 || only[0].Seq != 1 {
		t.Errorf("普通写入不 pairing，应只回自己（且取最早）：%+v", only)
	}
}

// TestRestorePreImage_ThreeShapes 还原的三种形状：退回内容 / 删掉造出的 / 空目录才敢删。
func TestRestorePreImage_ThreeShapes(t *testing.T) {
	dataDir, ws := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(SnapshotRoot(dataDir), "t1"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1) 覆盖写：退回去
	old := filepath.Join(ws, "old.txt")
	if err := os.WriteFile(old, []byte("新写的"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(SnapshotRoot(dataDir), "t1", "1.pre"), []byte("写前的样子"), 0o600); err != nil {
		t.Fatal(err)
	}
	note, err := restorePreImage(dataDir, "t1", preImage{Seq: 1, Path: old, Exists: true, Bytes: 15, File: "1.pre", Mode: 0o644})
	if err != nil {
		t.Fatalf("退回内容失败：%v", err)
	}
	if !strings.Contains(note, "任务开始前") {
		t.Errorf("说明里该讲清退到了哪个时刻：%q", note)
	}
	b, _ := os.ReadFile(old)
	if string(b) != "写前的样子" {
		t.Errorf("还原后内容 = %q", b)
	}

	// 2) 任务造出来的文件：删掉它
	created := filepath.Join(ws, "created.txt")
	if err := os.WriteFile(created, []byte("新东西"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := restorePreImage(dataDir, "t1", preImage{Seq: 2, Path: created}); err != nil {
		t.Fatalf("删掉任务造出的文件失败：%v", err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Error("文件应已被删掉")
	}
	// 再点一次还原：本来就是空的，如实说"无需还原"，不报错也不假装做了事
	again, err := restorePreImage(dataDir, "t1", preImage{Seq: 2, Path: created})
	if err != nil || !strings.Contains(again, "无需") {
		t.Errorf("重复还原应是无操作，实得 note=%q err=%v", again, err)
	}

	// 3) 非空目录：不删。递归删下去就不是"还原"而是"顺手清盘"
	dir := filepath.Join(ws, "outdir")
	if err := os.MkdirAll(filepath.Join(dir, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := restorePreImage(dataDir, "t1", preImage{Seq: 3, Path: dir}); err == nil {
		t.Error("非空目录必须报错，不能悄悄删掉里面的东西")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("报错不该顺手删掉目录：%v", err)
	}
	empty := filepath.Join(ws, "emptydir")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	note, err = restorePreImage(dataDir, "t1", preImage{Seq: 4, Path: empty})
	if err != nil || !strings.Contains(note, "空目录") {
		t.Errorf("空目录该删掉并说明，实得 %q err=%v", note, err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Error("空目录应已被删掉")
	}
}

// TestRestorePreImage_KeepsModeBits 还原要连可执行位一起退回去。
//
// 内容对了但跑不起来，在用户眼里就是"还原把它弄坏了"。
func TestRestorePreImage_KeepsModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的权限位只有只读位，测不出可执行位的保留")
	}
	dataDir, ws := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(SnapshotRoot(dataDir), "t1"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(ws, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho now-rwx\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(SnapshotRoot(dataDir), "t1", "1.pre"), []byte("#!/bin/sh\necho old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := preImage{Seq: 1, Path: script, Exists: true, Bytes: 22, File: "1.pre", Mode: uint32(0o755)}
	if _, err := restorePreImage(dataDir, "t1", rec); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("还原后权限位 = %v，应保留写前的 0755", info.Mode().Perm())
	}
}

// TestTaskPreImages_BlocksTraversal 任务 ID 会被拼进文件路径，穿越形态必须在读盘之前就被拒。
func TestTaskPreImages_BlocksTraversal(t *testing.T) {
	dataDir := t.TempDir()
	for _, hostile := range []string{"../../settings", "..", "a/b", "", "   ", string(os.PathSeparator)} {
		if _, err := taskPreImages(dataDir, hostile); err == nil {
			t.Errorf("taskID=%q 应被拒（数据目录下就有含密钥的文件）", hostile)
		}
	}
	if _, err := taskPreImages("", "t1"); err == nil {
		t.Error("未配置数据目录时应报错而不是去读相对路径")
	}
}

// TestPruneSnapshots_KeepsNewest 快照是**保险不是堆积**：按任务数淘汰最旧的。
func TestPruneSnapshots_KeepsNewest(t *testing.T) {
	dataDir := t.TempDir()
	root := SnapshotRoot(dataDir)
	for i := 0; i < 5; i++ {
		dir := filepath.Join(root, "task"+string(rune('a'+i)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(i-5) * time.Hour)
		if err := os.Chtimes(dir, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	PruneSnapshots(dataDir, 2)
	left, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("应保留 2 个任务快照目录，实际 %d：%v", len(left), names(left))
	}
	if _, err := os.Stat(filepath.Join(root, "taske")); err != nil {
		t.Error("最新那个任务（刚跑完的）必须留下")
	}
	PruneSnapshots(filepath.Join(dataDir, "never"), 3) // 目录不存在不能崩
	PruneSnapshots("", 3)                              // 没配数据目录也不能崩
}
