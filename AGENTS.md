# AGENTS.md — Gleam 仓库入口

> 这是**入口**，不是手册：**每多走一步才付那一步的上下文成本**——先读完这页再点开链接。
> 字数预算 ≤ 1950 字符，`scripts/verify.sh` 检查并**告警**（理由见 §4.6.23）。

## 三条铁律

1. **一个事实一个 owner。** 每类事实只住在负责它的那一层，别处**只放链接**。同一条规则两处各写一版就会漂移——发现时把一处改成链接，别两处都更新。
2. **先问归属，再谈实现。** 新行为优先挂到**已成文的扩展点**（技能 / MCP / 规则表 / 场景模板）；改核心循环是最后的选项。先想「属于哪一层」，再想「怎么写」。
3. **改完必须能证明。** 每条可机械判断的规则都配一条 **exit non-zero** 的命令（见下表）；新判据要能被改坏后拦住，所以**每条规则做一次负例控制**（`scripts/mutation/`）。

## 布局

| 目录 | 负责 |
| :--- | :--- |
| `internal/agent` | 循环：规划 / 执行 / 反思 / 验收 / 上下文 |
| `internal/harness` | 权限·记忆·技能·安全·调度·对话·成长 |
| `internal/tools` | 工具注册表与内置工具族 |
| `internal/llm` | 模型协议适配（3 种线协议） |
| `internal/server` · `internal/webui` · `cmd/gleam` | 接入层（JSON-RPC · HTTP+SSE · CLI） |
| `pkg/types` | 跨层类型 |
| `scripts/` | 仓库工程：验证 · 冒烟 · 变异 |

## 命令表（规则 → 命令）

| 规则 | 命令 |
| :--- | :--- |
| 格式·引用·落盘·接口清单 | `gofmt -l ./internal ./pkg ./cmd`；`scripts/check-*.py` |
| 静态检查 | `go vet ./...` |
| 编译 | `go build ./...` |
| 行为 | `go test ./... -count=1` |
| 端到端 | `bash scripts/smoke.sh` · `bash scripts/smoke-replay.sh` |
| 就绪体检 | `bin/gleam.exe doctor --strict` |
| 评测回归 | `bin/gleam.exe eval --layer smoke --strict` |
| 提示词稳定段不许含目标 | `go test ./internal/agent -run PromptCache` |
| 规则 Scope 只允许稳定维度 | 编译期（`internal/agent/rules.go` 的 `Scope` 签名） |
| 指标分母不许共用 | `go test ./internal/harness/growth ./internal/eval` |

**一次跑完前六层**：`bash scripts/verify.sh`（`--quick` 只到行为层；`bin/gleam.exe` 由它建出，**别用 `go run`**）。
**证明判据挡得住**：`python scripts/mutation/batch-a-audit.py`（批次见该目录）。

## 三问自检（改完问自己）

1. **知识外置了吗？** 约束只在我脑子里，下次它就不存在。
2. **正确入口明确吗？** 新人（或 agent）只读这页，能不能找到该改哪。
3. **错误何时被发现？** 有没有一条 exit non-zero 的命令，在**最近的地方**拦住它。

三问的答案写在 `docs/known-limits.md` 头部；`verify.sh` 最后一步会再问一遍。

## 指路

| 想知道 | 去哪 |
| :--- | :--- |
| 系统现状与决策理由 | `Gleam 技术设计文档.md`（§4.6.x，一节一个问题） |
| 已知限制 / 被否决的方案 / 刻意不做 | `docs/known-limits.md` |
| 用户可见的能力清单 | `README.md` |
| 历次外部资料对照与升级清单 | 根目录 `*对照与升级清单.md`（4 份） |
| 本批改了什么 | `pack/CHANGELOG.md` |
