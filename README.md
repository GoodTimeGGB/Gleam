# Gleam (微光)

Read this in other languages: [English](README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md)

**A local-first desktop AI agent — eyes on the work, mind on you.**

<p align="center">
  <a href="https://github.com/gleam-ai/Gleam/releases"><img src="https://img.shields.io/github/v/release/gleam-ai/Gleam?label=version&color=blue" alt="Release"></a>
  <a href="https://github.com/gleam-ai/Gleam/stargazers"><img src="https://img.shields.io/github/stars/gleam-ai/Gleam?style=social" alt="Stars"></a>
  <a href="https://github.com/gleam-ai/Gleam/blob/master/LICENSE"><img src="https://img.shields.io/github/license/gleam-ai/Gleam" alt="License"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://github.com/gleam-ai/Gleam/actions"><img src="https://img.shields.io/badge/build-passing-brightgreen" alt="Build"></a>
</p>

<p align="center">
  You decide. Gleam executes.
</p>

Gleam is neither a chat box nor a dumb task runner. It is a **desktop agent with memory, judgment, and the ability to drive work forward**. Built from scratch in Go with **zero third-party Go dependencies**, it ships as a single binary of about **10 MB**.

**Local-first** means conversations, memory, task state, and audit logs stay on your machine by default. It does **not** mean an offline LLM: when you connect a real model, Gleam calls the API endpoints you configure. Mock mode can run without network. See [Local-first honesty](#local-first-honesty) below.

---

## Preview

<p align="center">
  <img src="artifacts/gleam-desktop-01.png" alt="Gleam desktop" width="90%">
</p>

<p align="center">
  <img src="artifacts/settings-tab-llm-2026-09-09T06-00-49-346Z.png" alt="Model settings" width="45%">
  <img src="artifacts/settings-tab-engine-2026-09-09T06-01-00-565Z.png" alt="Engine settings" width="45%">
</p>

---

## Star history

<p align="center">
  <a href="https://star-history.com/#gleam-ai/Gleam&Date">
    <img src="https://api.star-history.com/svg?repos=gleam-ai/Gleam&type=Date" alt="Star History" width="600">
  </a>
</p>

---

## Product overview

| Topic | Detail |
| :--- | :--- |
| **What it is** | Local-first desktop AI agent for file organization, task planning, scheduled work, and tool-driven automation |
| **What it is not** | A hosted SaaS chat product, a cloud RAG platform, or a fully offline LLM |
| **Binary** | Single file ~10 MB, pure Go (no cgo), cross-compiled for multiple platforms |
| **Extensibility** | 23 built-in tools + MCP servers for hot-plugged external tools |
| **Interfaces** | Desktop app, CLI (`goal` / `doctor` / …), Web UI, JSON-RPC over stdio (editor plugins) |

---

## Features

### Autonomous task engine

Plan → Execute → Reflect loop with DAG-aware concurrent steps, 0–100 completion scoring, and automatic re-planning when scores stay low.

### Three task modes

| Mode | Use when |
| :--- | :--- |
| **Chat** | Fast Q&A straight to the LLM (no plan/execute loop) |
| **Work** | Full Plan-Execute-Reflect pipeline |
| **Code** | Minimal diffs plus build verification |

### Three-layer memory

- **Short-term:** recent conversation buffer (default ~20 turns)
- **Working:** task results on disk, reusable across sessions
- **Long-term:** in-house lexical index (Chinese bigrams + FNV + cosine), JSON persistence, no third-party vector DB

### Safety gating

Modes `auto` / `plan_first` / `interactive`. Per-tool permissions (read-only / need approval / full access). High-risk actions show a plan and wait for confirmation. Full audit trail on disk.

### Model providers

Presets for nine vendor entry points (Zhipu GLM, DeepSeek, Kimi, Tongyi, Volcengine Ark, MiniMax, OpenAI, Anthropic, OpenRouter). Supports token / coding / agent package styles where applicable.

### Skills, personas, and more

- **Skills:** execute → harden → reuse, YAML-versioned, success-rate tracking
- **Personas:** seven scene templates (general, analyst, creator, engineer, PM, researcher, ops)
- Context compaction, goal mode with live progress, MCP + skill marketplace hooks
- GEO post-creation citation checks, growth log, cost dashboard, task budgets, loop detection
- Change list + pre-write restore; egress logging (hostname + bytes only, never payload content)

---

## Architecture (brief)

```
cmd/gleam/              Entry points (app / serve / goal / webui / eval / doctor …)
internal/
  agent/                Autonomous engine: planner → executor → reflector
  harness/
    registry/           Tool registry (hot register / replace / unregister)
    memory/             Three-layer memory + context compaction
    safety/             Safety gate + full audit log
    scheduler/          Cron, interval, file watch, HTTP callbacks
    skill/              Skill harden / version / reuse
  tools/                File / shell / web / desktop / MCP client
  llm/                  OpenAI Chat · OpenAI Responses · Anthropic Messages (SSE)
  server/               JSON-RPC 2.0 over stdio
  webui/                HTTP REST + SSE + embedded frontend
  eval/                 Prompt and behavior regression evals
pkg/types/              Cross-layer types
```

**Stack notes:** Go 1.22+ with `go.mod` limited to the standard library; JSON-RPC (stdio) and HTTP REST + SSE for Web UI; embedded SPA via `go:embed`; self-written YAML subset parser with hot-reload settings overlay.

---

## Platforms

| Platform | Current shape |
| :--- | :--- |
| **Windows** | Primary desktop experience: tray, single instance, embedded window (`gleam app` / Desktop package) |
| **macOS / Linux** | Browser / service fallback today: `gleam app` opens the system browser when tray is unavailable; or run `gleam webui` for the service only |

Binaries are not code-signed or Apple-notarized yet. On first run, Windows SmartScreen or macOS Gatekeeper may warn — use “More info → Run anyway” or clear quarantine (`xattr`) as documented on the [website download section](http://gleam.wangjn.top/#download).

---

## Install and run

### Download

Get platform binaries from [Releases](https://github.com/gleam-ai/Gleam/releases).

### Run

```bash
# Desktop (recommended on Windows)
./gleam app

# Offline-capable Mock model (no API key)
./gleam goal "Create hello.txt in the current directory" --mock-llm

# Real model (network egress to your configured provider)
export GLEAM_API_KEY=your_api_key
./gleam goal "List Markdown files here and summarize" --mode auto

# Web UI only
./gleam webui
```

### Build from source

```bash
go build -trimpath -ldflags="-s -w" -o bin/gleam ./cmd/gleam
bash scripts/build-desktop.sh   # multi-platform cross build
```

Windows install helper: `scripts/install.ps1`.

---

## Tools and MCP

**23 built-in tools**, including:

| Group | Tools |
| :--- | :--- |
| File | `file.list` `file.read` `file.write` `file.mkdir` `file.move` `file.delete` `file.search` |
| Shell | `shell.exec` |
| Web | `web.fetch` |
| Desktop | `desktop.clipboard.read` `desktop.clipboard.write` `desktop.screenshot` `desktop.notify` `desktop.snippets` |
| Memory | `memory.save` `memory.search` `memory.delete` |
| Schedule | `schedule.create` `schedule.list` `schedule.delete` |
| Skills | `skill.list` `skill.run` |
| Reply | `reply` |

**MCP:** configure external servers under `mcp:` in config (command + args + trust level). Tools register into the same registry at startup.

---

## Configuration and data directories

| Path | Role |
| :--- | :--- |
| `configs/config.yaml` | Example / shipped config (YAML subset, 2-space indent) |
| `~/.gleam/` | Default **data directory** (`--data-dir` overrides) |
| `~/.gleam/settings.yaml` | Local settings overlay (hot-applied on save) |
| `~/.gleam/memory/` `tasks/` `skills/` | Long-term memory, task archives, skills |
| `~/.gleam/schedules.json` | Scheduler state |
| `~/.gleam/audit.jsonl` | Safety / egress audit log |
| `~/.gleam/browser-profile/` | Embedded browser profile (desktop) |

Prefer injecting secrets via `GLEAM_API_KEY` rather than committing keys into YAML.

---

## Local-first honesty

**Local-first ≠ offline LLM.**

- Conversation history, memory, task state, skills, and audits default to **your machine**.
- **Mock** (`--mock-llm` / `provider: mock`) can exercise the agent **without** calling a cloud model.
- **Real models** still send prompts to the **API base URL you configure**; egress records hostname and byte counts only, never message content.
- Optional cloud login / feedback paths also egress and are audited the same way.

Details and deliberate non-goals: [docs/known-limits.md](docs/known-limits.md).

---

## Documentation

| Doc | Purpose |
| :--- | :--- |
| [Known limits & won't-do](docs/known-limits.md) | **Single public source** for limits, rejected designs, and deliberate non-goals |
| [Contributing](CONTRIBUTING.md) | Local dev, commit rules, org-wide contribution entry |
| [Security](SECURITY.md) | Vulnerability reporting and local-first security boundary |
| [CHANGELOG](pack/CHANGELOG.md) | Release history |

---

## Contributing and security

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR (Conventional Commits, signed commits, DCO-style sign-off).
- Report vulnerabilities per [SECURITY.md](SECURITY.md). Do not file security issues as public GitHub issues when a private channel is required.
- Org-wide policies live under [gleam-ai/.github](https://github.com/gleam-ai/.github).

---

## License

[MIT License](LICENSE)

> Gleam is not chasing “AI that acts more human.” It aims to be a **more reliable coworker**.
