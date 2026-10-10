# Gleam 桌面壳（Electron）

> 状态：**Windows 可安装**。安装包是 NSIS 辅助安装（可选"所有人/仅我"、可改目录、完成页可勾选运行、
> 自动建桌面快捷方式），装完即可用——内置了 Node 与 uv，用户不需要自己装环境。
> 安装界面语言**按电脑区域自动选**：中国区域中文，其它区域英文。
> **未做代码签名**（无付费账号），所以 Windows 上 SmartScreen 会拦一次，见 `docs/known-limits.md`。
> Electron 固定为 `41.10.6`。

现有 `gleam app`（Edge `--app` 窗口）和 `gleam webui` 不受影响，这个目录是并行的一条路。

## 安装与首启

```
用户双击安装包 → 选"所有人/仅我" → 选安装目录 → 安装（建桌面与开始菜单快捷方式）
   → 勾选"运行 Gleam" → 「环境准备中」→ 主界面
```

**安装界面语言**（`electron-builder.yml` 的 `installerLanguages`）取系统区域：NSIS 在没有语言选择
对话框时用系统语言，认不出就用**列表里第一条**兜底，所以 `en_US` 必须排在前、`zh_CN` 排在后——
中文系统命中 `zh_CN` 走中文，其余区域（含拿不到区域的情况）一律英文。没有开
`displayLanguageSelector`：多一个选语言的弹窗，就多一步要用户做决定的选择。

「环境准备中」那屏（`src/main/splash.ts`）做的是**真实探测**：跑一遍 `node -v` / `uv --version` /
`git --version`，逐项显示结果与版本，通常在几百毫秒内结束。它自己的文案也跟着系统区域走
（`src/main/locale.ts`），与安装器同一口径。两条规矩：

- **不排假动画**：内置运行时是本地文件，探测多快就多快；
- **不拦路**：内置运行时跑不起来时把原因写在屏上、倒计时几秒后自动放行，Gleam 的核心功能
  （对话、任务、文件）本来就不依赖它们。

内置运行时的落点与可见范围（`src/main/runtimes.ts`）：

| | |
|---|---|
| 打包位置 | `resources/runtimes/{node,uv}`（由 `scripts/fetch-runtimes.mjs` 在**构建期**下载并校验官方 SHA256） |
| 运行期可见范围 | **只有 Gleam 自己的进程树**：Electron 主进程 → Go sidecar → sidecar 起的 `shell.exec` / MCP 子进程 |
| 系统 PATH | **不动**。理由与 Go 工具链一致（`internal/webui/goinstall.go`）：改用户机器的全局 PATH 是替他改系统状态 |
| 不含 | Git（体积几百 MB 且系统里通常已有；缺失时用到它的功能会提示，不自动装） |

## 结构

```
desktop/
  package.json            electron 41.10.6（精确版本）、typescript、electron-builder
  tsconfig.json
  electron-builder.yml    win: nsis（辅助安装、快捷方式、内置运行时随包、语言按区域）
  scripts/build-sidecar.mjs    go build ./cmd/gleam -> .sidecar/<platform>-<arch>/gleam[.exe]
  scripts/fetch-runtimes.mjs   下载并校验 Node / uv -> .runtimes/<platform>-<arch>/（含许可证）
  src/main/index.ts       入口：单实例锁、环境准备屏、生命周期、退出时关闭 sidecar
  src/main/locale.ts      安装器 / 首启屏的语言判定（系统区域：zh-* 中文，其余英文）
  src/main/sidecar.ts     启动 sidecar、等就绪行、探活、优雅关闭 / 超时强杀
  src/main/runtimes.ts    内置运行时的落点解析、版本探测、PATH 拼接
  src/main/splash.ts      首启「环境准备中」窗口（真探测、不依赖 IPC、中英双语）
  src/main/protocol.ts    app://gleam/ -> http://127.0.0.1:<随机端口>/ 代理，注入口令和 CSP
  src/main/security.ts    导航 / 新窗口 / webview 拦截、权限默认拒绝、IPC 白名单与各通道的处理
  src/main/window.ts      BrowserWindow（Windows/Linux 无边框，macOS 隐藏标题栏 + 红绿灯；不挂原生菜单）
  src/preload/index.ts    沙箱 preload：暴露 window.gleamDesktop（见下方「应用内菜单与窗口控件」）
  spike/                  Xvfb 下的验证脚本（Python + Playwright 走 CDP）
```

Go 侧只加了一个隐藏子命令 `gleam desktop-sidecar`（`cmd/gleam/sidecar_cmd.go`）：

- 默认监听 `127.0.0.1:0`，只允许回环地址；
- 口令由父进程经 `GLEAM_WEBUI_TOKEN` 预置，读完后从自身环境里抹掉；
- 就绪后在 stdout 打一行 `GLEAM_READY {"addr":"127.0.0.1:PORT","pid":N,"version":"x.y.z"}`，其余日志都走 stderr；
- stdin 关闭（包括 Electron 被强杀）或收到一行 `{"cmd":"shutdown"}` 即优雅退出：先断开 SSE 长连接，再 `Shutdown`。

界面本身的中英切换不在这个目录：词表与翻译器在 `internal/webui/static/i18n.js`，桌面壳只负责把
系统区域决定的语言用在**进主界面之前**的那几屏上。

## 应用内菜单与窗口控件

界面顶栏自己画「文件 / 编辑 / 视图 / 帮助」菜单和最小化 / 最大化 / 关闭按钮（`internal/webui/static/chrome.js`），
桌面壳只负责把这些动作落到真实的 `webContents` / `BrowserWindow` 上：

- 窗口：Windows / Linux `frame: false`，macOS `titleBarStyle: 'hidden'` + `trafficLightPosition`；`win.setMenu(null)`，不再用 `titleBarOverlay`。
- preload 暴露 `window.gleamDesktop`：`isDesktop`、`platform`、`info()`、`edit(action)`、`view(action)`、`window(action)`、
  `windowState()`、`onWindowState(cb)`、`openExternal(url)`、`capture()`、`newWindow()`。
- IPC 通道固定白名单：`desktop:info / edit / view / window / windowState / openExternal / capture / newWindow`，事件 `desktop:window-state`。
  每个处理函数先校验发送方是 `app://gleam` 的主框架，再按固定的动作表分派（`undo/redo/cut/copy/paste/selectAll`、
  `reload/forceReload/toggleDevTools/zoomIn/zoomOut/resetZoom/toggleFullScreen`、`minimize/toggleMaximize/close`），表外的值直接拒绝。
- 缩放步进 0.5，夹在 −3…4；`capture()` 截当前页面并缩到 ≤1600px 宽，供反馈对话框附图。
- `openExternal(url)` 只接受 http(s)，经 `shell.openExternal` 交给系统浏览器——内置预览「在浏览器打开」不再走 `window.open`，
  所以不会再被新窗口拦截误报成「弹窗被拦截」。
- 浏览器（非桌面壳）下同一套菜单会隐藏「关闭窗口 / 强制刷新 / 缩放」，编辑类动作退回 `execCommand` / Clipboard API。

已在 Xvfb 上验证：菜单四组、撤销 / 剪切 / 粘贴、缩放 1→1.095→1、反馈自动截图、关于（版本 / Electron / Chromium / 平台）、
外部打开、Ctrl+J / Ctrl+Shift+B。Xvfb 没有窗口管理器，最大化状态切换在那里观察不到，需要实机验证。

## 开发

需要 Go（版本见根目录 go.mod）和 Node 20+。

```bash
cd desktop
npm ci
npm run dev            # 构建 sidecar + 编译 TS + 启动 Electron
npm run fetch:runtimes # 下载并校验内置 Node / uv（打包前需要；重复跑走本地缓存）
npm run pack:win       # Windows 安装包：<系统临时目录>/gleam-pack/Gleam-Setup.exe
npm run pack:linux     # 解包目录：<系统临时目录>/gleam-pack/linux-unpacked/
```

安装包的**产物名不带版本号**（`Gleam-Setup.exe`，见 `electron-builder.yml` 的 `artifactName`）。
原因是官网的下载按钮指向 GitHub 的 `releases/latest/download/Gleam-Setup.exe`——版本号一旦进
文件名，那条链接每次发版都得改，而"忘了改"正是下载页对外报旧版本的原因。Release 上其余资产的
无日期名（`gleam-windows-amd64.exe`、`gleam-desktop-windows-amd64.exe`、`gleam-darwin-amd64`、
`gleam-darwin-arm64`、`gleam-linux-amd64`）由 `scripts/package.sh` 产出，上传时**不需要改名**。

开发时不想看首启那屏：`GLEAM_NO_SPLASH=1 npm start`。

开发 / 测试用环境变量：

| 变量 | 作用 |
|---|---|
| `GLEAM_SIDECAR_ARGS` | 追加给 sidecar 的参数，例如 `--mock-llm --data-dir /tmp/x --workspace /tmp/ws` |
| `GLEAM_SIDECAR_BIN` | 指定 sidecar 可执行文件 |
| `GLEAM_DESKTOP_USER_DATA` | Electron 的 userData 目录（localStorage 偏好、单实例锁都按它隔离） |
| `GLEAM_DESKTOP_WIDTH` / `GLEAM_DESKTOP_HEIGHT` | 初始窗口尺寸 |
| `GLEAM_NO_SPLASH` | 跳过「环境准备中」那屏（开发/自动化用） |
| `NODE_VERSION` / `UV_VERSION` | 钉住 `fetch:runtimes` 取的版本（默认取最新 LTS / 最新 release） |

**改完 `internal/webui/static/` 下的任何东西都要重编 sidecar**：前端是用 `//go:embed` 打进
sidecar 可执行文件的，只重启 Electron 不会生效（`npm run dev` / `npm run pack:win` 都会自动重编）。

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

## 待办

- **macOS 实机验证**（没做过）：无边框窗口的拖拽区与自绘窗口按钮、最大化状态同步、macOS 红绿灯位置、
  杀毒软件对 sidecar 的拦截。macOS 目前只到 `dir` 解包，没有 dmg。
- **保持 Electron 在受支持的大版本上**（现在是 `41.10.6`），升级后重跑上面的验证。
- **签名 / 公证**（Authenticode、Apple notarization）、自动更新、Electron fuses。
- 关窗口 → 托盘常驻（现在关窗口即退出）；sidecar 崩溃后带退避的自动重启（现在弹框后退出）。
- Windows 上用 Job Object 兜住 sidecar 的整个进程树；POSIX 上 Electron 被 kill -9 时 sidecar 自己会退，
  但它派生的工具子进程目前不保证一起退。
- CSP 的内联脚本哈希改为构建期计算；Google Fonts 改为本地打包。
- 生产包关闭 DevTools / `--remote-debugging-port`；收紧开发用环境变量。
