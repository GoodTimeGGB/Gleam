// scope.go 把「本次调用受哪几个根目录约束」放进 context。
//
// 为什么需要它：worktree 接入执行之后，同一个进程里不同的任务有不同的文件边界
// （任务 A 在自己的 worktree 里，任务 B 在工作区里）。而工具实例是**共享**的——
// 把边界写进实例字段，等于多任务共用一支笔：谁先开跑谁把笔换了，另一个还在写。
// ctx 天生 per-task，边界跟着它走才不会互相踩。
//
// 为什么不干脆把 roots 加进每个 Execute 的签名：file / shell / git 三处都要判边界，
// 参数化要改三套签名与全部调用点，而它们本来就都收 ctx。
package toolutil

import "context"

type rootsKey struct{}

// WithRoots 把本次任务的工作区边界放进 ctx。
//
// 空切片视为「没说」而不是「没有边界」：调用方拿到空 roots 时应当保持原样，
// 让下游回落到静态配置的那份。真正的「没有边界」由 ResolveInRoots 拒绝（空 roots 一律拒）。
func WithRoots(ctx context.Context, roots []string) context.Context {
	if ctx == nil || len(roots) == 0 {
		return ctx
	}
	out := make([]string, len(roots))
	copy(out, roots)
	return context.WithValue(ctx, rootsKey{}, out)
}

// RootsFrom 取本次调用的边界；ctx 里没写就回落到 fallback（工具构造时的那份）。
//
// 回落是刻意的：ctx 里没有边界，说明这次调用不在某个任务的执行链上
// （例如界面上单发一次「这一步要不要批准」的预览），那种时候全局工作区就是事实。
func RootsFrom(ctx context.Context, fallback []string) []string {
	if ctx != nil {
		if v, ok := ctx.Value(rootsKey{}).([]string); ok && len(v) > 0 {
			return v
		}
	}
	return fallback
}
