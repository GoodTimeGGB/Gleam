# Gleam（微光）

Read this in other languages: [English](README.en.md) · [简体中文](README.md) · [日本語](README.ja.md)

**ローカルファーストなデスクトップ AI エージェント——仕事を見据え、あなたに寄り添う。**

<p align="center">
  <a href="https://github.com/gleam-ai/Gleam/releases"><img src="https://img.shields.io/github/v/release/gleam-ai/Gleam?label=version&color=blue" alt="Release"></a>
  <a href="https://github.com/gleam-ai/Gleam/stargazers"><img src="https://img.shields.io/github/stars/gleam-ai/Gleam?style=social" alt="Stars"></a>
  <a href="https://github.com/gleam-ai/Gleam/blob/master/LICENSE"><img src="https://img.shields.io/github/license/gleam-ai/Gleam" alt="License"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://github.com/gleam-ai/Gleam/actions"><img src="https://img.shields.io/badge/build-passing-brightgreen" alt="Build"></a>
</p>

<p align="center">
  決断はあなたが。実行は Gleam が。
</p>

Gleam は単なるチャットボットでも、単純なタスク実行器でもありません。**記憶と判断を持ち、仕事を前に進められる**デスクトップエージェントです。コアは Go でゼロから自作し、**サードパーティの Go 依存はゼロ**、単一バイナリ約 **10MB**。デスクトップシェルは Electron で、そのコアを**ダブルクリックで即使える**インストーラにまとめています。

**ローカルファースト**とは、会話・記憶・タスク状態・監査ログが既定で本機に残ることを指します。**オフライン LLM ではありません**：実モデル接続時は設定したベンダー API へ送信します。Mock モードならネットワークなしで体験できます。詳細は[ローカルファーストの正直な説明](#ローカルファーストの正直な説明)を参照してください。

---

## プレビュー

<p align="center">
  <img src="artifacts/gleam-desktop-2026-10-10.png" alt="Gleam デスクトップ" width="90%">
</p>

<p align="center">
  <img src="artifacts/settings-tab-llm-2026-10-10.png" alt="モデル設定" width="45%">
  <img src="artifacts/settings-tab-engine-2026-10-10.png" alt="エンジン設定" width="45%">
</p>

---

## Star 推移

<p align="center">
  <a href="https://star-history.com/#gleam-ai/Gleam&Date">
    <img src="https://api.star-history.com/svg?repos=gleam-ai/Gleam&type=Date" alt="Star History" width="600">
  </a>
</p>

---

## 製品概要

| 項目 | 内容 |
| :--- | :--- |
| **できること** | ファイル整理・タスク計画・定期実行・ツール駆動の自動化向けローカルファーストなデスクトップエージェント |
| **ではないもの** | ホスト型 SaaS チャット、クラウド RAG 基盤、完全オフライン LLM |
| **コア** | 単一ファイル約 10MB、純 Go（cgo なし）、マルチプラットフォーム向けクロスビルド |
| **デスクトップシェル** | Electron + 本機コア。Windows は補助インストーラを提供し、Node と uv を同梱——**インストールすれば即使え、環境構築は不要** |
| **拡張** | 組み込みツール 27 種 + MCP による外部ツールのホットプラグ |
| **インターフェース** | デスクトップ、CLI（`goal` / `doctor` など）、Web UI、stdio 上の JSON-RPC（エディタプラグイン） |

---

## 機能

### 自律タスクエンジン

Plan → Execute → Reflect のループ。DAG 依存の並列実行、0–100 の完了度スコア、低スコア時の自動再計画。

### 3 つのタスクモード

| モード | 用途 |
| :--- | :--- |
| **対話** | LLM への直接 Q&A（計画/実行ループなし） |
| **作業** | Plan-Execute-Reflect のフルパイプライン |
| **コーディング** | 最小 diff + ビルド検証 |

### 3 層メモリとコンテキストウィンドウ

- **短期:** 直近の会話バッファ；**作業:** タスク結果をディスク保存しセッション横断で再利用；**長期:** 自作の語彙インデックス（中国語バイグラム + FNV + コサイン）、JSON 永続化、外部ベクトル DB なし。
- **コンテキストウィンドウ:** サイズは組み込みのモデル表から判定（不明なら 128k。200K / 400K / 1M の選択肢、手入力も可）。使用率は**直近リクエストの実 prompt tokens** から算出します。
- **自動圧縮:** タスクが終わっていれば **90%**、まだ実行中なら **96%** で次のターンの持ち越しを自動的に絞り、そのままタスクを続行します。ウィンドウから溢れた古い会話はいつでも要約され、失われません。

### タスク分離（Git worktree）

有効にすると各タスクは自分の worktree コピー内で実行され、メインのワークスペースは一切動きません。変更はタスクブランチに残ります。タスク終了後、**未コミットの変更がない**コピーは自動で削除され、変更のあるコピーは必ず残ります。無効時はワークスペース上で直接実行します。

### 安全ゲート

モード `auto` / `plan_first` / `interactive`。ツールごとの権限（読み取り専用 / 承認必要 / フルアクセス）。高リスク操作は計画提示のうえ確認待ち。監査はディスクに全量保存。変更一覧と書き込み前リストア；送信記録（ホスト名とバイト数のみ、本文は記録しない）。

### モデル接続

9 ベンダーの公式入口プリセット（智譜 GLM / DeepSeek / Kimi / 通義 / 火山方舟 / MiniMax / OpenAI / Anthropic / OpenRouter）。トークン従量 / Coding / Agent などの形態に対応し、メインモデルとは別に速い**補助モデル**も設定できます。

### スキル・ペルソナほか

- **スキル:** 実行 → 固化 → 再利用、YAML バージョン管理、成功率トラッキング
- **ペルソナ:** 7 つのシーンテンプレート（汎用 / 分析 / 制作 / 開発 / PM / 研究 / 運用）
- 目標モードの進捗配信、MCP + スキル市場フック、GEO 評価、成長ログ、コスト看板、タスク予算、ループ検出
- UI は中国語 / 英語のバイリンガル（設定で切替。**バックエンドが返すタスク進捗・通知・エラーは原文のまま**）

---

## アーキテクチャ（概要）

```
cmd/gleam/              エントリ（app / serve / goal / webui / desktop-sidecar …）
internal/
  agent/                自律エンジン: planner → executor → reflector
  worktree/             タスクごとの git worktree ライフサイクル
  harness/
    registry/           ツール登録（ホット登録 / 置換 / 解除）
    memory/             3 層メモリ + コンテキスト圧縮
    safety/             安全ゲート + 監査ログ
    scheduler/          Cron・間隔・ファイル監視・HTTP コールバック
    skill/              スキル固化 / 版管理 / 再利用
  tools/                ファイル / shell / web / git / デスクトップ / MCP クライアント
  llm/                  OpenAI Chat · Responses · Anthropic Messages（SSE）
  server/               JSON-RPC 2.0 over stdio
  webui/                HTTP REST + SSE + 埋め込みフロント（中国語・英語）
  eval/                 プロンプト / 行動リグレッション評価
pkg/types/              横断型
desktop/                Electron デスクトップシェル（メインプロセス / preload / パッケージ設定）
```

**スタック:** Go 1.22+（`go.mod` は標準ライブラリのみ）、JSON-RPC（stdio）と HTTP REST + SSE、`go:embed` 埋め込み SPA、自作 YAML サブセットとホットリロード設定オーバーレイ。デスクトップシェルは Electron（asar、ページはサンドボックス、`app://` プロキシ経由でループバックサービスへ）。

---

## プラットフォーム

| プラットフォーム | 現状 |
| :--- | :--- |
| **Windows** | **デスクトップインストーラが主経路**：NSIS の補助インストール（「全ユーザー / 自分のみ」選択、インストール先変更、完了ページから起動、デスクトップとスタートメニューのショートカット自動作成）に Node と uv を同梱し、インストール直後から使えます。インストーラの言語は**システムの地域に追従**：中国地域なら中国語、それ以外は英語。 |
| **macOS / Linux** | 当面はブラウザ / サービス中心：トレイ未実装時は `gleam app` が既定ブラウザを開く；`gleam webui` でサービスのみも可。macOS のデスクトップシェルは解凍ディレクトリまでで、dmg はまだありません。 |

バイナリは未コードサイン・未 Apple 公証です。初回は Windows SmartScreen に一度止められ（インストーラもバイナリも同様）、macOS では Gatekeeper に止められます。[サイトのダウンロード案内](http://gleam.wangjn.top/#download)に従い実行許可または隔離属性解除（`xattr`）をしてください。

---

## 1.1.1 の新機能

- **Windows デスクトップインストーラ**：Electron シェル + NSIS 補助インストール。ショートカットを自動作成し、インストール直後から使えます。Node（npx 同梱）と uv（uvx 同梱）は**Gleam 自身のプロセスツリーの PATH にのみ注入し、システム PATH は一切変更しません**。
- **初回起動の「環境準備中」**：`node -v` / `uv --version` / `git --version` を実際に実行して表示（偽のアニメーションなし、ブロックもしない）。この画面もシステムの地域に追従します。
- **コンテキストウィンドウと自動圧縮**：入力欄に使用率を表示し、90%（タスク終了後）/ 96%（タスク実行中）で次ターンの持ち越しを自動的に絞ります。
- **タスク分離（worktree）**：各タスクは自分のコピー先で実行され、変更はタスクブランチに残ります（黙って本流へマージされることはありません）。
- **Git ツール**：`git.branch` / `git.commit` / `git.push` が安全ゲートを通ります（push は毎回承認が必要）。
- **アプリメニューバー**：ファイル / 編集 / 表示 / ヘルプ。ホバー切替とキーボード操作に対応し、デスクトップでは preload IPC 経由で実際のウィンドウ操作に接続。ブラウザでできない項目は非表示になります。
- **ターミナルパネル**（Ctrl+J）と**右サイドバー**（Ctrl+Shift+B）：ワークスペースのファイル、内蔵ブラウザ、ターミナル入口。
- **フィードバック**（Ctrl+Alt+F）：スクリーンショット添付可。まず本機に保存し、リモートを設定したときだけ送信します。
- **編集可能なショートカット**：検索・録音・競合検出・既定値への復元。
- **設定 v2**：全ページのグループナビゲーション。モデルページで追加 / 編集と実際の接続検証ができ、キーは決して画面に戻しません。SSH ホストは `~/.ssh/config` から Host 名のみ読み取り。アーカイブ済みタスクは削除可能。ネットワークページは接続検査とプロキシの出所を表示します。

---

## セキュリティ：ローカル API トークン

- Web UI はループバックのみ（既定 `127.0.0.1`）で待ち受け、Host / Origin を検証します。
- 起動ごとに API トークンを生成し `~/.gleam/webui.token`（0600）へ書き込みます。すべての `/api/*` は `X-Gleam-Token` ヘッダーが必要で、アプリ内 UI は自動付与します。
- スクリプト / CI（例：フック `POST /api/hooks/<名前>`）からは次のように読みます：
  `curl -H "X-Gleam-Token: $(cat ~/.gleam/webui.token)" -X POST http://127.0.0.1:8787/api/hooks/daily-report`
- 固定トークンにしたい場合は環境変数 `GLEAM_WEBUI_TOKEN` を設定してください。ポートを LAN やインターネットへ公開しないでください。
- API キーは本機の資格情報ストアに保存し、設定した接続先ホストにのみ送信します。UI は「設定済み / 未設定」しか表示しません。

---

## インストールと実行

### Windows：インストーラを入手（推奨）

[Releases](https://github.com/gleam-ai/Gleam/releases) から `Gleam Setup <バージョン>.exe` をダウンロードし、4 ステップで完了します。**Node や uv などのランタイムを自分で入れる必要はありません**。インストーラの言語はシステムの地域に応じて中国語または英語になります。

コマンドラインのコアだけを使いたい場合は単一バイナリをダウンロードしてください：

```bash
./gleam app                                          # デスクトップウィンドウ
./gleam goal "カレントに hello.txt を作成" --mock-llm  # オフライン体験（API キー不要）
./gleam webui                                        # Web UI のみ
```

### 実モデルの接続

```bash
export GLEAM_API_KEY=your_api_key
./gleam goal "ここにある Markdown を列挙して要約" --mode auto
```

### ソースからビルド

```bash
# コア（単一バイナリ）
go build -trimpath -ldflags="-s -w" -o bin/gleam ./cmd/gleam

# デスクトップインストーラ（Windows；Node 20+ が必要）
cd desktop
npm ci
npm run pack:win      # -> <システム一時ディレクトリ>/gleam-pack/Gleam-Setup.exe
```

コアのマルチプラットフォーム向けクロスビルドは `scripts/build-desktop.sh`、Windows の補助インストールスクリプトは `scripts/install.ps1`。デスクトップシェルの開発者向け説明は [`desktop/README.md`](desktop/README.md) にあります。

---

## ツールと MCP

**組み込みツール 27 種**の例:

| グループ | ツール |
| :--- | :--- |
| ファイル | `file.list` `file.read` `file.write` `file.mkdir` `file.move` `file.delete` `file.search` |
| Shell | `shell.exec` |
| Web | `web.fetch` |
| Git | `git.branch` `git.commit` `git.push` |
| デスクトップ | `desktop.clipboard.read` `desktop.clipboard.write` `desktop.screenshot` `desktop.notify` `desktop.snippets` |
| メモリ | `memory.save` `memory.search` `memory.delete` |
| スケジュール | `schedule.create` `schedule.list` `schedule.delete` |
| スキル | `skill.list` `skill.run` |
| プロンプト | `prompt.run` |
| 返信 | `reply` |

**MCP:** 設定の `mcp:` に外部サーバ（command + args + trust）を宣言。起動時に同一レジストリへ登録されます。アプリ内マーケットからワンクリックで導入もできます（同梱の npx / uvx を使用）。

---

## 設定とデータディレクトリ

| パス | 役割 |
| :--- | :--- |
| `configs/config.yaml` | サンプル / 同梱設定（YAML サブセット、インデント 2 スペース） |
| `~/.gleam/` | 既定の**データディレクトリ**（`--data-dir` で上書き可） |
| `~/.gleam/settings.yaml` | 本機設定オーバーレイ（保存で即時反映） |
| `~/.gleam/memory/` `tasks/` `skills/` | 長期メモリ、タスクアーカイブ、スキル |
| `~/.gleam/worktrees/` | タスクごとのコピー（worktree 分離が有効なとき）とそのメタデータ |
| `~/.gleam/schedules.json` | スケジューラ状態 |
| `~/.gleam/audit.jsonl` | 安全 / 送信監査 |
| `~/.gleam/browser-profile/` | デスクトップ埋め込みブラウザプロファイル |
| `~/.gleam/webui.token` | 起動ごとに生成される Web UI API トークン（0600）。すべての Web UI API リクエストは `X-Gleam-Token` ヘッダーで送る必要があり、アプリ内 UI は自動で付与します。`GLEAM_WEBUI_TOKEN` で事前指定可 |

API キーは YAML に書かず、環境変数 `GLEAM_API_KEY` で渡すことを推奨します。

---
---

## Web UI インターフェース一覧

Web UI の REST エンドポイントはコードに登録されたルートと一対一で対応します。一覧と実装のずれは `scripts/check-api-docs.py` が検出します。すべてのエンドポイントに `X-Gleam-Token` が必要です（前の節を参照）。

| エンドポイント | 説明 |
| :--- | :--- |
| `GET /api/info` | アプリのバージョン・プラットフォーム・実行形態。UI はここから版を取る（リテラルを写さない） |
| `POST /api/heartbeat` · `POST /api/show-window` | 本機の生存ハートビート / ウィンドウを前面へ |
| `GET /api/settings` · `POST /api/settings` | 設定オーバーレイの読み書き（保存で即時反映） |
| `GET /api/onboarding` · `POST /api/onboarding` | 初回オンボーディングの状態と送信 |
| `GET /api/providers` | ベンダープリセット（公式入口 × プラン形態） |
| `POST /api/llm/models` · `POST /api/llm/test` | ベンダーのモデル一覧取得 / フォームの現在値で最小の疎通テスト |
| `GET /api/account` · `POST /api/account/configure` · `POST /api/account/signup` · `POST /api/account/signin` · `POST /api/account/signout` · `POST /api/account/oauth` | クラウドアカウント：状態、プロジェクト接続、登録 / ログイン / ログアウト / OAuth |
| `GET /api/local-data` | 本機データディレクトリの規模と構成 |
| `GET /api/network` · `GET /api/connections` | 接続チェック / 接続と送信の常駐境界台帳 |
| `GET /api/security/audit` | 安全ゲートの記録（遮断 / 通過 / 審査モデルによる追加遮断） |
| `GET /api/workspace` · `POST /api/workspace` · `POST /api/workspace/clear` | ワークスペースの取得 / 切替、開いている一覧とクリア |
| `GET /api/fs` | ワークスペース内のディレクトリ閲覧（ファイル選択用） |
| `GET /api/goals` · `POST /api/goals` · `GET /api/goals/{id}` · `POST /api/goals/{id}/cancel` · `GET /api/goals/{id}/diff` · `POST /api/goals/{id}/revert` · `DELETE /api/goals/{id}` | 目標 / タスク：投入、照会、中止、差分と巻き戻し、アーカイブ削除 |
| `GET /api/approvals` · `POST /api/approvals/{id}` | 承認待ちの操作とその裁定 |
| `GET /api/events` | SSE イベントストリーム |
| `GET /api/conversations` · `POST /api/conversations` · `GET /api/conversations/{id}` · `PATCH /api/conversations/{id}` · `DELETE /api/conversations/{id}` · `POST /api/conversations/{id}/activate` | 会話：一覧、作成、取得、名前変更、削除、切替 |
| `POST /api/conversation/reset` | 現在の会話コンテキストを消去（長期メモリは保持） |
| `GET /api/context` · `POST /api/context/compress` · `POST /api/context/clear` | コンテキストウィンドウの読み / 即時圧縮 / 要約と圧縮待ち履歴の消去 |
| `GET /api/memory` · `POST /api/memory` · `DELETE /api/memory/{id}` | 長期メモリ：検索、書き込み、削除 |
| `GET /api/spaces` · `POST /api/spaces` · `PATCH /api/spaces/{id}` · `DELETE /api/spaces/{id}` · `POST /api/spaces/{id}/activate` | スペース：一覧、作成、名前変更、削除、切替 |
| `GET /api/schedules` · `POST /api/schedules` · `DELETE /api/schedules/{name}` · `POST /api/schedules/{name}/enabled` · `POST /api/schedules/{name}/notify` | スケジュール：一覧、作成、削除、有効化・無効化、単発通知 |
| `POST /api/hooks/{name}` | 外部スクリプト / CI から叩く HTTP フック（各スケジュールに自動で付く） |
| `GET /api/skills` · `POST /api/skills` · `POST /api/skills/{name}/run` · `POST /api/skills/{name}/enabled` · `DELETE /api/skills/{name}` | スキル：一覧、固化、実行、有効化・無効化、削除 |
| `GET /api/market/skills` · `POST /api/market/skills/install` · `GET /api/market/mcp` · `POST /api/market/mcp/install` · `GET /api/market/runtimes` · `GET /api/market/sources` | マーケット：スキルテンプレートと MCP カタログ、ワンクリック導入、利用可能なランタイムとカタログ元 |
| `GET /api/mcp` · `POST /api/mcp` · `DELETE /api/mcp/{name}` · `POST /api/mcp/{name}/enabled` · `POST /api/mcp/{name}/reconnect` | MCP サーバ：一覧、追加、削除、有効化・無効化、再接続 |
| `GET /api/tools` · `POST /api/tools/permission` · `POST /api/tools/call` | エンジン登録済みのツール、権限档位、直接呼び出し |
| `GET /api/growth` · `GET /api/growth/recent` · `GET /api/roles` | 成長統計（レベル・イベント数）/ 最近のイベント / 利用可能なペルソナ |
| `GET /api/readiness` | レディネス検査：環境と能力のセルフチェック |
| `GET /api/go-status` · `POST /api/go-status` · `POST /api/go-status/install` | Go ツールチェーンの検出とインストール |
| `GET /api/ssh/hosts` | ~/.ssh/config からホスト別名を読む（Host 名のみ） |
| `GET /api/geo` · `POST /api/geo/analyze` · `DELETE /api/geo/history` | GEO：状態、コンテンツの分析、分析履歴の消去 |
| `GET /api/feedback` · `POST /api/feedback` · `GET /api/feedback/attachment` · `GET /api/feedback/context` · `POST /api/feedback/{id}/resend` · `DELETE /api/feedback/{id}` | フィードバック：一覧、送信、添付とコンテキストのスナップショット、再送、削除 |
| `GET /api/import/scan` · `POST /api/import/apply` | 本機の他 AI ツールからメモリとルールを取り込む：スキャンと書き込み |
| `GET /api/cues` · `POST /api/cues/{id}/adopt` · `POST /api/cues/{id}/dismiss` · `POST /api/cues/unsuppress` | 候補目標：一覧、採用、無視、無視の取り消し |
| `GET /api/worktrees` · `DELETE /api/worktrees/{id}` | Gleam が管理するタスクコピー：一覧と削除 |
| `GET /api/update/check` · `POST /api/update/apply` | 更新の確認 / ダウンロードして置換（どちらもあなたが押す） |

---
## ローカルファーストの正直な説明

**ローカルファースト ≠ オフライン LLM。**

- 会話・記憶・タスク状態・スキル・監査は既定で**本機**に残ります。
- **Mock**（`--mock-llm` / `provider: mock`）はクラウドモデルなしでエージェントフローを体験できます。
- **実モデル**は設定した **API** にプロンプトを送ります。送信ログはホスト名とバイト数のみで、本文は記録しません。
- 任意のクラウドログイン / フィードバック経路も同様に送信し、監査されます。

既知の制限と意図的にやらないこと: [docs/known-limits.md](docs/known-limits.md)。

---

## ドキュメント

| 文書 | 内容 |
| :--- | :--- |
| [既知の制限とやらないこと](docs/known-limits.md) | **公開の単一ソース**: 制限 / 却下案 / 意図的非目標 |
| [デスクトップシェル](desktop/README.md) | インストールフロー、ランタイムの可視範囲、開発・検証コマンド |
| [コントリビューション](CONTRIBUTING.md) | ローカル開発、コミット規約、組織ガイド入口 |
| [セキュリティ](SECURITY.md) | 脆弱性報告とローカルファーストの境界 |
| [CHANGELOG](CHANGELOG.md) | 変更履歴 |

---

## コントリビューションとセキュリティ

- PR の前に [CONTRIBUTING.md](CONTRIBUTING.md) を読んでください（Conventional Commits、署名コミット、DCO 風 sign-off）。
- 脆弱性は [SECURITY.md](SECURITY.md) に従って報告してください。秘密経路が必要な場合は公開 Issue を使わないでください。
- 組織ポリシー: [gleam-ai/.github](https://github.com/gleam-ai/.github)。

---

## ライセンス

[MIT License](LICENSE)

> Gleam は「より人間らしい AI」ではなく、「より信頼できる同僚」を目指します。
