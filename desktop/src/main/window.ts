import { BrowserWindow, nativeTheme } from 'electron';
import { join } from 'node:path';
import { forwardWindowState } from './security';

// Frameless on Windows / Linux: the web UI draws its own menu bar (文件 / 编辑 / 视图 / 帮助) and the
// minimise / maximise / close buttons in the 44px top bar (--topbar-h in style.css), and marks the
// rest of the top bar as a drag region. macOS keeps the system traffic lights (hidden title bar);
// the UI pads the top bar for them via html[data-platform="darwin"] (unverified on macOS).
export function createMainWindow(opts: { width?: number; height?: number } = {}): BrowserWindow {
  const isMac = process.platform === 'darwin';
  const win = new BrowserWindow({
    width: opts.width ?? 1280,
    height: opts.height ?? 840,
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
