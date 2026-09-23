package llm

import (
	"net/url"
	"sync"
)

// EgressFunc 出网留痕回调：每次向模型服务端发起请求时调用一次。
//
// 参数只有目标主机与请求体字节数——**没有内容**。这条边界由签名保证：
// 调用方手里没有正文可传，也就不可能把它写进审计（见 safety.AuditEntry.Egress）。
type EgressFunc func(host string, nbytes int)

// egressHook 进程级出网留痕钩子，装配时设置一次（buildRuntime），此后只读。
//
// 为什么用包级变量而不是给三个协议客户端各加一个字段：
// 出网路径已经收敛到 httputil.go 的两个函数与 glm.go 的 do()，装配点也只有一处；
// 再往下逐层传一遍纯属管道工程，而且以后新增协议客户端时必然漏掉一个。
// 代价是它是全局状态——所以只允许在装配阶段写，运行期只读。
var (
	egressMu   sync.RWMutex
	egressHook EgressFunc
)

// SetEgressHook 装上出网留痕钩子；传 nil 卸载。
func SetEgressHook(fn EgressFunc) {
	egressMu.Lock()
	egressHook = fn
	egressMu.Unlock()
}

// noteEgress 记一次出网。URL 解析不出主机名时退化成原样记录，不影响请求本身——
// 审计是尽力而为的旁路，不能因为它失败而让模型调用失败。
func noteEgress(rawURL string, nbytes int) {
	egressMu.RLock()
	fn := egressHook
	egressMu.RUnlock()
	if fn == nil {
		return
	}
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = u.Host
	}
	fn(host, nbytes)
}
