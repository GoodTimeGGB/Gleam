// Package desktop 提供跨平台「打开/关闭/查询主窗口」能力。
//
// 设计说明（为什么不内嵌 WebView2）：
//   - 在同一进程内混合 WebView2 与自建托盘消息循环极易导致消息泵死锁/卡死（Windows "未响应"）；
//   - Gleam 本身是本地 HTTP 服务，渲染完全交给成熟的浏览器内核（Edge/Chrome --app 模式），
//     浏览器崩溃不影响主服务与托盘；这也是 Slack/Discord 等产品的稳定方案。
//   - exe 文件图标、任务栏图标、窗口标题由启动参数控制，观感近似原生应用。
package desktop

// OpenWindow 打开（或激活）主应用窗口。
// 返回 true 表示成功启动/激活了一个窗口；false 表示无可用浏览器。
// onClose 在窗口关闭时调用（不代表进程退出，仅窗口隐藏/关闭）。
func OpenWindow(url string, onClose func()) bool {
	return openWindowPlatform(url, onClose)
}

// CloseWindow 请求关闭所有由本进程打开的应用窗口。
func CloseWindow() { closeWindowPlatform() }

// WindowOpen 报告是否存在处于打开状态的应用窗口。
func WindowOpen() bool { return windowOpenPlatform() }

// OpenURL 用系统默认浏览器打开外部链接（如第三方登录授权页）。
func OpenURL(rawURL string) error { return openURLPlatform(rawURL) }
