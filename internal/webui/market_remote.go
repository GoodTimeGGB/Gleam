package webui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gleam/internal/atomicfile"
	"gleam/internal/buildinfo"
	"gleam/internal/market"
)

// 市场远端源：目录从哪来、为什么是它、连不上怎么办。
//
// 三件事在这里落地，别的文件不重复：
//  1. **拉官方 MCP 注册表**（唯一的权威目录），落盘缓存，断网时用缓存兜底；
//  2. **出网留痕**：每一次请求都记 `RecordEgress("market", host, n)`——
//     `/api/connections` 台账按这个字面量对账（scripts/check-egress-owner.py 双向判），
//     少记一处，界面上那一行就会说"没有记录"，而真实原因是"没往这一类记"。
//  3. **按可达延迟选 npm 源**：国内直连 npmjs 常常慢得离谱，而 npmmirror 是它的
//     国内镜像。选中的镜像会作为 `--registry` 塞进安装参数，npx 装包才真的走它。
//
// 为什么**不**按 IP 归属地选：那要额外打一个第三方 geo 接口，等于为了省一次测速
// 而把本机出口 IP 交给第三方——本地优先的产品不该这么干。测速问的是同一个问题的
// 更直接的那一半：这个源现在连得上、多快。归属地只是它的一个代理指标。

const (
	// 目录源（官方注册表）实测很慢，8 秒会稳定超时——超时就等于这条目录不存在，
	// 而它其实是存在的。宁可让市场页多转一会儿（有骨架屏），也不要给出一个假答案。
	marketFetchTimeout = 15 * time.Second
	marketProbeTimeout = 3 * time.Second
	marketMaxBody      = 4 << 20
	marketCacheName    = "market-official-mcp.json"
	marketCacheTTL     = 10 * time.Minute
)

// marketState 进程内的远端目录缓存与测速结果。
//
// 为什么不每次请求都打网：市场页是边打字边搜的，逐次打网会把"搜索"变成
// "每次敲键发一个请求"。缓存 + TTL 是这一层唯一需要的东西。
type marketState struct {
	mu      sync.Mutex
	byQuery map[string]marketEntry // 查询串 -> 结果（市场页边打字边搜，共用一个槽位会互相冲）
	npmPick string                 // 选中的 npm 源 ID（空 = 还没测）
	npmAt   time.Time
	npmWhy  string
}

// marketEntry 一次查询的结果与它什么时候拿到的。
type marketEntry struct {
	presets []market.RemotePreset
	at      time.Time
}

var marketCacheState = &marketState{}

// handleMarketSources 列出可用源、当前选中项与选它的理由。
func (s *Server) handleMarketSources(w http.ResponseWriter, _ *http.Request) {
	srcs := market.DefaultSources()
	pick, why := s.marketNPMSource(context.Background())
	writeJSON(w, 200, map[string]any{
		"sources":        srcs,
		"npm_source":     pick.ID,
		"npm_source_why": why,
		"catalog_source": "official-mcp",
		"note": "目录来自 MCP 官方注册表（没有国内镜像）；npm 源只影响安装时 npx 走哪个 registry，" +
			"选择方式是实测这两个地址的响应延迟，不查 IP 归属地。",
	})
}

// marketNPMSource 在候选 npm 源里挑最快的一个，结果进程内复用。
func (s *Server) marketNPMSource(ctx context.Context) (market.Source, string) {
	cands := []market.Source{}
	for _, src := range market.DefaultSources() {
		if src.Kind == "npm" {
			cands = append(cands, src)
		}
	}
	if len(cands) == 0 {
		return market.Source{}, "没有配置 npm 源"
	}

	marketCacheState.mu.Lock()
	if marketCacheState.npmPick != "" && time.Since(marketCacheState.npmAt) < marketCacheTTL {
		id, why := marketCacheState.npmPick, marketCacheState.npmWhy
		marketCacheState.mu.Unlock()
		for _, c := range cands {
			if c.ID == id {
				return c, why
			}
		}
	} else {
		marketCacheState.mu.Unlock()
	}

	best := cands[0] // 默认第一个（npmjs），测速失败也总得选一个
	bestDur := time.Duration(0)
	why := ""
	for i, c := range cands {
		d, err := s.probeSource(ctx, c)
		if err != nil {
			if why == "" {
				why = fmt.Sprintf("%s 连不上（%v）", c.Name, err)
			}
			continue
		}
		if i == 0 || bestDur == 0 || d < bestDur {
			best, bestDur = c, d
			why = fmt.Sprintf("实测最快：%s 用时 %dms", c.Name, d.Milliseconds())
		}
	}
	if why == "" {
		why = "两个 npm 源都没测通，先用默认源"
	}

	marketCacheState.mu.Lock()
	marketCacheState.npmPick, marketCacheState.npmAt, marketCacheState.npmWhy = best.ID, time.Now(), why
	marketCacheState.mu.Unlock()
	return best, why
}

// probeSource 量一次源的可达延迟。只取一个极小的已知包，别把测速变成一次下载。
func (s *Server) probeSource(ctx context.Context, src market.Source) (time.Duration, error) {
	start := time.Now()
	raw := strings.TrimSuffix(src.BaseURL, "/") + "/left-pad"
	cctx, cancel := context.WithTimeout(ctx, marketProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, raw, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "gleam/"+buildinfo.Version)
	resp, err := (&http.Client{Timeout: marketProbeTimeout}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	s.recordMarketEgress(raw, n)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

// lookupRemotePreset 按 id 找一条远端条目。
//
// 为什么不只查浏览页：用户是在**搜索结果**里看到这条的，而浏览页（limit=100）里
// 未必有它——按 id 去找浏览页，会给出"目录里没有这条"，而它明明就在眼前。
// 顺序：先扫已经取回来的那些查询结果（最可能是命中），再按名字向注册表搜一次。
func (s *Server) lookupRemotePreset(ctx context.Context, id string) (market.RemotePreset, string, bool) {
	marketCacheState.mu.Lock()
	for _, e := range marketCacheState.byQuery {
		for _, p := range e.presets {
			if p.ID == id {
				marketCacheState.mu.Unlock()
				return p, "", true
			}
		}
	}
	marketCacheState.mu.Unlock()

	// 没在缓存里：拿 id 的最后一段当关键词问一次注册表（注册表的 search 匹配名字）
	key := id
	if i := strings.LastIndexAny(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	presets, note := s.marketSearch(ctx, key, false)
	for _, p := range presets {
		if p.ID == id {
			return p, note, true
		}
	}
	return market.RemotePreset{}, note, false
}

// marketSearch 查远端目录。
//
// 查询词**发给目录源**，不在本地过滤：注册表里有几千条服务器，本地过滤只能看到
// 它默认返回的那一页——搜"filesystem"会一条都搜不到，而用户看到的是"没有"。
// 这件事真发生过：第一版就是本地过滤，实测 remote_count 恒为 0。
//
// 缓存按查询串分开：市场页是边打字边搜的，共用一个槽位会让相邻两次查询互相冲掉。
func (s *Server) marketSearch(ctx context.Context, q string, force bool) ([]market.RemotePreset, string) {
	q = strings.TrimSpace(q)

	marketCacheState.mu.Lock()
	if !force && marketCacheState.byQuery != nil {
		if e, ok := marketCacheState.byQuery[q]; ok && time.Since(e.at) < marketCacheTTL {
			out := e.presets
			marketCacheState.mu.Unlock()
			return out, ""
		}
	}
	marketCacheState.mu.Unlock()

	var src market.Source
	for _, c := range market.DefaultSources() {
		if c.Kind == "mcp-registry" {
			src = c
		}
	}
	if src.BaseURL == "" {
		return nil, "没有配置目录源"
	}
	raw := src.BaseURL + "?limit=100"
	if q != "" {
		raw = src.BaseURL + "?search=" + url.QueryEscape(q) + "&limit=50"
	}

	body, err := s.fetchMarketURL(ctx, raw)
	if err != nil {
		// 网络失败不是"没有这条"：退磁盘缓存（存的是浏览页那份）并按查询词本地过滤。
		// 这一份可能不全，所以说法里必须带上"可能不全"——把残缺当完整就是撒谎。
		cached, cErr := s.readMarketCache()
		if cErr == nil && len(cached) > 0 {
			out := market.FilterRemote(cached, q)
			return out, "目录源连不上（" + err.Error() + "），这一份来自上次缓存的浏览页，可能不全。"
		}
		return nil, "目录源连不上：" + err.Error()
	}
	presets, perr := market.ParseRegistry(body, src.ID)
	if perr != nil {
		return nil, "目录源返回的内容读不懂：" + perr.Error()
	}
	s.cacheMarketQuery(q, presets)
	if q == "" {
		s.writeMarketCache(body) // 只有浏览页值得落盘：搜索结果随词变化，存它没有意义
	}
	return presets, ""
}

func (s *Server) cacheMarketQuery(q string, presets []market.RemotePreset) {
	marketCacheState.mu.Lock()
	if marketCacheState.byQuery == nil {
		marketCacheState.byQuery = map[string]marketEntry{}
	}
	marketCacheState.byQuery[q] = marketEntry{presets: presets, at: time.Now()}
	marketCacheState.mu.Unlock()
}

// marketCachePath 磁盘缓存位置；没有数据目录时返回空串（那就只留内存）。
func (s *Server) marketCachePath() string {
	dir := strings.TrimSpace(s.Agent.DataDir())
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, marketCacheName)
}

// writeMarketCache 把原始响应落盘。存原文而不是解析结果：
// 条目结构变了（官方加字段）时，旧解析器仍能读旧原文，解析器升级后也不必重拉。
func (s *Server) writeMarketCache(body []byte) {
	p := s.marketCachePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	// 落盘失败不影响本次结果：缓存是省一次请求，不是前提。
	_ = atomicfile.Write(p, body, 0o600)
}

func (s *Server) readMarketCache() ([]market.RemotePreset, error) {
	p := s.marketCachePath()
	if p == "" {
		return nil, fmt.Errorf("没有数据目录")
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return market.ParseRegistry(body, "official-mcp")
}

// fetchMarketURL 发一次请求并把出网记进门控留痕。
func (s *Server) fetchMarketURL(ctx context.Context, raw string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, marketFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gleam/"+buildinfo.Version)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: marketFetchTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, marketMaxBody))
	s.recordMarketEgress(raw, int64(len(body)))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// recordMarketEgress 记一条出网留痕。只记主机与字节，不记查询串（搜什么词是本机的事）。
func (s *Server) recordMarketEgress(raw string, n int64) {
	if s.Agent == nil || s.Agent.Gate == nil {
		return
	}
	host := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host = u.Host
	}
	// kind 写死成字面量：闸门（scripts/check-egress-owner.py）与台账都靠它字面对账，
	// 传常量会让两边都读不出来。
	s.Agent.Gate.RecordEgress("market.remote", host, int(n))
}

// marketInstallArgs 把选中的 npm 源变成安装参数。
//
// 为什么在这里做：`npx -y pkg` 走哪个 registry 由 npm 自己的配置决定，用户在
// 国内直连 npmjs 会很慢。把 `--registry` 显式塞进参数，安装就真的走选中的镜像——
// 否则"选源"只是界面上一个好看的开关。
//
// 只对 npm 包这么做：pypi 的镜像地址各家不同，凭空写一个等于替用户猜。
func marketInstallArgs(src market.Source, isNPM bool, args []string) []string {
	if !isNPM || src.Kind != "npm" || strings.Contains(src.BaseURL, "registry.npmjs.org") {
		return args
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, "--registry="+src.BaseURL)
	return append(out, args...)
}

// handleMarketRuntimes 列出本机运行时（npx / uvx / docker / cargo / go）与补齐办法。
// 市场页据此在卡片上标出"装不了：本机没有 npx"，而不是等用户点了才报。
func (s *Server) handleMarketRuntimes(w http.ResponseWriter, _ *http.Request) {
	list := market.Runtimes()
	missing := []string{}
	for _, r := range list {
		if !r.Found {
			missing = append(missing, r.Name)
		}
	}
	writeJSON(w, 200, map[string]any{"runtimes": list, "missing": missing})
}
