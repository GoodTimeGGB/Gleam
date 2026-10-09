# Gleam 桌面壳（Electron）· Phase 0 spike

> 状态：**spike / 草稿**。只在 Linux（Xvfb）上验证过；Windows、macOS 未验证。
> Electron 固定为 `37.7.0`，仅用于这次 spike；正式发布前要升到当前受支持的大版本（44.x），见下方「Phase 1 待办」。

现有 `gleam app`（Edge `--app` 窗口）和 `gleam webui` 不受影响，这个目录是并行的一条路。

## 结构

```
desktop/
  package.json            electron 37.7.0（精确版本）、typescript、electron-builder
  tsconfig.json
  electron-builder.yml    最小配置：只构建过 linux "dir"（解包目录）
  scripts/build-sidecar.mjs   go build ./cmd/gleam -> .sidecar/<platform>-<arch>/gleam[.exe]
  src/main/index.ts       入口：单实例锁、生命周期、退出时关闭 sidecar
  src/main/sidecar.ts     启动 sidecar、等就绪行、探活、优雅关闭 / 超时强杀
  src/main/protocol.ts    app://gleam/ -> http://127.0.0.1:<随机端口>/ 代理，注入口令和 CSP
  src/main/security.ts    导航 / 新窗口 / webview 拦截、权限默认拒绝、IPC 白名单
  src/main/window.ts      BrowserWindow（隐藏标题栏 + 窗口控件覆盖层，高度 44 = --topbar-h）
  src/preload/index.ts    沙箱 preload：只暴露 window.gleamDesktop.{isDesktop, info}
  spike/                  Xvfb 下的验证脚本（Python + Playwright 走 CDP）
```

Go 侧只加了一个隐藏子命令 `gleam desktop-sidecar`（`cmd/gleam/sidecar_cmd.go`）：

- 默认监听 `127.0.0.1:0`，只允许回环地址；
- 口令由父进程经 `GLEAM_WEBUI_TOKEN` 预置，读完后从自身环境里抹掉；
- 就绪后在 stdout 打一行 `GLEAM_READY {"addr":"127.0.0.1:PORT","pid":N,"version":"x.y.z"}`，其余日志都走 stderr；
- stdin 关闭（包括 Electron 被强杀）或收到一行 `{"cmd":"shutdown"}` 即优雅退出：先断开 SSE 长连接，再 `Shutdown`。

## 开发

需要 Go（版本见根目录 go.mod）和 Node 20+。

```bash
cd desktop
npm ci
npm run dev          # 构建 sidecar + 编译 TS + 启动 Electron
npm run pack:linux   # 解包目录：release/linux-unpacked/
```

开发 / 测试用环境变量（Phase 1 再决定是否保留到正式包里）：

| 变量 | 作用 |
|---|---|
| `GLEAM_SIDECAR_ARGS` | 追加给 sidecar 的参数，例如 `--mock-llm --data-dir /tmp/x --workspace /tmp/ws` |
| `GLEAM_SIDECAR_BIN` | 指定 sidecar 可执行文件 |
| `GLEAM_DESKTOP_USER_DATA` | Electron 的 userData 目录（localStorage 偏好、单实例锁都按它隔离） |
| `GLEAM_DESKTOP_WIDTH` / `GLEAM_DESKTOP_HEIGHT` | 初始窗口尺寸 |

## 为什么用 app:// 代理而不是直接加载回环地址

- 端口每次随机，`app://gleam` 这个源却固定，localStorage 里的界面偏好得以跨启动保留；
- 口令只在主进程里，由代理逐个请求注入；渲染进程直接访问回环端口会被 CSP 挡住；
- 渲染进程发出的 `Origin: app://gleam` / `Sec-Fetch-*` 在代理处校验后剥掉，不需要放宽 Go 侧的 Host / Origin 校验；
- 代理把上游响应体重新包了一层：渲染进程取消请求（`EventSource.close()`、刷新页面）时会顺着 cancel 掉上游的 `net.fetch`。
  直接返回上游 Response 的话，关掉的 SSE 流不会释放，Chromium 每主机 6 条连接的池在第 2 轮就被占满（spike 中实测）。

## 验证（Linux / Xvfb）

```bash
Xvfb :77 -screen 0 1600x1000x24 &
npm run build
SPIKE_DISPLAY=:77 SPIKE_OUT=/tmp/gspike/out python3 spike/verify.py     # 渲染、SSE、主题持久化、单实例、kill -9、像素对比
SPIKE_DISPLAY=:77 SPIKE_OUT=/tmp/gspike/out python3 spike/sse-leak.py   # SSE 流在代理里能否释放
```

`spike/stubborn-sidecar.py` 是一个不理关闭指令也不理 SIGTERM 的替身，用来验证「超时强杀」路径：
`GLEAM_SIDECAR_BIN=spike/stubborn-sidecar.py npm start`，然后给 Electron 主进程发 SIGTERM。

## Phase 1 待办

- **Windows / macOS 实机验证**（都没做过）：窗口控件覆盖层、macOS 红绿灯位置（目前是平台判断后注入一条 CSS）、
  `taskkill /T` 强杀路径、杀毒软件对 sidecar 的拦截。
- **升级 Electron 到 44.x**（37 已出支持期），并重跑上面的验证。
- **签名 / 公证**（Authenticode、Apple notarization）、安装包（NSIS / dmg）、自动更新、Electron fuses。
- 关窗口 → 托盘常驻（spike 里关窗口即退出）；sidecar 崩溃后带退避的自动重启（spike 里弹框后退出）。
- Windows 上用 Job Object 兜住 sidecar 的整个进程树；POSIX 上 Electron 被 kill -9 时 sidecar 自己会退，
  但它派生的工具子进程目前不保证一起退。
- CSP 的内联脚本哈希改为构建期计算；Google Fonts 改为本地打包（决定 D5 允许 spike 先保留）。
- 生产包关闭 DevTools / `--remote-debugging-port`；收紧开发用环境变量。
