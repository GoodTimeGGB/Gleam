# Gleam (微光)

Read this in other languages: [English](README.en.md) · [简体中文](README.md) · [日本語](README.ja.md)

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

Gleam is neither a chat box nor a dumb task runner. It is a **desktop agent with memory, judgment, and the ability to drive work forward**. The core is built from scratch in Go with **zero third-party Go dependencies** and ships as a single binary of about **10 MB**; the desktop shell is Electron, packaging that core into an installer you **double-click and use**.

**Local-first** means conversations, memory, task state, and audit logs stay on your machine by default. It does **not** mean an offline LLM: when you connect a real model, Gleam calls the API endpoints you configure. Mock mode can run without network. See [Local-first honesty](#local-first-honesty) below.

---

## Preview

<p align="center">
  <img src="artifacts/gleam-desktop-2026-10-10.png" alt="Gleam desktop" width="90%">
</p>

<p align="center">
  <img src="artifacts/settings-tab-llm-2026-10-10.png" alt="Model settings" width="45%">
  <img src="artifacts/settings-tab-engine-2026-10-10.png" alt="Engine settings" width="45%">
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
| **Core** | Single file ~10 MB, pure Go (no cgo), cross-compiled for multiple platforms |
| **Desktop shell** | Electron over the local core; Windows gets an assisted installer with Node and uv bundled — **install and use, no environment setup** |
| **Extensibility** | 27 built-in tools + MCP servers for hot-plugged external tools |
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

### Three-layer memory and the context window

- **Short-term:** recent conversation buffer; **working:** task results on disk, reusable across sessions; **long-term:** in-house lexical index (Chinese bigrams + FNV + cosine), JSON persistence, no third-party vector DB.
- **Context window:** the size comes from a built-in model table (128k when unknown, or set by hand or through the 200K / 400K / 1M choices), and occupancy is measured from the **real prompt tokens of the last request**.
- **Automatic compression:** at **90%** once the task has finished, or **96%** while it is still running — then the task carries on. Turns that spill outside the window are summarized at any time, never dropped.

### Task isolation (Git worktrees)

When enabled, each task runs inside its own worktree copy and your main workspace never moves; changes stay on the task branch. After a task finishes, copies with **no uncommitted changes** are removed automatically while copies with changes are always kept. When off, tasks run directly in your workspace.

### Safety gating

Modes `auto` / `plan_first` / `interactive`. Per-tool permissions (read-only / need approval / full access). High-risk actions show a plan and wait for confirmation. Full audit trail on disk. Change lists with pre-write restore; egress logging (hostname + bytes only, never payload content).

### Model providers

Presets for nine vendor entry points (Zhipu GLM, DeepSeek, Kimi, Tongyi, Volcengine Ark, MiniMax, OpenAI, Anthropic, OpenRouter). Supports token / coding / agent package styles where applicable, plus an optional faster **helper model** alongside the main one.

### Skills, personas, and more

- **Skills:** execute → harden → reuse, YAML-versioned, success-rate tracking
- **Personas:** seven scene templates (general, analyst, creator, engineer, PM, researcher, ops)
- Goal mode with live progress, MCP + skill marketplace hooks, GEO post-creation citation checks, growth log, cost dashboard, task budgets, loop detection
- Bilingual UI in Chinese and English (switched in Settings; **task progress, notifications and errors coming from the backend stay in their original language**)

---

## Architecture (brief)

```
cmd/gleam/              Entry points (app / serve / goal / webui / desktop-sidecar …)
internal/
  agent/                Autonomous engine: planner → executor → reflector
  worktree/             Git worktree lifecycle per task
  harness/
    registry/           Tool registry (hot register / replace / unregister)
    memory/             Three-layer memory + context compaction
    safety/             Safety gate + full audit log
    scheduler/          Cron, interval, file watch, HTTP callbacks
    skill/              Skill harden / version / reuse
  tools/                File / shell / web / git / desktop / MCP client
  llm/                  OpenAI Chat · OpenAI Responses · Anthropic Messages (SSE)
  server/               JSON-RPC 2.0 over stdio
  webui/                HTTP REST + SSE + embedded frontend (Chinese and English)
  eval/                 Prompt and behavior regression evals
pkg/types/              Cross-layer types
desktop/                Electron desktop shell (main process, preload, packaging)
```

**Stack notes:** Go 1.22+ with `go.mod` limited to the standard library; JSON-RPC (stdio) and HTTP REST + SSE for Web UI; embedded SPA via `go:embed`; self-written YAML subset parser with hot-reload settings overlay. The desktop shell is Electron (asar-packed, pages sandboxed, reaching the loopback service through an `app://` proxy).

---

## Platforms

| Platform | Current shape |
| :--- | :--- |
| **Windows** | **Desktop installer is the main path**: assisted NSIS setup (choose all users / just me, change the install directory, a finish-page checkbox to launch, desktop and Start Menu shortcuts created automatically) with Node and uv bundled, ready right after install. The installer picks its language from the system region: Chinese for China, English everywhere else. |
| **macOS / Linux** | Browser / service fallback today: `gleam app` opens the system browser when tray is unavailable; or run `gleam webui` for the service only. The macOS desktop shell is packaged as an unpacked directory only — no dmg yet. |

Binaries are not code-signed or Apple-notarized yet. On first run, Windows SmartScreen warns once (for the installer and the binary alike) and macOS Gatekeeper blocks — use “More info → Run anyway” or clear quarantine (`xattr`) as documented on the [website download section](http://gleam.wangjn.top/#download).

---

## New in 0.0.5

- **Windows desktop installer**: Electron shell plus assisted NSIS setup, shortcuts created automatically, usable right after install. Node (with npx) and uv (with uvx) are bundled and **only injected into Gleam's own process tree PATH — your system PATH is never touched**.
- **First-run “Preparing environment”**: real probes (`node -v` / `uv --version` / `git --version`) with no fake animation and no blocking; this screen follows the system region too.
- **Context window and auto-compression**: the composer shows window occupancy, and at 90% (task finished) or 96% (task running) the next round automatically carries fewer turns.
- **Task isolation (worktrees)**: every task runs in its own copy directory and changes stay on the task branch — never merged back silently.
- **Git tools**: `git.branch` / `git.commit` / `git.push`, all through the safety gate (every push needs your approval).
- **App menu bar**: File / Edit / View / Help with hover switching and keyboard support; in the desktop shell the items map to real window actions over preload IPC, and items a browser cannot do are hidden.
- **Terminal panel** (Ctrl+J) and **right sidebar** (Ctrl+Shift+B): workspace files, built-in browser and terminal shortcuts.
- **Feedback** (Ctrl+Alt+F): with screenshots; stored locally first, sent remotely only when a backend is configured.
- **Editable shortcuts**: search, record, conflict detection, reset to defaults.
- **Settings v2**: full-page grouped navigation; the Models page adds/edits models with a real connectivity check and never echoes keys; SSH hosts are read from `~/.ssh/config` (Host names only); archived tasks can be deleted; the Network page runs connection checks and shows the proxy source.

---

## Security: local API token

- The Web UI listens on loopback only (default `127.0.0.1`) and checks Host / Origin.
- A per-launch API token is written to `~/.gleam/webui.token` (mode 0600). Every `/api/*` request must send it in the `X-Gleam-Token` header; the built-in UI does this automatically.
- For scripts and CI (for example hooks, `POST /api/hooks/<name>`), read the file:
  `curl -H "X-Gleam-Token: $(cat ~/.gleam/webui.token)" -X POST http://127.0.0.1:8787/api/hooks/daily-report`
- Set `GLEAM_WEBUI_TOKEN` to pin a fixed token. Do not expose the port to your LAN or the internet.
- API keys live in the local credential store and are only sent to the endpoint host you configured; the UI only shows whether a key is set.

---

## Install and run

### Windows: download the installer (recommended)

Grab `Gleam Setup <version>.exe` from [Releases](https://github.com/gleam-ai/Gleam/releases) and walk through the four steps — **you do not need to install Node, uv or any other runtime yourself**. The installer speaks Chinese or English depending on the system region.

If you only want the command-line core without the desktop shell, download the single binary:

```bash
./gleam app                                          # desktop window
./gleam goal "Create hello.txt here" --mock-llm      # offline demo, no API key
./gleam webui                                        # Web UI only
```

### Connect a real model

```bash
export GLEAM_API_KEY=your_api_key
./gleam goal "List Markdown files here and summarize" --mode auto
```

### Build from source

```bash
# Core (single binary)
go build -trimpath -ldflags="-s -w" -o bin/gleam ./cmd/gleam

# Desktop installer (Windows; needs Node 20+)
cd desktop
npm ci
npm run pack:win      # -> <system temp dir>/gleam-pack/Gleam-Setup.exe
```

Multi-platform cross builds of the core live in `scripts/build-desktop.sh`; the Windows install helper is `scripts/install.ps1`. Engineering notes for the desktop shell are in [`desktop/README.md`](desktop/README.md).

---

## Tools and MCP

**27 built-in tools**, including:

| Group | Tools |
| :--- | :--- |
| File | `file.list` `file.read` `file.write` `file.mkdir` `file.move` `file.delete` `file.search` |
| Shell | `shell.exec` |
| Web | `web.fetch` |
| Git | `git.branch` `git.commit` `git.push` |
| Desktop | `desktop.clipboard.read` `desktop.clipboard.write` `desktop.screenshot` `desktop.notify` `desktop.snippets` |
| Memory | `memory.save` `memory.search` `memory.delete` |
| Schedule | `schedule.create` `schedule.list` `schedule.delete` |
| Skills | `skill.list` `skill.run` |
| Prompts | `prompt.run` |
| Reply | `reply` |

**MCP:** configure external servers under `mcp:` in config (command + args + trust level). Tools register into the same registry at startup, or install them in one click from the in-app market (which uses the bundled npx / uvx).

---

## Configuration and data directories

| Path | Role |
| :--- | :--- |
| `configs/config.yaml` | Example / shipped config (YAML subset, 2-space indent) |
| `~/.gleam/` | Default **data directory** (`--data-dir` overrides) |
| `~/.gleam/settings.yaml` | Local settings overlay (hot-applied on save) |
| `~/.gleam/memory/` `tasks/` `skills/` | Long-term memory, task archives, skills |
| `~/.gleam/worktrees/` | Per-task copies (when worktree isolation is on) plus their metadata |
| `~/.gleam/schedules.json` | Scheduler state |
| `~/.gleam/audit.jsonl` | Safety / egress audit log |
| `~/.gleam/browser-profile/` | Embedded browser profile (desktop) |
| `~/.gleam/webui.token` | Per-launch Web UI API token (0600). Every Web UI API request needs it in the `X-Gleam-Token` header; the in-app UI gets it automatically. Preset it with `GLEAM_WEBUI_TOKEN` |

Prefer injecting secrets via `GLEAM_API_KEY` rather than committing keys into YAML.

---
---

## Web UI API reference

The Web UI REST endpoints match the routes registered in code one for one; `scripts/check-api-docs.py` fails the build when the list and the implementation drift apart. Every endpoint needs `X-Gleam-Token` (see the section above).

| エンドポイント | 説明 |
| :--- | :--- |
| `GET /api/info` | App version, platform and runtime shape; the UI reads its version from here instead of copying a literal |
| `POST /api/heartbeat` · `POST /api/show-window` | Local liveness heartbeat / bring the window to the front |
| `GET /api/settings` · `POST /api/settings` | Read and write the settings overlay (applied hot on save) |
| `GET /api/onboarding` · `POST /api/onboarding` | First-run onboarding state and submission |
| `GET /api/providers` | Provider presets (official endpoints × package styles) |
| `POST /api/llm/models` · `POST /api/llm/test` | Fetch the provider model list / one minimal connectivity test using the form as-is |
| `GET /api/account` · `POST /api/account/configure` · `POST /api/account/signup` · `POST /api/account/signin` · `POST /api/account/signout` · `POST /api/account/oauth` | Cloud account: status, project connection, sign-up / sign-in / sign-out / OAuth |
| `GET /api/local-data` | Size and make-up of the local data directory |
| `GET /api/network` · `GET /api/connections` | Connectivity check / the standing-boundary ledger for connections and egress |
| `GET /api/security/audit` | Safety-gate trail (blocked / allowed / flagged by the review model) |
| `GET /api/workspace` · `POST /api/workspace` · `POST /api/workspace/clear` | Read / switch workspace, list of open workspaces, and clearing |
| `GET /api/fs` | Browse directories inside the workspace (for the file picker) |
| `GET /api/goals` · `POST /api/goals` · `GET /api/goals/{id}` · `POST /api/goals/{id}/cancel` · `GET /api/goals/{id}/diff` · `POST /api/goals/{id}/revert` · `DELETE /api/goals/{id}` | Goals / tasks: submit, query, cancel, diff and revert changes, delete an archive |
| `GET /api/approvals` · `POST /api/approvals/{id}` | Operations awaiting approval, and the verdict |
| `GET /api/events` | SSE event stream |
| `GET /api/conversations` · `POST /api/conversations` · `GET /api/conversations/{id}` · `PATCH /api/conversations/{id}` · `DELETE /api/conversations/{id}` · `POST /api/conversations/{id}/activate` | Conversations: list, create, read, rename, delete and switch |
| `POST /api/conversation/reset` | Clear the current conversation context (long-term memory is kept) |
| `GET /api/context` · `POST /api/context/compress` · `POST /api/context/clear` | Context window reading / compress now / clear summaries and pending history |
| `GET /api/memory` · `POST /api/memory` · `DELETE /api/memory/{id}` | Long-term memory: search, write, delete |
| `GET /api/spaces` · `POST /api/spaces` · `PATCH /api/spaces/{id}` · `DELETE /api/spaces/{id}` · `POST /api/spaces/{id}/activate` | Spaces: list, create, rename, delete and switch |
| `GET /api/schedules` · `POST /api/schedules` · `DELETE /api/schedules/{name}` · `POST /api/schedules/{name}/enabled` · `POST /api/schedules/{name}/notify` | Schedules: list, create, delete, enable/disable and one-off notify |
| `POST /api/hooks/{name}` | HTTP hook triggered by external scripts / CI (every schedule gets one automatically) |
| `GET /api/skills` · `POST /api/skills` · `POST /api/skills/{name}/run` · `POST /api/skills/{name}/enabled` · `DELETE /api/skills/{name}` | Skills: list, harden, run, enable/disable and delete |
| `GET /api/market/skills` · `POST /api/market/skills/install` · `GET /api/market/mcp` · `POST /api/market/mcp/install` · `GET /api/market/runtimes` · `GET /api/market/sources` | Market: skill templates and MCP catalog, one-click install, available runtimes and catalog sources |
| `GET /api/mcp` · `POST /api/mcp` · `DELETE /api/mcp/{name}` · `POST /api/mcp/{name}/enabled` · `POST /api/mcp/{name}/reconnect` | MCP servers: list, add, remove, enable/disable and reconnect |
| `GET /api/tools` · `POST /api/tools/permission` · `POST /api/tools/call` | Tools registered by the engine, their access levels, and direct calls |
| `GET /api/growth` · `GET /api/growth/recent` · `GET /api/roles` | Growth stats (level, event counts) / recent events / available roles |
| `GET /api/readiness` | Readiness check: environment and capability self-check |
| `GET /api/go-status` · `POST /api/go-status` · `POST /api/go-status/install` | Go toolchain detection and installation |
| `GET /api/ssh/hosts` | Read host aliases from ~/.ssh/config (Host names only) |
| `GET /api/geo` · `POST /api/geo/analyze` · `DELETE /api/geo/history` | GEO: state, analyze a piece of content, clear analysis history |
| `GET /api/feedback` · `POST /api/feedback` · `GET /api/feedback/attachment` · `GET /api/feedback/context` · `POST /api/feedback/{id}/resend` · `DELETE /api/feedback/{id}` | Feedback: list, submit, attachments and context snapshot, resend and delete |
| `GET /api/import/scan` · `POST /api/import/apply` | Import memory and rules from other local AI tools: scan and apply |
| `GET /api/cues` · `POST /api/cues/{id}/adopt` · `POST /api/cues/{id}/dismiss` · `POST /api/cues/unsuppress` | Follow-up goals: list, adopt, dismiss and undo a dismissal |
| `GET /api/worktrees` · `DELETE /api/worktrees/{id}` | Task copies managed by Gleam: list and delete |
| `GET /api/update/check` · `POST /api/update/apply` | Check for updates / download and replace (both need your click) |

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
| [Desktop shell](desktop/README.md) | Install flow, runtime visibility, dev and verification commands |
| [Contributing](CONTRIBUTING.md) | Local dev, commit rules, org-wide contribution entry |
| [Security](SECURITY.md) | Vulnerability reporting and local-first security boundary |
| [CHANGELOG](CHANGELOG.md) | Release history |

---

## Contributing and security

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR (Conventional Commits, signed commits, DCO-style sign-off).
- Report vulnerabilities per [SECURITY.md](SECURITY.md). Do not file security issues as public GitHub issues when a private channel is required.
- Org-wide policies live under [gleam-ai/.github](https://github.com/gleam-ai/.github).

---

## License

[MIT License](LICENSE)

> Gleam is not chasing “AI that acts more human.” It aims to be a **more reliable coworker**.
