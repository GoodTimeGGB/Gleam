import { BrowserWindow, nativeTheme } from 'electron';
import { join } from 'node:path';

// Must match --topbar-h in internal/webui/static/style.css: the window controls overlay sits on the topbar.
const TOPBAR_HEIGHT = 44;

// The topbar paddings in style.css are not aware of the macOS traffic lights. Rather than touch the
// shared UI, the shell injects this one rule on macOS only (decision D4). Unverified on macOS.
const MAC_TRAFFIC_LIGHT_CSS = '.topbar { padding-left: 78px !important; }';

export function createMainWindow(opts: { width?: number; height?: number } = {}): BrowserWindow {
  const isMac = process.platform === 'darwin';
  const win = new BrowserWindow({
    width: opts.width ?? 1280,
    height: opts.height ?? 840,
    minWidth: 720,
    minHeight: 480,
    show: false,
    title: 'Gleam',
    backgroundColor: nativeTheme.shouldUseDarkColors ? '#0f1412' : '#f6f7f5',
    // No menu bar on Windows/Linux (decision D4); the app menu on macOS is the default one for now.
    autoHideMenuBar: true,
    titleBarStyle: 'hidden',
    ...(isMac
      ? { trafficLightPosition: { x: 14, y: 14 } }
      : { titleBarOverlay: { height: TOPBAR_HEIGHT, color: '#00000000', symbolColor: '#7a7f7c' } }),
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
  if (!isMac) win.setMenuBarVisibility(false);
  if (isMac) {
    win.webContents.on('did-finish-load', () => {
      void win.webContents.insertCSS(MAC_TRAFFIC_LIGHT_CSS);
    });
  }
  win.once('ready-to-show', () => win.show());
  return win;
}
