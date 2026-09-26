// taskstore_test.go 任务归档的唯一出口：写成什么样、什么时候才准删运行日志、
// 越界的 task_id 一个字节都不许落盘。
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gleam/pkg/types"
)

func archivedResult(taskID string) *types.GoalResult {
	return &types.GoalResult{
		TaskID:  taskID,
		Goal:    "把会议记录整理成纪要",
		Status:  types.GoalSuccess,
		Summary: "已生成纪要",
	}
}

// 归档写下之后才删运行日志：顺序反过来，一次写盘失败就会把运行中唯一的凭据一起带走。
func TestSaveTaskResult_WritesThenDiscardsRunLog(t *testing.T) {
	dir := t.TempDir()
	NewRunLog(dir).RecordStep("t-1", step("s1", types.StepSucceeded))
	if err := SaveTaskResult(dir, archivedResult("t-1")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tasks", "t-1.json")
	if raw, err := os.ReadFile(path); err != nil {
		t.Fatalf("归档没写下: %v", err)
	} else if !strings.Contains(string(raw), `"task_id": "t-1"`) {
		t.Errorf("归档内容不含 task_id: %.80s", raw)
	}
	if _, err := os.Stat(RunLogPath(dir, "t-1")); !os.IsNotExist(err) {
		t.Error("快照已写成，运行日志应被删掉")
	}
	// 原子写不留临时文件：残留说明改名前就退出了
	residue, _ := filepath.Glob(filepath.Join(dir, "tasks", "*.tmp*"))
	if len(residue) != 0 {
		t.Errorf("tasks/ 留下临时文件: %v", residue)
	}
}

// 越界的 task_id 必须**一个字节都不落**：数据目录下放着含密钥的 settings.json，
// 而 task_id 在 JSON-RPC 的 goal/submit 里是调用方给的。
func TestSaveTaskResult_UnsafeIDWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ids := []string{"../../escape", "a/b", `..\b`, "", ".", "..", strings.Repeat("x", 200)}
	for _, id := range ids {
		if err := SaveTaskResult(dir, archivedResult(id)); err == nil {
			t.Errorf("taskID %q 应被拒绝", id)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("非法 id 在数据目录里留下了东西: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.json")); err == nil {
		t.Error("写到了数据目录之外")
	}
}

// TaskArchivePath 不拼越界路径（读侧同样靠它挡穿越）。
func TestTaskArchivePath_RejectsUnsafe(t *testing.T) {
	for _, id := range []string{"../bad", "a/b", "", "中文", "x.json"} {
		if p := TaskArchivePath("/data", id); p != "" {
			t.Errorf("TaskArchivePath(%q) = %q，应为空", id, p)
		}
	}
	if p := TaskArchivePath("/data", "t-1"); !strings.HasSuffix(filepath.ToSlash(p), "/tasks/t-1.json") {
		t.Errorf("合法 id 的路径 = %q", p)
	}
}

// 写不成时，运行日志必须**还在**。
//
// 这条测的是顺序，不是结果：先删日志再写快照，一次写盘失败就会把运行中唯一的凭据一起
// 带走——那正是它最该被保留的时候。让 MkdirAll 失败（tasks 被占成一个普通文件）
// 是最便宜的注入方式：不碰权限，Windows 上也一样。
func TestSaveTaskResult_FailedWriteKeepsRunLog(t *testing.T) {
	dir := t.TempDir()
	NewRunLog(dir).RecordStep("t-1", step("s1", types.StepSucceeded))
	if err := os.WriteFile(filepath.Join(dir, "tasks"), []byte("占位"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveTaskResult(dir, archivedResult("t-1")); err == nil {
		t.Fatal("tasks 被占成文件，归档应报错")
	}
	if _, err := os.Stat(RunLogPath(dir, "t-1")); err != nil {
		t.Errorf("归档没写成，运行日志却没了: %v", err)
	}
}

// 读侧：没写过、写坏了、文件名与内容不符，是**三件不同的事**。
// 前一个返回 (nil, nil)（常态），后两个必须报错——把它们混成"不存在"，
// 用户就会反复重跑那次任务，而真正的问题是归档文件本身读不动。
func TestReadTaskResult_MissingBrokenMismatch(t *testing.T) {
	dir := t.TempDir()
	if g, err := ReadTaskResult(dir, "t-1"); err != nil || g != nil {
		t.Fatalf("缺归档应返回 (nil, nil)，得到 %v / %v", g, err)
	}
	if err := SaveTaskResult(dir, archivedResult("t-1")); err != nil {
		t.Fatal(err)
	}
	g, err := ReadTaskResult(dir, "t-1")
	if err != nil || g == nil || g.Goal == "" {
		t.Fatalf("读回失败: %v / %v", g, err)
	}

	path := TaskArchivePath(dir, "t-1")
	if err := os.WriteFile(path, []byte(`{"task_id": "t-1", "goa`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTaskResult(dir, "t-1"); err == nil {
		t.Error("半截 JSON 应报错，而不是当成没有")
	}

	// 文件名 t-1 里装着 t-2 的内容
	if err := SaveTaskResult(dir, archivedResult("t-2")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(TaskArchivePath(dir, "t-2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTaskResult(dir, "t-1"); err == nil {
		t.Error("文件名与内容不符应报错")
	}
}

// TestListTaskResults_OrderLimitSkip 列表要按"最近优先"给出，且坏文件不能带走整页。
//
// 界面在重启后问的是"我跑过哪些任务"，而内存表那时是空的：这个函数就是那份历史。
// 一条读不动的归档只该让那一条消失——让它把整页打翻，用户看到的就是"记录全没了"。
func TestListTaskResults_OrderLimitSkip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i, id := range []string{"t-old", "t-mid", "t-new"} {
		g := archivedResult(id)
		g.StartedAt = now.Add(time.Duration(i) * time.Hour)
		if err := SaveTaskResult(dir, g); err != nil {
			t.Fatal(err)
		}
	}
	list, skipped, err := ListTaskResults(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 {
		t.Errorf("干净目录不该有跳过：%d", skipped)
	}
	if len(list) != 3 || list[0].TaskID != "t-new" || list[2].TaskID != "t-old" {
		t.Fatalf("顺序应 t-new, t-mid, t-old，实得 %v", list)
	}

	// limit 是从最新往回数，不是先截断再排序
	list, _, err = ListTaskResults(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].TaskID != "t-new" {
		t.Errorf("limit=2 应给最新两条，实得 %v", list)
	}

	// 塞一条坏归档：列表少一条并报出数量，其余照常
	if err := os.WriteFile(filepath.Join(dir, "tasks", "broken.json"), []byte("{半截"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, skipped, err = ListTaskResults(dir, 10)
	if err != nil {
		t.Fatalf("单条坏文件不该让整个列表失败：%v", err)
	}
	if skipped != 1 {
		t.Errorf("坏归档应计 1 条，实得 %d", skipped)
	}
	if len(list) != 3 {
		t.Errorf("坏归档之外的记录都该在，实得 %v", list)
	}
}

// TestListTaskResults_NoDirIsNotAnError 从没跑过任务时 tasks/ 可能压根不存在。
//
// 这不是异常状态，而是每个新用户的第一次——报成错误会让界面显示"读取失败"，
// 而正确答复是"还没有任务"。
func TestListTaskResults_NoDirIsNotAnError(t *testing.T) {
	list, skipped, err := ListTaskResults(filepath.Join(t.TempDir(), "nowhere"), 10)
	if err != nil {
		t.Fatalf("目录不存在应回空，不该报错：%v", err)
	}
	if len(list) != 0 || skipped != 0 {
		t.Errorf("应回空列表，实得 %v / %d", list, skipped)
	}
}
