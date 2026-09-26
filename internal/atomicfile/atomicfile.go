// Package atomicfile 落地"写完才算数"这一条：先写同目录临时文件，再 rename 覆盖。
//
// 为什么值得单独一层：直接 os.WriteFile 的失败形态不是"这次没保存"，而是**半截文件**。
// 进程中途被杀、磁盘写满、杀毒软件插手，都会留下截断的 JSON；下一次启动解析失败，
// 用户丢的不是正在写的这一条，而是整个文件里的全部记录（成长日志、会话、任务结果）。
// 而这些文件恰恰是最不该丢的那类——它们就是用户在这台机器上攒下来的东西。
//
// rename 在同目录内是原子的：要么看到旧内容，要么看到新内容，不会看到中间态。
// 临时文件用 CreateTemp 而不是固定 path+".tmp"：两处并发写同一目标时，
// 固定名字会互相写坏对方的临时文件。
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write 原子写入 path。perm 申请文件权限（Windows 上只体现只读位，与 os.WriteFile 一致）。
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		// 刷不下去就不覆盖：宁可报"这次没保存"，也不要用一个可能只落到缓存的内容
		// 去换掉一份本来完好的文件。
		cleanup()
		return fmt.Errorf("落盘失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("关闭失败: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("设置权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("替换目标文件失败: %w", err)
	}
	return nil
}
