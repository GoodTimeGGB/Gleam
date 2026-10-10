package market

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// 市场源：把「目录从哪来」这件事显式化。
//
// 为什么要有这一层：内置目录是随程序发布的常量（零依赖、离线可用），但它装不下
// 生态里的全部服务器。要接外部源，就必须先回答三个问题——**接谁、为什么是它、
// 连不上怎么办**。三个问题各有一个字段/函数在这里负责，而不是散在 handler 里。
//
// 为什么不在这个包里发请求：出网要有留痕（门控 `/api/connections` 台账按
// `RecordEgress` 的字面量落点对账），而留痕只有接入层（webui）拿得到门控。
// 所以这个包只做**纯解析**：给一段 JSON，回一批可安装条目。发请求、缓存、
// 记留痕都在 internal/webui/market_remote.go。

// Source 一个可选的目录源。
type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`     // mcp-registry（官方注册表）| npm（包元数据）| builtin（内置常量）
	BaseURL string `json:"base_url"` // 查询地址（builtin 为空）
	Region  string `json:"region"`   // global | cn —— 只用于「按地区优先」的排序，不代表绝对快慢
	Note    string `json:"note,omitempty"`
}

// BuiltinSource 内置目录：不是一个网络地址，永远可用。
func BuiltinSource() Source {
	return Source{
		ID: "builtin", Name: "内置目录", Kind: "builtin", Region: "local",
		Note: "随程序发布的常量目录：离线可用，永远排在最后兜底。",
	}
}

// 源地址声明成变量：唯一 owner 在这里，测试也能把它换成本地服务（否则判据得真连官方站）。
var (
	MarketCatalogURL = "https://registry.modelcontextprotocol.io/v0/servers"
	MarketNPMURL     = "https://registry.npmjs.org"
	MarketNPMCNURL   = "https://registry.npmmirror.com"
)

// DefaultSources 默认源表。顺序即默认优先级（地区偏好会在此基础上重排）。
//
// 三件事刻意分开写：
//   - 官方注册表（registry.modelcontextprotocol.io）是**权威目录**，回答「有哪些服务器」；
//   - npm 两个地址只用来**查包**（版本、是否存在），回答「这个包真能装吗」——
//     npmmirror 是它的国内镜像，这就是「按 IP 换源」真正有意义的那一处；
//   - 内置目录兜底。
//
// 注册表**没有**国内镜像，这一点不能装糊涂：写一个不存在的镜像地址，比不提供更坏。
func DefaultSources() []Source {
	return []Source{
		{
			ID: "official-mcp", Name: "MCP 官方注册表", Kind: "mcp-registry",
			BaseURL: MarketCatalogURL, Region: "global",
			Note: "由 Model Context Protocol 官方维护的服务器目录；目前没有国内镜像。",
		},
		{
			ID: "npm", Name: "npm registry", Kind: "npm",
			BaseURL: MarketNPMURL, Region: "global",
			Note: "查 npm 包是否真实存在、最新版本是多少。",
		},
		{
			ID: "npm-cn", Name: "npm 国内镜像（npmmirror）", Kind: "npm",
			BaseURL: MarketNPMCNURL, Region: "cn",
			Note: "npm 的国内镜像；查包时用它，装包的命令仍由本机 npx 决定走哪个 registry。",
		},
		BuiltinSource(),
	}
}

// RemotePreset 从外部目录解析出来的一条可安装条目。
//
// 字段与内置 MCPPreset 对齐（Installable 之外），这样前端与安装入口只需认识一种形状。
type RemotePreset struct {
	ID       string   `json:"id"`   // 源的唯一名（官方注册表里是反向域名式名字）
	Name     string   `json:"name"` // 给人看的名字
	Desc     string   `json:"desc"`
	Version  string   `json:"version,omitempty"`
	Command  string   `json:"command,omitempty"`
	BaseArgs []string `json:"base_args,omitempty"`
	Params   []Param  `json:"params,omitempty"`
	Trust    string   `json:"trust"`
	Tags     []string `json:"tags,omitempty"`
	// PkgRef npm / pypi 包引用（identifier[@version]）。用途只有一个：判断「装过没」时
	// 拿它比对——装的时候可能按选中的 npm 源加过 `--registry=…`，逐字比 argv 会判错。
	PkgRef string `json:"pkg_ref,omitempty"`
	// Kind 装成什么："stdio"（本机进程，默认）| "http"（远端 streamable-http）。
	Kind string `json:"kind,omitempty"`
	// URL 与 Headers 只在 Kind=http 时有意义。
	URL         string         `json:"url,omitempty"`
	Headers     []RemoteHeader `json:"headers,omitempty"`
	Source      string         `json:"source"` // 来自哪个源（Source.ID）
	Homepage    string         `json:"homepage,omitempty"`
	Installable bool           `json:"installable"` // false 时 Unsupported 说明为什么
	Unsupported string         `json:"unsupported,omitempty"`
}

// ---------- 官方注册表的解析 ----------

// registryResponse 官方注册表的响应外壳。只取用得到的字段；多了不改也不报错。
type registryResponse struct {
	Servers []struct {
		Server registryServer `json:"server"`
	} `json:"servers"`
}

// registryRemote 注册表里的一个远端接入点。
type registryRemote struct {
	Type    string           `json:"type"`
	URL     string           `json:"url"`
	Headers []registryHeader `json:"headers"`
}

// registryHeader 远端要求的一个请求头（值里可带 {占位符}）。
type registryHeader struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	IsRequired  bool   `json:"isRequired"`
	IsSecret    bool   `json:"isSecret"`
	Description string `json:"description"`
}

// RemoteHeader 远端服务器要求的一个请求头。
//
// Value 是**模板**：`Bearer {smithery_api_key}` 里的 `{...}` 就是用户要填的那一项。
// 我们把每个占位符做成一条安装参数，填完替换回去——这样"需要填密钥"的条目也能装，
// 而不是整类被标成装不了。
type RemoteHeader struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Description string `json:"description,omitempty"`
}

type registryServer struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Repository  map[string]any    `json:"repository"`
	Packages    []registryPackage `json:"packages"`
	Remotes     []registryRemote  `json:"remotes"`
}

type registryPackage struct {
	RegistryType    string `json:"registryType"` // npm | pypi | cargo | oci
	RegistryBaseURL string `json:"registryBaseUrl"`
	Identifier      string `json:"identifier"`
	Version         string `json:"version"`
	RuntimeHint     string `json:"runtimeHint"` // npx | uvx | docker …
	Transport       struct {
		Type string `json:"type"`
	} `json:"transport"`
	RuntimeArguments []struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"runtimeArguments"`
	EnvironmentVariables []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsRequired  bool   `json:"isRequired"`
	} `json:"environmentVariables"`
}

// runtimeFor 把 registryType 映射成能在本机跑起来的运行时。
//
// 只认两种：npm→npx、pypi→uvx。别的（cargo / oci 镜像）本机不一定有，也不该
// 冒充能装 —— 标成不可安装并说明原因，比让用户点了之后卡在"命令不存在"强。
func runtimeFor(registryType, hint string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(registryType)) {
	case "npm":
		return "npx", true
	case "pypi":
		return "uvx", true
	}
	// hint 给了也只在它确实是已知运行时的情况下采纳
	switch strings.ToLower(strings.TrimSpace(hint)) {
	case "npx", "uvx":
		return strings.ToLower(hint), true
	}
	return "", false
}

// ParseRegistry 把官方注册表的响应解析成可安装条目。
//
// 只认 **stdio** 传输：Gleam 的 MCP 是"在你机器上起一个进程"，http/streamable-http
// 那种远端服务器它连不上。只有 remotes 没有 packages 的条目因此标成不可安装 ——
// 这是这一层最容易骗人的地方：目录里有它，用户点了装却装不上。
func ParseRegistry(body []byte, sourceID string) ([]RemotePreset, error) {
	var resp registryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]RemotePreset, 0, len(resp.Servers))
	// 同名服务器会有多个版本，注册表把它们各列一条。不去重的话，搜一次能看到同一台
	// 服务器占好几行（0.1.0、0.1.9…），用户得自己猜该装哪条。取版本最高的那一条。
	best := map[string]int{} // 名字 -> out 里的下标
	for _, it := range resp.Servers {
		s := it.Server
		if strings.TrimSpace(s.Name) == "" {
			continue
		}
		p := RemotePreset{
			ID: s.Name, Name: firstNonEmpty(s.Title, s.Name), Desc: s.Description,
			Version: s.Version, Source: sourceID, Trust: "user_approved",
		}
		if u, ok := s.Repository["url"].(string); ok {
			p.Homepage = u
		}
		// 先看有没有本机包（stdio 首选：不经别人的服务器、凭据也不外发）。
		pkg, hasPkg := pickStdioPackage(s.Packages)
		if !hasPkg {
			// 没有本机包就看远端：streamable-http 是这一版实现了的那一种。
			if rm, ok := pickStreamableRemote(s.Remotes); ok {
				p.Kind, p.URL, p.Installable = "http", rm.URL, true
				for _, h := range rm.Headers {
					if strings.TrimSpace(h.Name) == "" {
						continue
					}
					rh := RemoteHeader{Name: h.Name, Value: h.Value, Required: h.IsRequired, Secret: h.IsSecret, Description: h.Description}
					p.Headers = append(p.Headers, rh)
					// 模板里的每个 {占位符} 变成一条要用户填的参数
					for _, ph := range placeholders(h.Value) {
						p.Params = append(p.Params, Param{
							Key: ph, Label: headerLabel(h.Name, h.Description),
							Placeholder: h.Description, Required: h.IsRequired, Secret: h.IsSecret, Kind: "header",
						})
					}
				}
				out = append(out, p)
				continue
			}
		}
		cmd, cmdOK := runtimeFor(pkg.RegistryType, pkg.RuntimeHint)
		if !hasPkg || !cmdOK {
			switch {
			case !hasPkg:
				p.Unsupported = unsupportedReason(s)
			default:
				p.Unsupported = "这个包只有 " + pkg.RegistryType + " 分发方式，本机没有对应运行时"
			}
			out, best = emit(out, best, p)
			continue
		}
		ref := pkg.Identifier
		if pkg.Version != "" {
			ref += "@" + pkg.Version
		}
		args := []string{ref}
		for _, a := range pkg.RuntimeArguments {
			if strings.TrimSpace(a.Value) != "" {
				args = append([]string{a.Value}, args...)
			}
		}
		p.Command, p.BaseArgs, p.PkgRef, p.Installable = cmd, args, ref, true
		// 环境变量两类都收：必填的会变成安装弹窗里必须填的一项，可选的留空即不传。
		// Kind=env 是关键——这些值要作为环境变量交给子进程，不是塞进命令行。
		for _, ev := range pkg.EnvironmentVariables {
			if strings.TrimSpace(ev.Name) == "" {
				continue
			}
			p.Params = append(p.Params, Param{
				Key: ev.Name, Label: "环境变量 " + ev.Name, Placeholder: ev.Description,
				Required: ev.IsRequired, Kind: "env",
			})
		}
		out, best = emit(out, best, p)
	}
	return out, nil
}

// emit 把一条条目并入结果：同名只留版本最高的那一条（原位替换，不并排留两条）。
//
// 注册表按"版本"各列一条，所以同一台服务器会出现好几次。原地替换而不是追加，
// 是因为追加会把去重变成"少几条"，数量对得上但内容里仍夹着旧版本。
func emit(out []RemotePreset, best map[string]int, p RemotePreset) ([]RemotePreset, map[string]int) {
	if idx, dup := best[p.ID]; dup {
		if compareVersion(p.Version, out[idx].Version) <= 0 {
			return out, best
		}
		out[idx] = p
		return out, best
	}
	best[p.ID] = len(out)
	return append(out, p), best
}

// pickStdioPackage 挑一个本机能跑的 stdio 包。优先 npm（npx 最普遍），其次 pypi。
func pickStdioPackage(pkgs []registryPackage) (registryPackage, bool) {
	for _, want := range []string{"npm", "pypi"} {
		for _, p := range pkgs {
			if strings.EqualFold(p.RegistryType, want) && strings.TrimSpace(p.Identifier) != "" {
				return p, true
			}
		}
	}
	return registryPackage{}, false
}

// unsupportedReason 说清「目录里有它、但 Gleam 装不了」的原因。
// 空话（"暂不支持"）不算说清：要么是传输方式不对，要么是分发方式不对。
func unsupportedReason(s registryServer) string {
	if len(s.Remotes) > 0 {
		kinds := map[string]bool{}
		for _, r := range s.Remotes {
			if r.Type != "" {
				kinds[r.Type] = true
			}
		}
		list := make([]string, 0, len(kinds))
		for k := range kinds {
			list = append(list, k)
		}
		sort.Strings(list)
		return "只提供远端接入（" + strings.Join(list, " / ") + "），本版实现了的是 streamable-http"
	}
	if len(s.Packages) > 0 {
		seen := map[string]bool{}
		kinds := make([]string, 0, len(s.Packages))
		for _, p := range s.Packages {
			if k := strings.TrimSpace(p.RegistryType); k != "" && !seen[k] {
				seen[k] = true
				kinds = append(kinds, k)
			}
		}
		sort.Strings(kinds)
		return "只有 " + strings.Join(kinds, " / ") + " 分发方式，本机没有对应运行时（只用 npx / uvx）"
	}
	return "这个条目没有提供任何可安装的分发方式"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// FilterRemote 按关键词过滤远端条目，命中口径与内置目录一致（id / 名字 / 描述 / 标签）：
// 两套口径各写一份，用户就会看到"同一个词在内置目录里搜得到、在远端里搜不到"。
func FilterRemote(in []RemotePreset, q string) []RemotePreset {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return in
	}
	out := make([]RemotePreset, 0, len(in))
	for _, p := range in {
		if matchKeyword(q, p.ID, p.Name, p.Desc, strings.Join(p.Tags, " ")) {
			out = append(out, p)
		}
	}
	return out
}

// compareVersion 比较两个版本号：a<b 返回 -1。只认数字段，非数字段按 0 处理。
// 与 update.go 里那个同名函数是同一个口径——版本号怎么比，产品里只该有一套说法；
// 那边比的是"要不要升级"，这边比的是"哪条更新"，比错了都会让用户装到旧的。
func compareVersion(a, b string) int {
	pa, pb := versionSegs(a), versionSegs(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionSegs(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &n); err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// pickStreamableRemote 挑一个 streamable-http 远端。
//
// 只认这一种：旧版 sse 传输本版没实现，把它也当"能装"，用户点下去只会连不上。
// 没有可用的远端就返回 false，由调用方给出如实的原因。
func pickStreamableRemote(remotes []registryRemote) (registryRemote, bool) {
	for _, r := range remotes {
		if strings.EqualFold(strings.TrimSpace(r.Type), "streamable-http") && strings.TrimSpace(r.URL) != "" {
			return r, true
		}
	}
	return registryRemote{}, false
}

// placeholders 从请求头模板里取出 {名字}。同一个名字只算一次。
func placeholders(tmpl string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '{' {
			continue
		}
		j := strings.IndexByte(tmpl[i+1:], '}')
		if j < 0 {
			break
		}
		name := strings.TrimSpace(tmpl[i+1 : i+1+j])
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i += j + 1
	}
	return out
}

// headerLabel 一条请求头参数的标签：优先用注册表给的描述（它更懂要填什么），
// 没有就退回请求头名字——"Authorization" 虽抽象，也比一个空标签强。
func headerLabel(name, desc string) string {
	if strings.TrimSpace(desc) != "" {
		return desc
	}
	return "请求头 " + name
}

// BuildHeaders 把用户填的占位符替换进请求头模板，得到最终请求头。
//
// 必填的没填 → 报错（否则会拿着 "Bearer {key}" 这种半成品去请求，得到一个 401，
// 而 401 的提示里看不出是"你没填"）。
func BuildHeaders(p RemotePreset, values map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, h := range p.Headers {
		val := h.Value
		for _, ph := range placeholders(h.Value) {
			v := strings.TrimSpace(values[ph])
			if v == "" && h.Required {
				return nil, fmt.Errorf("缺少必填项 %q（%s）", ph, headerLabel(h.Name, h.Description))
			}
			val = strings.ReplaceAll(val, "{"+ph+"}", v)
		}
		if strings.Contains(val, "{") && h.Required {
			return nil, fmt.Errorf("请求头 %s 的模板没填完：%s", h.Name, val)
		}
		if strings.TrimSpace(val) != "" {
			out[h.Name] = val
		}
	}
	return out, nil
}
