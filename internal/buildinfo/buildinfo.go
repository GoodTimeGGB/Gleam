// Package buildinfo 是「这是哪个版本的 Gleam」这件事的唯一 owner。
//
// 为什么要单独一个包：版本号此前在 CLI、JSON-RPC 服务、Web UI 与 MCP 客户端里各写了一遍
// 字面量——升版本时漏掉任何一处，那一路就对外报一个旧号。而反馈单要带的正是这个号，
// 于是它成了第六处复制。**先问归属，再谈实现**：加一个字段不该顺手复制一个已有的事实。
package buildinfo

// Version is the application version string.
// Release process: bump the default below, and keep website id="dl-version" in sync
// (scripts/check-version-owner.py enforces that). Build scripts also pass
//
//	-X gleam/internal/buildinfo.Version=<same>
//
// so packaged Desktop binaries cannot silently stick on an old default.
// Version must be a package-level var (not const) for -X to take effect.
var Version = "1.0.2"
