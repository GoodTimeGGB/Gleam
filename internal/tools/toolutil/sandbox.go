// sandbox.go 工作区边界的共享校验。
//
// 为什么抽出来：文件工具与 shell 工具都必须在同一套边界语义下工作——
// 两条路径各写一份 within，就会出现"文件工具挡住了、shell 绕过去了"这种缝。
// 边界只有一处实现，才谈得上纵深防御。
package toolutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Within 判断 path 是否落在 root 之内（含 root 自身）。
// 用 filepath.Rel 而不是字符串前缀：前缀比较会把 /ws2 误判成 /ws 的子路径。
func Within(root, path string) bool {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// ResolveInRoots 解析路径并校验它落在某个根目录内，返回绝对路径。
//
// 语义与文件工具一致：
//   - 绝对路径必须位于某个根目录下；
//   - 相对路径基于第一个根目录；
//   - 解析软链接后再判定（工作区里的一个软链不能把读写引到外面去）。
//
// roots 为空时一律拒绝：**失败要朝着安全的方向失败**——
// 没配边界就放行，等于把"忘了配置"变成一次越权。
func ResolveInRoots(p string, roots []string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("路径为空")
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("未配置允许的根目录，拒绝访问 %q", p)
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(roots[0], p))
	}
	for _, root := range roots {
		checkPath := abs
		// 解析已存在的部分：软链接不能成为越界通道
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			checkPath = real
		} else if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			checkPath = filepath.Join(parent, filepath.Base(abs))
		}
		if Within(root, checkPath) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("路径 %q 超出允许的工作区范围", p)
}
