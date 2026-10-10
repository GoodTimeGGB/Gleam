package market

import (
	"os/exec"
	"strings"
)

// 运行时检测：MCP 服务器的"装"其实是"起一个别人写好的进程"，
// 而起它需要本机已经有对应的运行时（npx / uvx / docker）。
//
// 为什么必须提前判：没有 npx 时，安装会**先把配置写进去**，然后在连接那一步失败，
// 只丢回一句 warning（`mcpInstall` 的既定行为：配置是配置，连接是连接）。
// 用户看到的是"装了但连不上"，而真实原因是"这台机器根本没有 node"——
// 这两件事的修法完全不同。所以缺运行时要在**按下安装之前**就说清。

// RuntimeStatus 一个运行时在本机的可用性。
type RuntimeStatus struct {
	Name  string `json:"name"` // npx | uvx | docker | cargo | go
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"`
	// Why 缺了之后怎么办。不是"请安装 node"这种正确但没用的话——
	// 要能照着做：给下载页、给一行命令。
	Why string `json:"why"`
}

// runtimeSpec 已知运行时：名字、它属于哪套工具链、以及缺了怎么办。
var runtimeSpec = []struct {
	name string
	why  string
}{
	{"npx", "npx 随 Node.js 一起装：https://nodejs.org（装完重开 Gleam）"},
	{"uvx", "uvx 随 uv 一起装：https://docs.astral.sh/uv/（或 pip install uv）"},
	{"docker", "docker 需要 Docker Desktop：https://www.docker.com/products/docker-desktop/"},
	{"cargo", "cargo 随 Rust 一起装：https://rustup.rs"},
	{"go", "go 可以在 设置 → 电脑 里一键安装"},
}

// Runtimes 探测所有已知运行时（只查 PATH，不执行、不联网）。
func Runtimes() []RuntimeStatus {
	out := make([]RuntimeStatus, 0, len(runtimeSpec))
	for _, spec := range runtimeSpec {
		st := RuntimeStatus{Name: spec.name, Why: spec.why}
		if p, err := exec.LookPath(spec.name); err == nil {
			st.Found, st.Path = true, p
		}
		out = append(out, st)
	}
	return out
}

// RuntimeFor 返回这个命令属于哪个运行时（用于把条目和运行时对上）。
// npx 在 Windows 上是 npx.cmd，所以只比去掉扩展名后的名字。
func RuntimeFor(command string) string {
	base := strings.ToLower(strings.TrimSpace(command))
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	for _, spec := range runtimeSpec {
		if base == spec.name {
			return spec.name
		}
	}
	return ""
}

// RuntimeMissing 判断一条命令需要的运行时是否缺失。返回缺失项与"缺了"这个事实。
// 认不出命令（自定义命令、绝对路径）时返回 false——那种情况没有可查的运行时，
// 别把"我不认识"说成"你缺东西"。
func RuntimeMissing(command string) (RuntimeStatus, bool) {
	name := RuntimeFor(command)
	if name == "" {
		return RuntimeStatus{}, false
	}
	for _, st := range Runtimes() {
		if st.Name == name {
			if st.Found {
				return RuntimeStatus{}, false
			}
			return st, true
		}
	}
	return RuntimeStatus{}, false
}

// RankRemote 把远端条目按"能不能装、名字像不像"重排。
//
// 为什么要排：注册表把第三方 fork 排得比官方包靠前（搜 figma 的第一条常是
// ai.smithery/* 之类），而我们的列表要给人挑。判据只两条、都摆在明面上：
//  1. 能装的在前（装不了的排在后面，它们只是目录的一部分，不是待选项）；
//  2. 名字与查询词越贴越前（完全相等 > 前缀 > 包含）。
//
// 不做"哪个更官方"的判断——那需要一张权威名单，猜出来的排名比不排更坏。
func RankRemote(in []RemotePreset, q string) []RemotePreset {
	q = strings.ToLower(strings.TrimSpace(q))
	score := func(p RemotePreset) int {
		s := 0
		if p.Installable {
			s += 100
		}
		name := strings.ToLower(p.ID + " " + p.Name)
		if q != "" {
			switch {
			case strings.EqualFold(strings.ToLower(p.Name), q), strings.EqualFold(strings.ToLower(p.ID), q):
				s += 30
			case strings.HasPrefix(name, q):
				s += 20
			case strings.Contains(name, q):
				s += 10
			}
		}
		return s
	}
	out := append([]RemotePreset(nil), in...)
	// 稳定排序：同分保持注册表原顺序，避免同一查询两次排出不同的样子
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && score(out[j]) > score(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
