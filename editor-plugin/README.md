# Gleam × 编辑器插件接入清单

本目录提供 Gleam 的编辑器插件接入清单（`gleam.plugin.json`），对应设计文档 §6.3「编辑器插件集成」与路线图 Phase 3。

> 这里的「编辑器插件」指**任意编辑器 / IDE**：Gleam 对外只暴露一条通用的 JSON-RPC 2.0 over stdio 通道，
> 谁都能接，不绑定任何具体宿主。ZCode 是搭建本项目所用的开发环境，不是集成目标。

## 接入方式

Gleam 以**独立进程**运行，宿主（编辑器 / IDE）通过 stdio 启动并通信：

```bash
gleam serve        # JSON-RPC 2.0 over stdio（ndjson 分帧）
```

清单要点（`gleam.plugin.json`）：

- `entry`：stdio 启动命令与协议声明；
- `methods`：宿主 ↔ Gleam 全部方法（目标提交/进度推送/审批回路/工具/记忆/技能/调度）；
- `safety`：审批走 `goal/ask_approval` 双向请求，超时自动拒绝，高风险操作白名单不可豁免；
- `web_ui`：桌面端应用模式入口（`gleam app`，关闭窗口自动退出）。

## 宿主侧接入步骤

1. 将本目录注册为编辑器插件（或把 `entry` 写入 MCP/自定义 stdio 服务配置）；
2. 宿主启动 `gleam serve` 后先发送 `initialize` 完成握手；
3. 用 `goal/submit` 提交目标，监听 `goal/progress` / `goal/completed` 渲染进度与结果；
4. 收到 `goal/ask_approval` **请求**时弹确认卡片，并以相同 id 应答 `{"approved": bool, "note": "..."}`；
5. 收到 `agent/suggest_skill` 时提示用户可调用 `skills/save` 固化技能。

协议细节见 README「目标模式 JSON-RPC API（编辑器插件接入）」一节。
