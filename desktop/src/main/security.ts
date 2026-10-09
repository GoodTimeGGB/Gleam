// Security baseline for every webContents and the default session.
import { app, ipcMain, shell, type IpcMainInvokeEvent, type Session, type WebContents } from 'electron';
import { APP_HOST, APP_ORIGIN, APP_SCHEME } from './protocol';

// Note: Node's URL reports origin "null" for schemes it does not know (app:), so compare scheme + host.
function isAppURL(raw: string): boolean {
  try {
    const u = new URL(raw);
    return u.protocol === `${APP_SCHEME}:` && u.host === APP_HOST;
  } catch {
    return false;
  }
}

function isExternalHTTP(raw: string): boolean {
  try {
    const u = new URL(raw);
    return u.protocol === 'http:' || u.protocol === 'https:';
  } catch {
    return false;
  }
}

/** Navigation / window-open / webview guards. Installed from app 'web-contents-created'. */
export function hardenContents(contents: WebContents): void {
  // Top-level navigation may only stay inside app://gleam/.
  const guardNav = (e: Electron.Event, url: string) => {
    if (!isAppURL(url)) {
      e.preventDefault();
      console.warn(`[security] blocked navigation to ${url}`);
    }
  };
  contents.on('will-navigate', guardNav);
  contents.on('will-redirect', guardNav);

  // No new windows. http(s) links go to the system browser; everything else is dropped.
  contents.setWindowOpenHandler(({ url }) => {
    if (isExternalHTTP(url)) void shell.openExternal(url);
    else console.warn(`[security] blocked window.open(${url})`);
    return { action: 'deny' };
  });

  contents.on('will-attach-webview', (e) => {
    e.preventDefault();
    console.warn('[security] blocked <webview>');
  });
}

/** Deny-by-default permissions for the default session. */
export function hardenSession(ses: Session): void {
  ses.setPermissionRequestHandler((_wc, permission, callback, details) => {
    // Clipboard writes from our own UI (copy buttons) are the one thing it legitimately asks for.
    const ok = permission === 'clipboard-sanitized-write' && isAppURL(details.requestingUrl);
    if (!ok) console.warn(`[security] denied permission ${permission} for ${details.requestingUrl}`);
    callback(ok);
  });
  ses.setPermissionCheckHandler((_wc, permission, requestingOrigin) => {
    return permission === 'clipboard-sanitized-write' && requestingOrigin === APP_ORIGIN;
  });
  // Nothing in the app needs downloads in Phase 0.
  ses.on('will-download', (e) => e.preventDefault());
}

// ---- IPC allowlist -------------------------------------------------------------------------------
// Every channel is listed here, and every handler checks that the call comes from our own top frame.
// The preload exposes exactly these channels and nothing else.

export const IPC_CHANNELS = ['desktop:info'] as const;

function fromAppFrame(e: IpcMainInvokeEvent): boolean {
  const frame = e.senderFrame;
  return !!frame && frame.parent === null && isAppURL(frame.url);
}

export function installIpc(info: () => Record<string, unknown>): void {
  ipcMain.handle('desktop:info', (e) => {
    if (!fromAppFrame(e)) throw new Error('forbidden');
    return { ...info(), appVersion: app.getVersion(), platform: process.platform };
  });
}
