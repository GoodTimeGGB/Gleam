# Gleam（微光）

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

Gleam 不是聊天机器人，也不是任务执行器，而是一个**有记忆、有判断、能主动推进工作**的桌面智能体。基于 Go 从零自研，零第三方依赖，单文件二进制约 10MB。本地优先指数据与执行在本机；真实 LLM 仍需你配置的 API（见下方说明）。

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

## 技术栈

| 层面 | 选型 |
| :--- | :--- |
| **语言** | Go 1.22+，零第三方依赖（`go.mod` 仅标准库） |
| **二进制** | 单文件 ~10MB，纯 Go 零 cgo，五平台交叉编译 |
| **通信** | JSON-RPC 2.0（stdio）· HTTP REST + SSE（Web UI） |
| **前端** | 内嵌单页应用（`go:embed`），深色玻璃拟态，375-1440px 响应式 |
| **LLM 协议** | OpenAI Chat · OpenAI Responses · Anthropic Messages，SSE 流式 |
| **记忆** | 自研词法索引（中文双字组 + FNV 哈希 + 余弦相似度），JSON 持久化 |
| **配置** | 自研 YAML 子集解析，设置覆盖层，保存即热生效 |

---

## 核心功能

### 自主任务引擎

Plan → Execute → Reflect 三阶段循环，DAG 依赖并发执行，0-100 完成度评分，低分自动重规划。

### 三种任务模式

| 模式 | 场景 |
| :--- | :--- |
| **对话** | 直连 LLM 快速问答，不经规划/执行 |
| **工作** | 全流程 Plan-Execute-Reflect |
| **编程** | 最小 diff + 构建验证 |

### 三层记忆

- **短期**：最近 20 轮对话缓冲
- **工作**：任务结果落盘，跨会话可用
- **长期**：自研词法索引，中文双字组 + 余弦检索，零第三方依赖

### 安全门控

三种安全模式（auto / plan_first / interactive），每个工具可独立设置只读放行 / 需批准 / 完全访问，高风险操作执行前展示计划请求确认。

### 模型接入

9 家厂商官方入口预设（智谱 GLM / DeepSeek / Kimi / 通义 / 火山方舟 / MiniMax / OpenAI / Anthropic / OpenRouter），支持 Token 按量 / Coding 编程 / Agent 智能体三类套餐一键切换。

### 技能系统

执行 → 固化 → 一键复用闭环，YAML 版本化，自动统计运行成功率，失败后自动优化参数。

### 专家角色

7 个预置角色（通用 / 数据分析师 / 内容创作者 / 开发工程师 / 项目经理 / 研究员 / 运维专家），每个角色是一份完整场景模板。

### 更多能力

- **上下文自动压缩**：旧对话滚动摘要，token 收益实时估算
- **目标模式**：JSON-RPC 提交目标，实时推送进度与审批
- **市场**：MCP 服务器目录 + 技能模板，关键词搜索一键安装
- **GEO 生成式引擎优化**：创作后自动评估 AI 问答引擎可引用性
- **成长日志**：7 级成长等级，连续活跃天数追踪
- **消耗看板**：实时统计 token、工具调用、重试与耗时
- **任务预算熔断**：调用次数 / token / 时长上限，超限停下征求确认
- **防打转检测**：同一动作重复多轮自动停下
- **改动清单与写前还原**：一键退回任务开始前的状态
- **出网留痕**：每次发包只记主机名与字节量，绝不记内容

---

## 快速开始

### 下载

前往 [Releases](https://github.com/gleam-ai/Gleam/releases) 下载对应平台的二进制文件。

### 运行

```bash
# 桌面端（推荐）
./gleam app

# 离线体验（Mock 模型，不需要 API Key）
./gleam goal "在当前目录创建 hello.txt 并写入内容" --mock-llm

# 接入真实模型
export GLEAM_API_KEY=你的APIKey
./gleam goal "列出当前目录的 Markdown 文件并总结" --mode auto

# Web UI
./gleam webui
```

### 从源码构建

```bash
go build -trimpath -ldflags="-s -w" -o bin/gleam ./cmd/gleam
bash scripts/build-desktop.sh   # 五平台交叉编译
```

---

## 架构

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

---

## 平台说明

| 平台 | 当前形态 |
| :--- | :--- |
| **Windows** | 桌面端为主：托盘常驻、单实例、内嵌窗口（`gleam app` / Desktop 包） |
| **macOS / Linux** | 暂以浏览器 / 服务模式为主：`gleam app` 在无托盘实现时回退为打开系统默认浏览器；也可用 `gleam webui` 只起服务 |

## 关于「本地优先」

**本地优先 ≠ 离线大模型。** 对话、记忆、任务状态与审计默认落在本机；接入真实 LLM 时仍会向你配置的厂商 API 出网（Mock 模式可不联网体验）。出网只记主机名与字节量，不记内容。详见 [已知限制](docs/known-limits.md)。

## 文档

| 文档 | 说明 |
| :--- | :--- |
| [已知限制与刻意不做](docs/known-limits.md) | **唯一公开口径**：已知限制 / 被否决方案 / 刻意不做 |
| [贡献指南](CONTRIBUTING.md) | 本地开发、提交约定、组织级规范入口 |
| [安全策略](SECURITY.md) | 漏洞报告与本地优先安全边界 |
| [CHANGELOG](pack/CHANGELOG.md) | 历次变更记录 |

---

## 许可

[MIT License](LICENSE)

> Gleam 不做"更像人的 AI"，而是做"更可靠的同事"。
