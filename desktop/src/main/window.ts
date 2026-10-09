import { BrowserWindow, nativeTheme } from 'electron';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { forwardWindowState } from './security';

// 应用图标。没给 BrowserWindow 传 icon 时，Windows/Linux 上窗口与任务栏会用它自己的默认图标——
// 界面上任何地方都改不到，只有这里能改。
//   dev：仓库里的 assets/icons（__dirname 是 desktop/dist/main，往上三层是仓库根）
//   打包：electron-builder 按 electron-builder.yml 的 extraResources 放到 resources/icon.png
// macOS 忽略窗口 icon（用 .app 的 bundle 图标），由 mac.icon 指向 assets/gleam.icns。
function resolveAppIcon(): string | undefined {
  if (process.platform === 'darwin') return undefined;
  const candidates = [
    join(process.resourcesPath ?? '', 'icon.png'),
    join(__dirname, '..', '..', '..', 'assets', 'icons', 'gleam-256.png'),
  ];
  return candidates.find((p) => p && existsSync(p));
}

// Frameless on Windows / Linux: the web UI draws its own menu bar (文件 / 编辑 / 视图 / 帮助) and the
// minimise / maximise / close buttons in the 44px top bar (--topbar-h in style.css), and marks the
// rest of the top bar as a drag region. macOS keeps the system traffic lights (hidden title bar);
// the UI pads the top bar for them via html[data-platform="darwin"] (unverified on macOS).
export function createMainWindow(opts: { width?: number; height?: number } = {}): BrowserWindow {
  const isMac = process.platform === 'darwin';
  const icon = resolveAppIcon();
  console.log(`[desktop] window icon: ${icon ?? '(built-in)'}`);
  const win = new BrowserWindow({
    width: opts.width ?? 1280,
    height: opts.height ?? 840,
    ...(icon ? { icon } : {}),
    minWidth: 720,
    minHeight: 480,
    show: false,
    title: 'Gleam',
    backgroundColor: nativeTheme.shouldUseDarkColors ? '#161615' : '#f5f5f2',
    autoHideMenuBar: true,
    ...(isMac ? { titleBarStyle: 'hidden' as const, trafficLightPosition: { x: 14, y: 14 } } : { frame: false }),
    webPreferences: {
      preload: join(__dirname, '..', 'preload', 'index.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
      nodeIntegrationInSubFrames: false,
      webviewTag: false,
      spellcheck: false,
      // devTools stay available in the spike; Phase 1 gates them on !app.isPackaged.
    },
  });
  // No native menu: its accelerators would fight the in-app keymap (Ctrl+N, Ctrl+T, Ctrl+J …).
  win.setMenu(null);
  forwardWindowState(win);
  win.once('ready-to-show', () => win.show());
  return win;
}
