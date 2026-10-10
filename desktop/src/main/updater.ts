// 自动更新（仅打包后的构建）。渲染进程不直接碰 electron-updater：它只订阅一条状态流，
// 并且在"要装"的时候说一声。下载是后台的事（autoDownload），安装永远是用户的决定——
// 除了退出时顺手装上已下好并验证过的那份（autoInstallOnAppQuit）。
//
// 更新从哪来不在这里决定：electron-builder 会把 electron-builder.yml 里的 `publish` 块
// 写进 resources/app-update.yml，打包时定下来。
import { app, BrowserWindow, ipcMain, type IpcMainInvokeEvent } from 'electron';
import { autoUpdater, type ProgressInfo, type UpdateDownloadedEvent, type UpdateInfo } from 'electron-updater';
import { APP_HOST, APP_SCHEME } from './protocol';

/** 推给界面的形状。`idle` 只是初始快照（按钮保持隐藏）。 */
export type UpdateStatus =
  | { kind: 'idle' }
  | { kind: 'checking' }
  | { kind: 'available'; version: string }
  | { kind: 'downloading'; version: string; percent: number }
  | { kind: 'downloaded'; version: string }
  | { kind: 'not-available' }
  | { kind: 'error'; version: string; message: string };

// 检查频率：启动一次，之后每 6 小时一次。桌面应用常开，太密是白耗流量，太疏则
// 用户永远在"上一版"。
const CHECK_EVERY_MS = 6 * 60 * 60 * 1000;
const log = (message?: unknown) => console.log('[update]', message);

let latest: UpdateStatus = { kind: 'idle' };
let version = '';

// 与 security.ts 同一个口径：只认自己的顶层 app://gleam 框架。
function fromAppFrame(e: IpcMainInvokeEvent): boolean {
  const frame = e.senderFrame;
  if (!frame || frame.parent !== null) return false;
  try {
    const u = new URL(frame.url);
    return u.protocol === `${APP_SCHEME}:` && u.host === APP_HOST;
  } catch {
    return false;
  }
}

export function installUpdater(): void {
  // 更新要能原地替换自己。Windows（NSIS）可以；纯 Linux 的 `dir` 构建不行——
  // 只有 AppImage 会自己报上 $APPIMAGE。
  if (process.platform === 'linux' && !process.env.APPIMAGE) {
    log('skipped: this Linux build cannot replace itself in place');
    return;
  }

  const push = (status: UpdateStatus) => {
    if (status.kind !== 'idle') latest = status;
    for (const win of BrowserWindow.getAllWindows()) {
      if (!win.isDestroyed()) win.webContents.send('desktop:update-status', status);
    }
  };

  autoUpdater.logger = { info: log, warn: log, error: log };
  autoUpdater.autoDownload = true;
  autoUpdater.autoInstallOnAppQuit = true;

  autoUpdater.on('checking-for-update', () => push({ kind: 'checking' }));
  autoUpdater.on('update-available', (info: UpdateInfo) => {
    version = info.version;
    push({ kind: 'available', version });
  });
  autoUpdater.on('download-progress', (p: ProgressInfo) => {
    push({ kind: 'downloading', version, percent: Math.round(p.percent) });
  });
  autoUpdater.on('update-downloaded', (info: UpdateDownloadedEvent) => {
    version = info.version;
    push({ kind: 'downloaded', version });
  });
  autoUpdater.on('update-not-available', () => {
    version = '';
    push({ kind: 'not-available' });
  });
  autoUpdater.on('error', (err: Error) => push({ kind: 'error', version, message: err.message }));

  const check = async () => {
    try {
      await autoUpdater.checkForUpdates();
    } catch (err) {
      // 'error' 事件已经推过一次了，这里只是别让 rejection 变成 unhandled。
      log(`check failed: ${String(err)}`);
    }
  };

  ipcMain.handle('desktop:update:check', (e) => {
    if (!fromAppFrame(e)) throw new Error('forbidden');
    void check();
    return { ok: true };
  });
  ipcMain.handle('desktop:update:status', (e) => {
    if (!fromAppFrame(e)) throw new Error('forbidden');
    return latest;
  });
  ipcMain.handle('desktop:update:restart', (e) => {
    if (!fromAppFrame(e)) throw new Error('forbidden');
    if (latest.kind !== 'downloaded') return { ok: false };
    log('restarting to install the downloaded update');
    // 等这次 IPC 回完再退出，否则界面等不到回执就被带走了。
    setImmediate(() => autoUpdater.quitAndInstall());
    return { ok: true };
  });

  // 更新是长驻的，界面是随时可以换的：新窗口 / 重新加载后补一次当前状态，
  // 否则刷新之后"重启"按钮就丢在那次刷新里。
  app.on('browser-window-created', (_e, win) => {
    win.webContents.on('did-finish-load', () => {
      if (!win.isDestroyed()) win.webContents.send('desktop:update-status', latest);
    });
  });

  void check();
  const timer = setInterval(() => void check(), CHECK_EVERY_MS);
  timer.unref();
  app.on('before-quit', () => clearInterval(timer));
}
