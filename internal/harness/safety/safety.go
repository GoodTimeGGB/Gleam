// Package safety 实现 Safety Gate 安全门控：
// 高风险操作执行前请求人工批准；支持信任白名单（路径/工具）与三种执行模式。
package safety

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gleam/internal/tools/toolutil"
	"gleam/pkg/types"
)

// Gate 安全门控。
type Gate struct {
	mu              sync.RWMutex
	Mode            string                      // auto | plan_first | interactive
	TrustedTools    map[string]bool             // 信任白名单：中风险直接放行（高风险不可豁免）
	TrustedPaths    []string                    // 绝对路径前缀（已解析）
	WorkspaceRoots  []string                    // 工作区根（视作默认信任路径）
	ApprovalTimeout time.Duration               // 审批等待时长
	toolPerm        map[string]types.Permission // 用户覆盖的工具权限（设置页配置）
	auditMu         sync.Mutex
	audit           []AuditEntry // 最近的门控留痕（环形，上限 auditCap）
	auditPath       string       // 审计落盘文件（空 = 只留内存；见 Record）
	pendingMu       sync.Mutex
	pending         []PendingApproval // 等待审批中的步骤（进程若此时退出，重启后可见）
	pendingPath     string            // 等待审批记录的落盘文件（空 = 不落盘）
}

// New 创建门控。
func New(mode string, trustedTools, trustedPaths, workspaceRoots []string, approvalTimeout time.Duration) *Gate {
	if mode == "" {
		mode = "auto"
	}
	if approvalTimeout <= 0 {
		approvalTimeout = 5 * time.Minute
	}
	tt := map[string]bool{}
	for _, t := range trustedTools {
		tt[t] = true
	}
	var tp []string
	addPath := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			tp = append(tp, filepath.Clean(abs))
		} else {
			tp = append(tp, filepath.Clean(p))
		}
	}
	for _, p := range trustedPaths {
		addPath(p)
	}
	for _, p := range workspaceRoots {
		addPath(p)
	}
	return &Gate{
		Mode:            mode,
		TrustedTools:    tt,
		TrustedPaths:    tp,
		WorkspaceRoots:  workspaceRoots,
		ApprovalTimeout: approvalTimeout,
		toolPerm:        map[string]types.Permission{},
	}
}

// Decision 门控裁决。
type Decision struct {
	NeedApproval bool
	Risk         string // low | medium | high
	Reason       string
}

// AuditEntry 一次门控留痕。文章点名的坑就是"不记录被拒操作"——
// 没人看过的拦截等于没发生过，事后也无法复盘。
type AuditEntry struct {
	Time   time.Time `json:"time"`
	Tool   string    `json:"tool"`
	Risk   string    `json:"risk"`
	Action string    `json:"action"` // denied | approved | auto | reviewed_block | egress | manual_revert
	Reason string    `json:"reason"`
	Detail string    `json:"detail,omitempty"`
	// Egress 数据出网的目标，形如 "llm:api.deepseek.com" / "web.fetch:example.com"。
	//
	// 为什么要有它：Gleam 的定位是"本地优先"，但在此之前**回答不了"什么数据出了本机"**——
	// 提示词发给了哪个模型服务、web.fetch 抓了哪个站点，两处都没有留痕。
	// 资料里的反例正是一台本地客户端**默认开启**地把工作区快照（含完整 .git 历史与 reflog）
	// 加密上传到对象存储。本地优先的产品更需要能自证这一点。
	//
	// **只记主机名与字节量，绝不记内容**：审计本身不能变成新的泄露面。
	// 把请求正文写进 audit.jsonl，等于把风险从"网络"搬到"磁盘上的日志"。
	Egress string `json:"egress,omitempty"`
	// EgressBytes 这次出网发了多少字节（仅 Action=egress 时有值）。
	//
	// 为什么要单独一个字段而不是让读的人去解析 Reason 里那句"（N 字节）"：
	// 那是一句**文案**，改一个标点就把统计打断，而打断的方向是"少算"——
	// 台账报出"本机一共发出去 0 字节"比不报更坏。「连接与出网」台账要按类加总，
	// 加总必须建立在结构化字段上。
	EgressBytes int `json:"egress_bytes,omitempty"`
}

// auditCap 审计日志在内存里保留的条数（够复盘即可，不无限增长）。
const auditCap = 200

// Evaluate 对"工具 + 参数"做裁决（无计划级预批准）。
//
// 这一对不带 ctx 的入口给的是**执行链之外的单发判定**（界面预览"这一步会不会被拦"、
// 服务器 API 的干跑）。那种时候没有 per-task 边界，按全局工作区解析路径就是事实。
// 执行链上的裁决走 EvaluateStepIn——它带着本次任务的边界，路径会按 worktree 解析。
func (g *Gate) Evaluate(t types.Tool, args map[string]any) Decision {
	return g.evaluate(context.Background(), t, args, false)
}

// EvaluateStep 带计划级预批准标记的裁决：
// plan_first 整计划获批后中风险步骤免重复审批，高风险仍需逐步确认。
func (g *Gate) EvaluateStep(t types.Tool, args map[string]any, preApproved bool) Decision {
	return g.evaluate(context.Background(), t, args, preApproved)
}

// EvaluateStepIn 执行链上的裁决：ctx 里带着本次任务的边界（见 toolutil.WithRoots）。
func (g *Gate) EvaluateStepIn(ctx context.Context, t types.Tool, args map[string]any, preApproved bool) Decision {
	return g.evaluate(ctx, t, args, preApproved)
}

// evaluate 核心裁决。只读自动放行；信任白名单放行；
// 中风险且路径全部落在信任路径内放行；高风险始终需要批准。
func (g *Gate) evaluate(ctx context.Context, t types.Tool, args map[string]any, preApproved bool) Decision {
	perm := g.effectivePermission(t)
	switch perm {
	case types.PermissionReadOnly:
		return Decision{NeedApproval: false, Risk: "low", Reason: "只读操作"}
	}

	g.mu.RLock()
	trustedTool := g.TrustedTools[t.Name()]
	mode := g.Mode
	g.mu.RUnlock()

	risk := "medium"
	if perm == types.PermissionFullAccess {
		risk = "high"
	}

	// 高风险（如 shell.exec、file.delete）始终需要批准，白名单不能豁免
	if risk == "high" {
		return Decision{NeedApproval: true, Risk: risk, Reason: "高风险操作，必须人工确认"}
	}

	if trustedTool {
		return Decision{NeedApproval: false, Risk: risk, Reason: fmt.Sprintf("工具 %s 在信任白名单中", t.Name())}
	}

	// 中风险：计划已整体批准时免重复审批
	if preApproved {
		return Decision{NeedApproval: false, Risk: risk, Reason: "计划已获用户批准"}
	}

	// 中风险：plan_first / interactive 模式下均需批准
	if mode == "plan_first" || mode == "interactive" {
		return Decision{NeedApproval: true, Risk: risk, Reason: fmt.Sprintf("%s 模式下所有写操作都需要确认", mode)}
	}

	// auto 模式：涉及路径的操作若全部在信任路径内则放行
	//
	// "信任范围"要算上**本次任务自己的边界**：任务在 worktree 里跑时，它写的每一处
	// 都落在那个目录下——那是 Gleam 自己为这次任务建的目录，属于本该放行的范围。
	// 边界从 ctx 取（per-task），不是往全局信任列表里塞一条：塞进去的话，
	// 任务 A 的 worktree 会在任务 B 跑的时候也算可信，那正是这次要消掉的串味。
	if pa, ok := t.(types.PathAware); ok {
		paths := pa.Paths(ctx, args)
		if len(paths) == 0 {
			return Decision{NeedApproval: true, Risk: risk, Reason: "无法确认操作路径，需要人工确认"}
		}
		roots := toolutil.RootsFrom(ctx, nil)
		for _, p := range paths {
			if !g.pathTrusted(p) && !pathInRoots(roots, p) {
				return Decision{NeedApproval: true, Risk: risk, Reason: fmt.Sprintf("路径 %s 不在信任路径内", p)}
			}
		}
		return Decision{NeedApproval: false, Risk: risk, Reason: "操作路径均在信任路径内"}
	}
	return Decision{NeedApproval: true, Risk: risk, Reason: "无法确认操作范围，需要人工确认"}
}

func (g *Gate) pathTrusted(p string) bool {
	if p == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		// 相对路径按工作区处理
		if len(g.WorkspaceRoots) > 0 {
			return true
		}
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, root := range g.TrustedPaths {
		if pathWithin(root, p) {
			return true
		}
	}
	return false
}

// pathInRoots 路径是否落在其中某个根目录内（本次任务的边界）。
// 判据与 toolutil.Within 同一套语义：用 Rel 而不是字符串前缀，免得 /ws2 被当成 /ws 的子路径。
func pathInRoots(roots []string, path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

func pathWithin(root, path string) bool {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// DescribePaths 生成审批卡片用的路径说明。ctx 决定按谁的边界解析（见 EvaluateStepIn）。
func (g *Gate) DescribePaths(ctx context.Context, t types.Tool, args map[string]any) []string {
	if pa, ok := t.(types.PathAware); ok {
		return pa.Paths(ctx, args)
	}
	return nil
}

// SetMode 运行时切换模式。
func (g *Gate) SetMode(mode string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Mode = mode
}

// SetApprovalTimeout 运行时调整审批等待时长。
func (g *Gate) SetApprovalTimeout(d time.Duration) {
	if d <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ApprovalTimeout = d
}

// SetWorkspaceRoots 运行时切换工作区根：同步更新信任路径
// （移除旧工作区派生的条目，加入新根），文件边界与门控裁决一起生效。
func (g *Gate) SetWorkspaceRoots(roots []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	old := map[string]bool{}
	for _, p := range g.WorkspaceRoots {
		old[filepath.Clean(p)] = true
	}
	kept := g.TrustedPaths[:0]
	for _, p := range g.TrustedPaths {
		if !old[filepath.Clean(p)] {
			kept = append(kept, p)
		}
	}
	g.WorkspaceRoots = append([]string(nil), roots...)
	g.TrustedPaths = append(kept, roots...)
}

// ApprovalTimeoutDuration 并发安全地读取当前审批等待时长。
func (g *Gate) ApprovalTimeoutDuration() time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.ApprovalTimeout
}

// SetToolPermission 运行时覆盖单个工具的权限级别（设置页"需我批准/完全访问/只读"）。
func (g *Gate) SetToolPermission(name string, perm types.Permission) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.toolPerm == nil {
		g.toolPerm = map[string]types.Permission{}
	}
	g.toolPerm[name] = perm
}

// ClearToolPermission 清除覆盖，恢复工具内置权限。
func (g *Gate) ClearToolPermission(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.toolPerm, name)
}

// SetToolPermissions 批量覆盖（启动时从配置/覆盖层装载）。
func (g *Gate) SetToolPermissions(perms map[string]types.Permission) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.toolPerm == nil {
		g.toolPerm = map[string]types.Permission{}
	}
	for name, perm := range perms {
		g.toolPerm[name] = perm
	}
}

// OverrideOf 查询某工具的权限覆盖（无覆盖返回 ok=false）。
func (g *Gate) OverrideOf(name string) (types.Permission, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.toolPerm[name]
	return p, ok
}

// SetAuditPath 指定审计落盘文件（JSONL，追加式）；空路径 = 只留内存。
// 站点 F3 的判断是「副作用闸门 + 全量审计是唯一确定性的那层，资源优先投这里」——
// 内存环只够复盘最近 200 条，重启即失忆，所以必须落盘。
func (g *Gate) SetAuditPath(p string) {
	if g == nil {
		return
	}
	g.auditMu.Lock()
	g.auditPath = p
	g.auditMu.Unlock()
}

// AuditPath 当前审计落盘文件（空 = 未配置）。
func (g *Gate) AuditPath() string {
	if g == nil {
		return ""
	}
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	return g.auditPath
}

// Record 记一条门控留痕（被拦截、被批准、审核模型加拦）。
// 内存环供 UI 快照；同时追加写 JSONL——**追加而不是重写**：
// 审计的价值在于"发生过什么"，重写会把它变成"当前状态"，且崩溃时丢全部。
func (g *Gate) Record(e AuditEntry) {
	if g == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	g.auditMu.Lock()
	g.audit = append(g.audit, e)
	if len(g.audit) > auditCap {
		g.audit = append([]AuditEntry(nil), g.audit[len(g.audit)-auditCap:]...)
	}
	path := g.auditPath
	g.auditMu.Unlock()

	if path != "" {
		g.appendAuditLine(path, e)
	}
}

// RecordEgress 记一次数据出网（Action=egress）。
//
// 与 Record 分开是为了把"只记主机名与字节量"这条边界写在签名上：
// 调用方手里根本没有正文可传，也就不可能不小心把它写进审计。
// host 为空时直接丢弃——记一条"发给了谁都不知道"的出网没有意义。
func (g *Gate) RecordEgress(kind, host string, nbytes int) {
	if g == nil || host == "" || kind == "" {
		return
	}
	g.Record(AuditEntry{
		Tool:        kind,
		Action:      "egress",
		Reason:      fmt.Sprintf("数据出网：%s（%d 字节）", host, nbytes),
		Egress:      kind + ":" + host,
		EgressBytes: nbytes,
	})
}

// appendAuditLine 追加一行 JSONL（尽力而为：审计写失败不阻断主流程）。
func (g *Gate) appendAuditLine(path string, e AuditEntry) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// LoadAuditLog 读回落盘的审计记录（最近 n 条，新的在前；n <= 0 取全部）。
// 供重启后的界面/CLI 查看——重启即失忆的审计等于没有审计。
func LoadAuditLog(path string, n int) ([]AuditEntry, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []AuditEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e AuditEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	// 新的在前
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// RecentAudit 返回最近的门控留痕（新的在前）。
func (g *Gate) RecentAudit(n int) []AuditEntry {
	if g == nil || n <= 0 {
		return nil
	}
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	if n > len(g.audit) {
		n = len(g.audit)
	}
	out := make([]AuditEntry, 0, n)
	for i := len(g.audit) - 1; i >= len(g.audit)-n; i-- {
		out = append(out, g.audit[i])
	}
	return out
}

// EgressStat 一类出网落点的实测读数。
type EgressStat struct {
	Kind  string   `json:"kind"`
	Count int      `json:"count"`
	Hosts []string `json:"hosts"`
	Bytes int      `json:"bytes"`
}

// EgressReport 出网读数的**统计范围**也一起给。
//
// 为什么范围要跟着走：内存环只有最近 auditCap 条（重启即清零），全量在落盘的
// audit.jsonl 里。界面若只印"N 次 / M 字节"，读者会把它当成"这台机器上一共发出去
// 多少"——那正是把"我不知道"讲成答案。带上 Scanned/Cap，界面就能如实说"只数了最近
// 这些条"，而且数字被挤掉时（Scanned == Cap）说法必须变，不必前端自己记那个上限。
type EgressReport struct {
	Stats   []EgressStat `json:"stats"`
	Scanned int          `json:"scanned"`
	Cap     int          `json:"cap"`
}

// EgressStats 按类聚合内存留痕里的出网记录（只读，不发请求、不读磁盘）。
//
// 主机名从结构化字段 Egress（"kind:host"）里取，不去解析 Reason 那句中文文案——
// 改一个标点就把统计打断，而打断的方向是"少算"。
func (g *Gate) EgressStats() EgressReport {
	rep := EgressReport{Cap: auditCap, Stats: []EgressStat{}}
	if g == nil {
		return rep
	}
	g.auditMu.Lock()
	defer g.auditMu.Unlock()
	rep.Scanned = len(g.audit)
	byKind := map[string]*EgressStat{}
	seen := map[string]map[string]bool{}
	for _, e := range g.audit {
		if e.Action != "egress" {
			continue
		}
		st, ok := byKind[e.Tool]
		if !ok {
			st = &EgressStat{Kind: e.Tool, Hosts: []string{}}
			byKind[e.Tool] = st
			seen[e.Tool] = map[string]bool{}
		}
		st.Count++
		st.Bytes += e.EgressBytes
		if host := strings.TrimPrefix(e.Egress, e.Tool+":"); host != "" && host != e.Egress {
			if !seen[e.Tool][host] {
				seen[e.Tool][host] = true
				st.Hosts = append(st.Hosts, host)
			}
		}
	}
	for _, st := range byKind {
		sort.Strings(st.Hosts)
		rep.Stats = append(rep.Stats, *st)
	}
	sort.Slice(rep.Stats, func(i, j int) bool { return rep.Stats[i].Kind < rep.Stats[j].Kind })
	return rep
}

// EffectivePermission 返回工具的有效权限（导出供门面/界面展示；
// 执行器据此判断哪些调用是只读的——只有只读调用的结果才能被安全复用）。
func (g *Gate) EffectivePermission(t types.Tool) types.Permission {
	return g.effectivePermission(t)
}

// effectivePermission 返回工具的有效权限：用户覆盖优先于内置定义。
func (g *Gate) effectivePermission(t types.Tool) types.Permission {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if p, ok := g.toolPerm[t.Name()]; ok {
		return p
	}
	return t.Permission()
}
