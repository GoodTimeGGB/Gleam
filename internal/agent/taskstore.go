package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/atomicfile"
	"gleam/pkg/types"
)

// TaskArchivePath 任务终态快照的路径（tasks/<taskID>.json）。taskID 不能作文件名时返回空。
func TaskArchivePath(dataDir, taskID string) string {
	name := SafeTaskName(taskID)
	if name == "" {
		return ""
	}
	return filepath.Join(dataDir, "tasks", name+".json")
}

// SaveTaskResult 写一次任务的终态快照，写成功之后才丢掉运行日志。
//
// 为什么只留这一个出口：CLI（gleam goal）、常驻服务（gleam serve / webui）与调度器
// 每次跑完都要归档。
// 两处各写一遍就会漂移，而"先写快照、写成了再删运行日志"这条顺序恰恰是漂移时最容易丢的那半——
// 反过来的话，一次写盘失败会把运行中唯一的凭据一起带走，那正是它最该被保留的时候。
//
// taskID 不合法就明确报错而不是净化后照写：`a/b` 与 `a_b` 映射到同一个文件，
// 两条任务会互相覆盖，读的人以为自己看的是完整的一次运行。
func SaveTaskResult(dataDir string, result *types.GoalResult) error {
	if result == nil {
		return fmt.Errorf("没有可归档的任务结果")
	}
	if dataDir == "" {
		return fmt.Errorf("未配置数据目录，任务 %s 未归档", result.TaskID)
	}
	path := TaskArchivePath(dataDir, result.TaskID)
	if path == "" {
		return fmt.Errorf("任务 ID %q 不能作文件名，未归档", result.TaskID)
	}
	b, err := json.MarshalIndent(result, "", " ")
	if err != nil {
		return fmt.Errorf("序列化任务结果失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := atomicfile.Write(path, b, 0o644); err != nil {
		return err
	}
	DiscardRunLog(dataDir, result.TaskID)
	return nil
}

// ListTaskResults 按时间倒序列出盘上的任务归档，最多 limit 条；第二个返回值是没读动的文件数。
//
// 为什么要"列"而不是只按 id 读：`tasks/` 是任务历史的**唯一**持久载体，
// 常驻服务的内存表既有淘汰上限、重启即空。只按 id 读就等于"知道编号才存在"，
// 而界面上问的是"我跑过哪些"。
//
// 只解析最新的 limit 个（按修改时间挑），不是全部：归档会一路涨到几千条，
// 而"最近任务"要的是最新那几十条——全读会把一次列表请求变成几千次解析。
//
// 单个文件读不动只跳过并计数，不让整个列表失败：坏归档是**一条**历史的损失，
// 让它把"最近任务"整页打翻，用户看到的就是"我的记录全没了"。
func ListTaskResults(dataDir string, limit int) ([]*types.GoalResult, int, error) {
	if limit <= 0 {
		limit = 50
	}
	dir := filepath.Join(dataDir, "tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	type entry struct {
		id  string
		mod time.Time
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{strings.TrimSuffix(e.Name(), ".json"), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]*types.GoalResult, 0, limit)
	skipped := 0
	for _, f := range files {
		if len(out) >= limit {
			break
		}
		g, err := ReadTaskResult(dataDir, f.id)
		if err != nil || g == nil {
			skipped++
			continue
		}
		out = append(out, g)
	}
	// 归档里的 StartedAt 才是"这次跑的次序"，mtime 只用来挑最新的一批
	// （复制过来的档案、被人手改过时间的文件，两者会不一致）。
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, skipped, nil
}

// ReadTaskResult 读回一份任务终态快照。
//
// 「没有这条归档」返回 (nil, nil) 而不是错误：任务没跑完、跑完没归档、归档被清理，
// 都是**常态**，把它做成错误会让每个调用点都去绕开一次预期中的失败。
// 但**读到了却解析不动**是真错误，必须报出来——那是文件坏了，不是没写过。
//
// 读侧也校验 `TaskID` 与文件名一致：不一致说明这份归档是被人手改过或放错了位置，
// 按 id 返回一条别人的结果比报"读不到"更坏。taskID 不能作文件名时同样视为没有。
func ReadTaskResult(dataDir, taskID string) (*types.GoalResult, error) {
	path := TaskArchivePath(dataDir, taskID)
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var g types.GoalResult
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("解析任务记录失败（%s）: %w", path, err)
	}
	if g.TaskID != taskID {
		return nil, fmt.Errorf("任务记录内容与文件名不符（%s 里写的是 %q）", path, g.TaskID)
	}
	return &g, nil
}
