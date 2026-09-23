// promptcache.go 提示词前缀缓存的辅助工具。
//
// 原理：主流厂商都在服务端做前缀缓存（KV cache 复用）——把「多次请求共有的开头」
// 的注意力中间态留在显存里，下次遇到同样的开头就不必重算，命中的输入 token 按折扣计价。
// 关键约束是**必须逐字节相同**：只要有一个字符不同，从该处往后的全部内容都算未命中。
//
// 推论（本项目提示词布局的依据）：把易变内容（时间、工作目录、摘要、历史对话）
// 夹在稳定内容中间，会让最长公共前缀缩到很短，缓存几乎全废；
// 稳定内容必须**连续地排在最前面**，易变内容统一沉到后面。
package llm

import "unicode/utf8"

// CacheFriendlyMinChars 值得为之优化缓存布局的最小提示词长度（字符）。
// 各厂商的可缓存前缀下限多在 1024 token 量级；中文约 1 字 1 token，
// 因此稳定段短于这个量级时，纠结前缀一致性的收益有限。
const CacheFriendlyMinChars = 1200

// SplitCacheBoundary 把两个提示词切成「稳定段」与各自的「易变段」：
// 稳定段是两次请求仍然逐字节一致的开头，易变段是其后的全部内容。
// 用它量化"两次请求能共用多少前缀"——这是提示词布局是否缓存友好的直接指标。
func SplitCacheBoundary(a, b string) (stable, restA, restB string) {
	n := 0
	for n < len(a) && n < len(b) {
		ra, sa := utf8.DecodeRuneInString(a[n:])
		rb, _ := utf8.DecodeRuneInString(b[n:])
		if ra != rb {
			break
		}
		n += sa
	}
	return a[:n], a[n:], b[n:]
}

// StablePrefixLen 返回两个字符串的公共前缀长度（字符数，不切断多字节字符）。
func StablePrefixLen(a, b string) int {
	stable, _, _ := SplitCacheBoundary(a, b)
	return utf8.RuneCountInString(stable)
}
