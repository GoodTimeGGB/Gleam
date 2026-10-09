# Gleam（微光）

Read this in other languages: [English](README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md)

**本地优先的桌面 AI 智能体——眼里有活，心里有你。**

<p align="center">
  <a href="https://github.com/gleam-ai/Gleam/releases"><img src="https://img.shields.io/github/v/release/gleam-ai/Gleam?label=version&color=blue" alt="Release"></a>
  <a href="https://github.com/gleam-ai/Gleam/stargazers"><img src="https://img.shields.io/github/stars/gleam-ai/Gleam?style=social" alt="Stars"></a>
  <a href="https://github.com/gleam-ai/Gleam/blob/master/LICENSE"><img src="https://img.shields.io/github/license/gleam-ai/Gleam" alt="License"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://github.com/gleam-ai/Gleam/actions"><img src="https://img.shields.io/badge/build-passing-brightgreen" alt="Build"></a>
</p>

<p align="center">
  你负责做决定，具体执行交给我。
</p>

Gleam 不是聊天机器人，也不是任务执行器，而是一个**有记忆、有判断、能主动推进工作**的桌面智能体。基于 Go 从零自研，**零第三方 Go 依赖**，单文件二进制约 **10MB**。

**本地优先**指对话、记忆、任务状态与审计默认落在本机。这**不等于**离线大模型：接入真实 LLM 时仍会向你配置的厂商 API 出网；Mock 模式可不联网体验。详见下方[关于「本地优先」](#关于本地优先)。

---

## 预览

<p align="center">
  <img src="artifacts/gleam-desktop-01.png" alt="Gleam 桌面端" width="90%">
</p>

<p align="center">
  <img src="artifacts/settings-tab-llm-2026-09-09T06-00-49-346Z.png" alt="模型设置" width="45%">
  <img src="artifacts/settings-tab-engine-2026-09-09T06-01-00-565Z.png" alt="引擎设置" width="45%">
</p>

---

## Star 趋势

<p align="center">
  <a href="https://star-history.com/#gleam-ai/Gleam&Date">
    <img src="https://api.star-history.com/svg?repos=gleam-ai/Gleam&type=Date" alt="Star History" width="600">
  </a>
</p>

---

## 产品概览

| 主题 | 说明 |
| :--- | :--- |
| **是什么** | 本地优先的桌面 AI 智能体：文件整理、任务规划、定时执行与工具驱动自动化 |
| **不是什么** | 托管 SaaS 聊天产品、云端 RAG 平台，或「完全离线大模型」 |
| **二进制** | 单文件 ~10MB，纯 Go（无 cgo），多平台交叉编译 |
| **扩展** | 内置 23 种工具 + MCP 热插拔外部工具 |
| **接入面** | 桌面端、CLI（`goal` / `doctor` 等）、Web UI、JSON-RPC over stdio（编辑器插件） |

---

## 核心功能

### 自主任务引擎

Plan → Execute → Reflect 三阶段循环，DAG 依赖并发执行，0–100 完成度评分，低分自动重规划。

### 三种任务模式

| 模式 | 场景 |
| :--- | :--- |
| **对话** | 直连 LLM 快速问答，不经规划/执行 |
| **工作** | 全流程 Plan-Execute-Reflect |
| **编程** | 最小 diff + 构建验证 |

### 三层记忆

- **短期**：最近约 20 轮对话缓冲
- **工作**：任务结果落盘，跨会话可用
- **长期**：自研词法索引（中文双字组 + FNV + 余弦），JSON 持久化，无第三方向量库

### 安全门控

三种安全模式（`auto` / `plan_first` / `interactive`），每个工具可独立设置只读放行 / 需批准 / 完全访问，高风险操作执行前展示计划请求确认，全量审计落盘。

### 模型接入

9 家厂商官方入口预设（智谱 GLM / DeepSeek / Kimi / 通义 / 火山方舟 / MiniMax / OpenAI / Anthropic / OpenRouter），支持 Token 按量 / Coding 编程 / Agent 智能体等套餐形态一键切换。

### 技能、角色与更多

- **技能系统**：执行 → 固化 → 一键复用，YAML 版本化，成功率统计
- **专家角色**：7 个预置场景模板（通用 / 数据分析师 / 内容创作者 / 开发工程师 / 项目经理 / 研究员 / 运维专家）
- 上下文自动压缩、目标模式实时进度、MCP + 技能市场入口
- GEO 生成式引擎优化评估、成长日志、消耗看板、任务预算熔断、防打转检测
- 改动清单与写前还原；出网留痕（只记主机名与字节量，绝不记内容）

---

## 架构（简述）

```
cmd/gleam/              主入口（app / serve / goal / webui / eval / doctor ...）
internal/
  agent/                自主任务引擎：planner → executor → reflector
  harness/
    registry/           工具注册表（热注册/替换/注销）
    memory/             三层记忆 + 上下文自动压缩
    safety/             安全门控 + 全量审计落盘
    scheduler/          Cron + 固定间隔 + 文件监听 + HTTP 回调
    skill/              技能固化/版本化/复用
  tools/                文件 / shell / web / 桌面集成 / MCP 客户端
  llm/                  三协议客户端（OpenAI Chat / Responses / Anthropic）
  server/               JSON-RPC 2.0 over stdio
  webui/                HTTP REST + SSE + 内嵌前端
  eval/                 提示词与行为回归评测
pkg/types/              跨层类型
```

**技术栈要点：** Go 1.22+，`go.mod` 仅标准库；JSON-RPC（stdio）与 HTTP REST + SSE；`go:embed` 内嵌前端；自研 YAML 子集解析，设置覆盖层保存即热生效。

---

## 平台说明

| 平台 | 当前形态 |
| :--- | :--- |
| **Windows** | 桌面端为主：托盘常驻、单实例、内嵌窗口（`gleam app` / Desktop 包） |
| **macOS / Linux** | 暂以浏览器 / 服务模式为主：`gleam app` 在无托盘实现时回退为打开系统默认浏览器；也可用 `gleam webui` 只起服务 |

二进制尚未做代码签名与 Apple 公证。首次运行可能被 Windows SmartScreen 或 macOS Gatekeeper 拦截——按官网[下载区](http://gleam.wangjn.top/#download)说明放行或清除隔离属性（`xattr`）。

---

## 安装与运行

### 下载

前往 [Releases](https://github.com/gleam-ai/Gleam/releases) 下载对应平台二进制。

### 运行

```bash
# 桌面端（Windows 推荐）
./gleam app

# 离线体验（Mock 模型，不需要 API Key）
./gleam goal "在当前目录创建 hello.txt 并写入内容" --mock-llm

# 接入真实模型（会向你配置的 API 出网）
export GLEAM_API_KEY=你的APIKey
./gleam goal "列出当前目录的 Markdown 文件并总结" --mode auto

# 仅 Web UI
./gleam webui
```

### 从源码构建

```bash
go build -trimpath -ldflags="-s -w" -o bin/gleam ./cmd/gleam
bash scripts/build-desktop.sh   # 多平台交叉编译
```

Windows 安装辅助：`scripts/install.ps1`。

---

## 工具与 MCP

**内置 23 种工具**，包括：

| 分组 | 工具 |
| :--- | :--- |
| 文件 | `file.list` `file.read` `file.write` `file.mkdir` `file.move` `file.delete` `file.search` |
| Shell | `shell.exec` |
| Web | `web.fetch` |
| 桌面 | `desktop.clipboard.read` `desktop.clipboard.write` `desktop.screenshot` `desktop.notify` `desktop.snippets` |
| 记忆 | `memory.save` `memory.search` `memory.delete` |
| 调度 | `schedule.create` `schedule.list` `schedule.delete` |
| 技能 | `skill.list` `skill.run` |
| 回复 | `reply` |

**MCP：** 在配置的 `mcp:` 下声明外部服务器（command + args + trust）。启动时注册进同一工具表。

---

## 配置与数据目录

| 路径 | 作用 |
| :--- | :--- |
| `configs/config.yaml` | 示例 / 随仓库提供的配置（YAML 子集，2 空格缩进） |
| `~/.gleam/` | 默认**数据目录**（可用 `--data-dir` 覆盖） |
| `~/.gleam/settings.yaml` | 本机设置覆盖层（保存即热生效） |
| `~/.gleam/memory/` `tasks/` `skills/` | 长期记忆、任务归档、技能 |
| `~/.gleam/schedules.json` | 调度状态 |
| `~/.gleam/audit.jsonl` | 安全 / 出网审计 |
| `~/.gleam/browser-profile/` | 桌面端内嵌浏览器配置 |

API Key 建议用环境变量 `GLEAM_API_KEY` 注入，不要写入并提交 YAML。

---

## 关于「本地优先」

**本地优先 ≠ 离线大模型。**

- 对话、记忆、任务状态、技能与审计默认留在**本机**。
- **Mock**（`--mock-llm` / `provider: mock`）可不调用云端模型即可体验智能体流程。
- **真实模型**仍会向你配置的 **API** 发送提示词；出网只记主机名与字节量，不记内容。
- 可选的云端登录 / 反馈路径同样出网，并纳入同一套审计。

已知限制与刻意不做见 [docs/known-limits.md](docs/known-limits.md)。

---

## 文档

| 文档 | 说明 |
| :--- | :--- |
| [已知限制与刻意不做](docs/known-limits.md) | **唯一公开口径**：已知限制 / 被否决方案 / 刻意不做 |
| [贡献指南](CONTRIBUTING.md) | 本地开发、提交约定、组织级规范入口 |
| [安全策略](SECURITY.md) | 漏洞报告与本地优先安全边界 |
| [CHANGELOG](pack/CHANGELOG.md) | 历次变更记录 |

---

## 贡献与安全

- 提 PR 前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md)（Conventional Commits、签名提交、DCO 风格 sign-off）。
- 漏洞请按 [SECURITY.md](SECURITY.md) 报告；需要私密渠道时不要用公开 Issue。
- 组织级规范见 [gleam-ai/.github](https://github.com/gleam-ai/.github)。

---

## 许可

[MIT License](LICENSE)

> Gleam 不做「更像人的 AI」，而是做「更可靠的同事」。
