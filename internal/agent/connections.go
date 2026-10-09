// Package agent 里的这一管负责一件事：把"这台机器上有哪些边界"列成一张台账。
//
// 为什么不做成"连接器市场"：别的桌面 Agent 那一屏回答的是"我还能连什么"，
// 而 Gleam 早已把实体散在 MCP / 工具 / 技能三层里——再加一屏画廊只是给同一批事实找第二个家，
// 而且那个家的朝向是"劝你多装"。这一层反过来回答：**这条线通了之后，谁被允许动我的什么**。
package agent

import (
	"fmt"
	"strings"

	"gleam/internal/harness/safety"
	"gleam/internal/llm"
)

// egressKinds 出网落点清单，**这一份是 owner**。
//
// 闸门 scripts/check-egress-owner.py 拿它与全仓所有 RecordEgress 调用点的 kind 字面量
// 双向对账（读源码前会先剥掉注释，免得注释里提一句这个函数就凭空多出一个落点）：
// 新接一条出网路径忘了在这里交代 → 红；在这里列了一条代码里没人记的出网 → 也红。
// 之所以值得为它写一道闸门：本地优先的产品最坏的失效不是"连得少"，而是
// **"连了而没交代"**——那句话在界面上看起来和"没连"一模一样。
//
// 台账行的 kind 与本清单必须一致，这条由 TestConnections_EveryEgressKindHasARow 在运行时判
// （闸门只能读源码，读不出"这一行今天到底画没画出来"）。
var egressKinds = []string{"llm", "web.fetch", "cloud", "feedback", "update", "go.toolchain"}

// EgressKinds 出网落点清单（供测试与自述用；改这份的同时必须改台账行）。
func EgressKinds() []string { return append([]string(nil), egressKinds...) }

// ConnectionRow 台账上的一条边界。
//
// 字段全是**已存在的事实**：目标来自配置与注册表，读数来自门控留痕，状态来自运行时。
// 这里没有任何一个手写的"能力宣传语"——一张会自己长出新行的表才有资格说"这就是全部"。
type ConnectionRow struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Kind     string `json:"kind"`     // out | in | local
	KindText string `json:"kindText"` // 出网 | 入网 | 本机（显示口径也归这里，前端不再各写一份映射）
	Target   string `json:"target"`   // 通到哪
	Status   string `json:"status"`   // 现在到底是不是通的
	Trigger  string `json:"trigger"`  // 谁能让它发生
	Leaves   string `json:"leaves"`   // 会离开本机（或被它触及）的东西
	Trace    string `json:"trace"`    // 留痕在哪、怎么回看
	Untraced bool   `json:"untraced"` // 关键动作没有留痕：界面必须把它渲染成警示，而不是沉默
	Off      string `json:"off"`      // 想关掉或收紧，动哪里
	Stats    string `json:"stats"`    // 实测读数（有留痕的出网行才有）
	Alert    string `json:"alert"`    // 这一行现在值得警惕的一件事
}

// ConnectionLedger 一张台账 + 它的覆盖范围。
type ConnectionLedger struct {
	Rows        []ConnectionRow `json:"rows"`
	Scope       string          `json:"scope"`
	EgressScope string          `json:"egressScope"`
}

// ConnectionView 从真实运行状态派生台账。完全只读：不发模型请求、不写文件、不联网探测。
//
// bindAddr 由接入层给（引擎不知道自己在监听哪个端口，这是宿主的事实）。
// 传空串表示"本机服务这一行拿不到监听地址"——那一行会如实说"未知"，不会假装是回环。
func (a *Agent) ConnectionView(bindAddr string) ConnectionLedger {
	rep := a.Gate.EgressStats()
	scope := egressScopeText(rep, a.Gate.AuditPath())

	rows := []ConnectionRow{a.llmRow(rep), a.webFetchRow(rep)}
	if in := inboundRow(bindAddr); in != nil {
		rows = append(rows, *in)
	}
	rows = append(rows, a.mcpRows()...)
	rows = append(rows, a.cloudRow(rep), a.feedbackRow(rep), updateRow(rep), goToolchainRow(rep), marketRow())

	return ConnectionLedger{
		Rows: rows,
		Scope: "这张表列的是「常驻边界」：配置里就存在、随时可能被动用的那几处。" +
			"登录时临时开的本机回环端口（几分钟后关闭）不在这里。",
		EgressScope: scope,
	}
}

// egressScopeText 读数覆盖范围必须跟着读数走。
//
// 内存环只有最近若干条、重启即清零。界面若只印"N 次 / M 字节"，读者会理解成
// "这台机器上一共发出去多少"——那是把"我不知道"讲成答案（同 §4.6.33 那一类错）。
// 环被写满时（Scanned == Cap）说法必须变，且容量这个数字不许前端自己记。
//
// 第二层：指路只能指向真实存在的东西。没开审计落盘时让人去 `pending --audit`，
// 他查到的"没有记录"会被读成"确实没出过网"，而事实是"这台机器压根没记"。
func egressScopeText(rep safety.EgressReport, auditPath string) string {
	back := "更早的记录在落盘审计里，`gleam pending --audit --egress` 可只看这一类"
	if auditPath == "" {
		back = "这个构建没开审计落盘，内存环之外的事没有记录"
	}
	if rep.Scanned == 0 {
		return "内存里还没有任何出网留痕；" + back + "。"
	}
	if rep.Scanned >= rep.Cap {
		return fmt.Sprintf("下面的读数只数了内存里最近 %d 条留痕（环已写满，更早的已被挤掉；重启清零；%s）",
			rep.Scanned, back)
	}
	return fmt.Sprintf("下面的读数来自本次运行内存里的 %d 条留痕（重启清零；%s）", rep.Scanned, back)
}

// egressStat 取某一类出网落点的实测读数。
func egressStat(rep safety.EgressReport, kind string) (safety.EgressStat, bool) {
	for _, s := range rep.Stats {
		if s.Kind == kind {
			return s, true
		}
	}
	return safety.EgressStat{}, false
}

// egressStatsText 一行出网读数的说法。没有记录要说成"没有记录"，不能留空——
// 空白格子在界面上的读法是"这行不归我管"，而事实是"这台机器至今没往它发过东西"。
func egressStatsText(rep safety.EgressReport, kind string) string {
	st, ok := egressStat(rep, kind)
	if !ok {
		return "本次运行还没有这一类的出网记录"
	}
	hosts := strings.Join(st.Hosts, "、")
	if len(st.Hosts) == 0 {
		hosts = "主机名未知"
	}
	return fmt.Sprintf("已出网 %d 次，涉及 %d 个主机（%s），累计 %s",
		st.Count, len(st.Hosts), hosts, humanBytes(st.Bytes))
}

// humanBytes 读数要说人话；这是显示口径，归后端一处（前端再写一份就会有两个"1.2 KB"）。
func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d 字节", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	}
}

// llmRow 模型服务这一行：本机唯一会把**提示词全文**送出去的边界。
//
// 目标主机取 `llm.KeyScope(cfg.LLM.BaseURL)`——那正是密钥绑定的那把尺（见 §4.6.26），
// 台账与它读同一个数，才不会出现"密钥说是 A、台账写着 B"。
func (a *Agent) llmRow(rep safety.EgressReport) ConnectionRow {
	base := strings.TrimSpace(a.Cfg.LLM.BaseURL)
	host := llm.KeyScope(base)
	r := ConnectionRow{
		ID: "llm", Title: "模型服务", Kind: "out", KindText: "出网",
		Trigger: "每一次规划、执行、反思、验收，以及对话模式下每一条回答",
		Leaves:  "提示词全文：目标、系统提示、工作目录路径、会话历史、工具输出片段",
		Trace:   "门控留痕 action=egress、kind=llm（只记主机与字节，不记正文）",
		Off:     "设置 → 模型：换成模拟模型（provider: mock）就不再发包；密钥只存本机 credentials.json，且只允许发往绑定过的那一个接入主机",
		Stats:   egressStatsText(rep, "llm"),
	}
	if a.Cfg.LLM.Provider == "mock" || host == "" {
		r.Status = "没配真实模型，这条线发不出东西"
		r.Target = "—"
		r.Leaves = "不外发：当前用的是模拟模型/未填接入地址"
		r.Alert = "没有模型接入，界面里的智能功能都不会真的发包"
		return r
	}
	r.Status = "开着"
	r.Target = host
	if st, ok := egressStat(rep, "llm"); ok {
		for _, h := range st.Hosts {
			if h != host {
				r.Alert = fmt.Sprintf("留痕里出现过与当前接入不同的主机 %s：那说明发包时的接入地址与设置页现在写的不是同一个", h)
				break
			}
		}
	}
	return r
}

// webFetchRow 网页抓取：这是**唯一一处目标不由用户配置**的出网边界——
// 网址是模型自己挑的，所以这一行的重点在"自动放行"这件事本身。
func (a *Agent) webFetchRow(rep safety.EgressReport) ConnectionRow {
	r := ConnectionRow{
		ID: "web.fetch", Title: "网页抓取", Kind: "out", KindText: "出网",
		Target:  "任意公网主机（网址由模型在任务里自己挑）",
		Trigger: "模型判断需要看某个网页时——它是只读工具，默认不经过你的批准",
		Leaves:  "你要它看的那个网址（含查询串）；抓回来的正文留在本机，不进审计",
		Trace:   "门控留痕 action=egress、kind=web.fetch（记主机与网址长度）",
		Stats:   egressStatsText(rep, "web.fetch"),
		Off:     "设置 → 工具 → web.fetch 改「需我批准」，让每一次抓取都经过你点头；彻底摘掉它要改代码，目前没有单独开关",
	}
	if t, ok := a.Reg.Get("web.fetch"); ok {
		perm := a.Gate.EffectivePermission(t)
		if perm.String() == "readonly" {
			r.Status = "开着（自动放行）"
		} else {
			r.Status = "开着（每次问你）"
		}
	} else {
		r.Status = "这个构建里没注册"
		r.Target = "—"
	}
	if a.Cfg.Safety.AllowPrivateWeb {
		r.Alert = "已打开 safety.allow_private_web：抓取可以打到本机与内网地址（含云元数据接口）——被注入的提示词就多了一条读内网的路"
	}
	return r
}

// inboundRow 本机服务端口：台账里唯一一条"入"的方向。
//
// 这一行值得单独存在，是因为它常被当成"这是本机所以安全"。/api 现在要每次启动随机生成的口令
// （internal/webui/guard.go），还校验 Host 与跨站来源，浏览器里的网页替你发请求这条路已经堵上；
// 但口令文件就在数据目录里，**同一用户下的本机进程读得到它**，拿到口令就能提交目标、裁决审批、读你的会话。
// 默认只绑回环；**风险在有人手改 --addr 之后变大**（明文 HTTP，口令在网络上可见），所以状态必须如实印出来。
func inboundRow(bindAddr string) *ConnectionRow {
	if strings.TrimSpace(bindAddr) == "" {
		return nil
	}
	loopback := isLoopbackAddr(bindAddr)
	r := ConnectionRow{
		ID: "inbound", Title: "本机服务端口", Kind: "in", KindText: "入网",
		Target:   bindAddr,
		Status:   "在监听",
		Trigger:  "能连到这个端口、并持有本次启动口令的程序",
		Leaves:   "不外发。但持有口令的一方能提交目标、批准或拒绝审批、读你的会话与设置",
		Trace:    "没有留痕：这个端口不记录谁连过",
		Untraced: true,
		Off:      "桌面端固定只绑 127.0.0.1；命令行别带 --addr，或显式写 127.0.0.1:8787",
	}
	if loopback {
		r.Alert = "回环地址：只有本机能连，/api 要本次启动的口令。口令文件在数据目录里，同一用户下的本机进程读得到它"
	} else {
		r.Status = "在监听（非回环）"
		r.Alert = "监听在非回环地址：局域网里的设备能连上来。/api 要口令，但连接是明文 HTTP，口令在网络上可见。要出这台机器，请放到带 TLS 的反代后面"
	}
	return &r
}

// isLoopbackAddr 只看地址前缀：回环的判定归这里，不引 net 包做多一次解析——
// 台账这一层的职责是"如实交代"，把 net 的判定借来反而多一处会漂的地方。
func isLoopbackAddr(addr string) bool {
	a := strings.TrimSpace(addr)
	host := a
	if i := strings.LastIndexByte(a, ':'); i >= 0 {
		host = a[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

// mcpRows 一台 MCP 服务器一行。
//
// **为什么它不是"连接器"**：MCP 在 Gleam 里不通网络，而是**在你机器上起一个进程**。
// 于是它的爆炸半径不是"我能不能访问某个云"，而是"那个进程拿你的文件当自己的文件"。
// 这一层的留痕只到"谁被调用、批没批"，进程自己又连了什么，本机不知道——所以 Untraced 必须为真。
func (a *Agent) mcpRows() []ConnectionRow {
	out := []ConnectionRow{}
	for _, m := range a.MCPList() {
		name, _ := m["name"].(string)
		command, _ := m["command"].(string)
		enabled, _ := m["enabled"].(bool)
		connected, _ := m["connected"].(bool)
		tools, _ := m["tools"].(int)
		status := "已连接"
		if !enabled {
			status = "已停用：现在起不来进程"
		} else if !connected {
			status = "配置在，但这次没连上"
		}
		out = append(out, ConnectionRow{
			ID:       "mcp." + name,
			Title:    "MCP 服务器 " + name,
			Kind:     "local",
			KindText: "本机",
			Target:   "本机进程 " + command,
			Status:   status,
			Trigger:  fmt.Sprintf("模型调用它注册的 %d 个工具时（工具名前缀 mcp.%s.）", tools, name),
			Leaves:   "不外发到这里；但它是一个跑在你机器上的进程，能读写什么由它自己决定",
			Trace:    "调用与审批有留痕；这个进程随后又连了什么、动了什么文件，本机没有留痕",
			Untraced: true,
			Off:      "设置 → MCP：停用（保留配置）或卸载（连配置一起删）。它的权限级别也在那一页",
		})
	}
	return out
}

// cloudRow 云端账号：只有配了项目地址才存在，所以这一行会说"没配"。
func (a *Agent) cloudRow(rep safety.EgressReport) ConnectionRow {
	r := ConnectionRow{
		ID: "cloud", Title: "云端账号", Kind: "out", KindText: "出网",
		Trigger: "你在「我的 → 账号」点注册/登录/登出那一下，以及令牌临近过期时的续期",
		Leaves:  "邮箱、密码或一次性授权码、会话令牌；发到你自己填的 Supabase 项目",
		Trace:   "门控留痕 action=egress、kind=cloud",
		Stats:   egressStatsText(rep, "cloud"),
		Off:     "「我的 → 账号」退出登录清掉会话；把项目地址填掉就彻底断开",
	}
	if a.Creds == nil {
		r.Status = "凭证存储没装配，发不出去"
		r.Target = "—"
		return r
	}
	c := a.Creds.GetCloudConfig()
	if c == nil || strings.TrimSpace(c.SupabaseURL) == "" {
		r.Status = "未配置：这条线不存在"
		r.Target = "—"
		r.Leaves = "不外发：登录功能没被启用，也没有可发的东西"
		return r
	}
	r.Status = "已配置"
	r.Target = llm.KeyScope(c.SupabaseURL)
	return r
}

// feedbackRow 反馈投递：用户交出去的是他写的那段话，所以这一行必须写清发出去前动了什么。
func (a *Agent) feedbackRow(rep safety.EgressReport) ConnectionRow {
	r := ConnectionRow{
		ID: "feedback", Title: "反馈投递", Kind: "out", KindText: "出网",
		Trigger: "只有你点「提交反馈」那一下",
		Leaves:  "你写的正文（发送前把本机密钥与绝对路径挖掉）加一份上下文快照；本地归档永远先落住",
		Trace:   "门控留痕 action=egress、kind=feedback",
		Stats:   egressStatsText(rep, "feedback"),
		Off:     "不配云端项目地址就只落本地（默认就是这样），一条都不会发出去",
	}
	if a.Creds == nil {
		r.Status = "凭证存储没装配，发不出去"
		r.Target = "—"
		return r
	}
	c := a.Creds.GetCloudConfig()
	// 地址与 key 两件都齐才算配好投递：只有地址的请求一定 401，
	// 那种时候这一行说"已配置"就是骗人（判定与 feedback.Configured 同一把尺）。
	if strings.TrimSpace(c.SupabaseURL) == "" || strings.TrimSpace(c.SupabaseAnonKey) == "" {
		r.Status = "未配置：反馈只落本机"
		r.Target = "—"
		r.Leaves = "不外发：反馈与它的上下文快照都留在数据目录里"
		return r
	}
	r.Status = "已配置"
	r.Target = llm.KeyScope(c.SupabaseURL)
	return r
}

// updateRow 自我更新：这条线只在用户点了「更新」那一下才通。
//
// 它和「市场」那一行正好相反：市场是"你以为是连接、其实不是"，更新是
// "你以为是本机操作、其实在往 GitHub 拉一个可执行文件"。自替换是这台机器上
// 动作最大的一次出网，所以它必须有一行，且必须写清下载地址只认官方 release。
func updateRow(rep safety.EgressReport) ConnectionRow {
	return ConnectionRow{
		ID: "update", Title: "自我更新", Kind: "out", KindText: "出网",
		Target:  "GitHub 官方 release（下载地址只认官方仓库的资产，前端递不进别的来源）",
		Status:  "待命：只有你点「检查更新 → 更新」才下载",
		Trigger: "「我的 / 关于」里点「检查更新」，再点「下载并替换」那一下",
		Leaves:  "不外发你的数据；这一下只往外取一个新版可执行文件，随后替换本机程序",
		Trace:   "门控留痕 action=egress、kind=update（记主机与字节）。替换前旧程序留成 .old",
		Stats:   egressStatsText(rep, "update"),
		Off:     "不点它就不发任何包；只想看有没有新版就点「检查更新」，那一步只发一次 GET",
	}
}

// goToolchainRow Go 工具链安装：这条线只在设置页点「安装 Go」那一下才通。
//
// 为什么它必须进台账：这是一条**用户不会想到的**出网路径——他以为在装开发工具，
// 机器却在往镜像站拉一个 80MB 的压缩包。落的落点、装到哪里、怎么校验，都要写明白。
// 目标主机写"镜像站"而不是某一个域名：按顺序试阿里云、官方国内镜像、官方站，
// 真发了包的那一个由留痕读数回答（这一行有 Stats）。
func goToolchainRow(rep safety.EgressReport) ConnectionRow {
	return ConnectionRow{
		ID: "go.toolchain", Title: "Go 工具链安装", Kind: "out", KindText: "出网",
		Target:  "Go 官方下载镜像（按序试阿里云 / 官方国内镜像 / 官方站）",
		Status:  "待命：只有你点「安装 Go」才下载",
		Trigger: "设置 → 电脑 → 未检测到 Go 时点「安装 Go」那一下",
		Leaves:  "不外发你的数据；这一下只往外取一个 Go 归档，装进 <数据目录>/tools/go",
		Trace:   "门控留痕 action=egress、kind=go.toolchain（记主机与字节）",
		Stats:   egressStatsText(rep, "go.toolchain"),
		Off:     "不点它就不发任何包；已经装好的 Go 不会被重新拉取（检测到即跳过）",
	}
}

// marketRow 模板目录：市场是随程序一起装好的静态目录，**装它本身不出网**。
//
// 为什么值得单独占一行：别的桌面 Agent 把"市场"当成入口，用户默认那里在联网。
// 这一行是这张表里唯一一条"你以为是连接、其实不是"——把这件事写在台账上，
// 比在功能介绍里说一遍"我们本地优先"更可核查。
func marketRow() ConnectionRow {
	return ConnectionRow{
		ID: "market", Title: "技能 / MCP 模板目录", Kind: "local", KindText: "本机",
		Target:  "内置在程序里，不是一个网络地址",
		Status:  "离线可用",
		Trigger: "你在市场点「安装」时",
		Leaves:  "不外发：安装只往配置与技能库里写条目",
		Trace:   "写配置与技能库有留痕（安装记录在配置里）；目录本身一次请求都不发",
		Off:     "不需要关：它不发请求。装完的 MCP 若不想再用，去设置 → MCP 停用",
	}
}
