package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gleam/internal/tools/toolutil"
)

// 超长工具输出的落盘缓存。
//
// **为什么需要它**：`fitRunes` 的截断是**有损**的——头 3/5 + 尾 2/5 看起来照顾了
// 两端信息量最高的部分，但被丢掉的中段可能正是关键（一份两万字日志里的第 1 万行）。
// 原来的兜底是"让模型自己再调 `file.read` 按范围取"，而那要求模型**先意识到自己缺了什么**——
// 它只看到首尾，恰恰意识不到中间有什么。
//
// **这是缓存不是归档**（A23）：任务结束后按与任务记录同样的保留上限清理。
// 所以提示语里必须写明"临时文件"，否则模型会把它当持久产物引用，
// 而那个文件在几轮之后就不在了。

// spillDirName 落盘根目录名（在 DataDir 下）。
const spillDirName = "tool-output"

// SpillRoot 超长工具输出的根目录；未配置数据目录时返回空串。
func SpillRoot(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	return filepath.Join(dataDir, spillDirName)
}

// spillMaxTasks 保留的任务目录数上限。
//
// 与 `maxRetainedTasks`（任务记录保留上限）取同一个数量级、同一个意图：
// 能查最近的任务就够了，再往前的既没人看、也不该占磁盘。
const spillMaxTasks = 200

// spillMaxSegLen 单层路径名的长度上限（防止用超长 ID 撞文件系统限制）。
const spillMaxSegLen = 64

// safeSeg 把一段可能来自模型的文本变成安全的**单层**路径名。
//
// **为什么必须做**：`step.ID` 直接来自模型输出的计划 JSON，而计划校验只查了
// 「非空」与「不重复」（`planner.go:582`），**没有做路径安全**。所以
// `"id": "../../../x"` 这种值是能一路走到这里的，直接 `filepath.Join` 出去
// 就会写到数据目录之外——而且这是**写**，不是读。
//
// 做法是白名单：只保留字母数字与 `-_.`，其余一律替换成 `_`；再去掉首尾的点
// （挡掉 `.` 与 `..`），空结果兜底成 `_`。白名单而不是黑名单：黑名单永远漏。
func safeSeg(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			// 中文等非 ASCII 一律替换：路径要跨平台可读，不值得为文件名保留它们。
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if len(out) > spillMaxSegLen {
		out = out[:spillMaxSegLen]
	}
	out = strings.Trim(out, ".")
	if out == "" {
		return "_"
	}
	return out
}

// SpillOutput 把超长输出的**完整原文**写到 `<DataDir>/tool-output/<taskID>/<stepID>.txt`，
// 返回写入路径。
//
// 路径同时过两道：`safeSeg` 保证单层、不越界；`toolutil.ResolveInRoots` 与工具读写
// 走**同一套**边界校验。两套校验一定会漂移，而漂移的方向通常是「新加的那条更松」——
// 所以宁可在已经安全的地方再走一遍共用的那条。
func SpillOutput(dataDir, taskID, stepID, content string) (string, error) {
	root := SpillRoot(dataDir)
	if root == "" {
		return "", fmt.Errorf("未配置数据目录")
	}
	dir := filepath.Join(root, safeSeg(taskID))
	path := filepath.Join(dir, safeSeg(stepID)+".txt")
	if _, err := toolutil.ResolveInRoots(path, []string{root}); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// PruneSpill 清理落盘缓存：按修改时间保留最新的 keep 个任务目录，其余删除。
//
// 失败一律忽略：这是缓存，清理不掉最多多占点磁盘，**不能因此让任务失败**。
// 一个"为了省磁盘把任务弄挂"的清理逻辑，比不清理更坏。
func PruneSpill(dataDir string, keep int) {
	root := SpillRoot(dataDir)
	if root == "" || keep <= 0 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type dirInfo struct {
		path string
		mod  time.Time
	}
	dirs := make([]dirInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, dirInfo{path: filepath.Join(root, e.Name()), mod: info.ModTime()})
	}
	if len(dirs) <= keep {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	for _, d := range dirs[keep:] {
		_ = os.RemoveAll(d.path)
	}
}
