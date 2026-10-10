// Sandboxed preload: only `electron`'s contextBridge/ipcRenderer are available here.
// Exposes a frozen, minimal API; the UI does not depend on it (it runs unchanged in a plain browser).
// Every function maps to exactly one allowlisted channel in src/main/security.ts.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron';

type WindowState = { maximized: boolean; fullscreen: boolean };

contextBridge.exposeInMainWorld('gleamDesktop', {
  isDesktop: true,
  platform: process.platform,
  info: (): Promise<unknown> => ipcRenderer.invoke('desktop:info'),
  /** undo | redo | cut | copy | paste | pasteAndMatchStyle | delete | selectAll */
  edit: (action: string): Promise<void> => ipcRenderer.invoke('desktop:edit', action),
  /** reload | forceReload | resetZoom | zoomIn | zoomOut */
  view: (action: string): Promise<void> => ipcRenderer.invoke('desktop:view', action),
  /** minimize | maximize (toggle) | close | fullscreen (toggle) */
  window: (action: string): Promise<WindowState> => ipcRenderer.invoke('desktop:window', action),
  windowState: (): Promise<WindowState> => ipcRenderer.invoke('desktop:windowState'),
  onWindowState: (cb: (s: WindowState) => void): (() => void) => {
    const h = (_e: IpcRendererEvent, s: WindowState) => cb(s);
    ipcRenderer.on('desktop:window-state', h);
    return () => ipcRenderer.removeListener('desktop:window-state', h);
  },
  openExternal: (url: string): Promise<boolean> => ipcRenderer.invoke('desktop:openExternal', url),
  capture: (): Promise<string> => ipcRenderer.invoke('desktop:capture'),
  newWindow: (): Promise<void> => ipcRenderer.invoke('desktop:newWindow'),
  showOpenDialog: (options: Electron.OpenDialogOptions): Promise<string[]> => ipcRenderer.invoke('desktop:showOpenDialog', options),
  /** 全局唤起快捷键：get() 读当前；set('') 取消。返回 { ok, accelerator, error? } */
  globalShortcut: (action: 'get' | 'set', accelerator?: string): Promise<{ ok: boolean; accelerator: string; error?: string }> =>
    ipcRenderer.invoke('desktop:globalShortcut', action, accelerator),
  update: {
    check: (): Promise<unknown> => ipcRenderer.invoke('desktop:update:check'),
    status: (): Promise<unknown> => ipcRenderer.invoke('desktop:update:status'),
    restart: (): Promise<unknown> => ipcRenderer.invoke('desktop:update:restart'),
    onStatus: (cb: (s: unknown) => void): (() => void) => {
      const h = (_e: IpcRendererEvent, s: unknown) => cb(s);
      ipcRenderer.on('desktop:update-status', h);
      return () => ipcRenderer.removeListener('desktop:update-status', h);
    },
  },
  /** 托盘：把界面自己的数据（当前语言 + 最近会话）推给壳，壳按它重建菜单；
   *  菜单里点了东西再从这里回到界面执行（动作名见 src/main/tray.ts）。 */
  tray: {
    sync: (state: { lang: string; recents: { id: string; title: string }[] }): Promise<unknown> =>
      ipcRenderer.invoke('desktop:tray:sync', state),
    onAction: (cb: (a: unknown) => void): (() => void) => {
      const h = (_e: IpcRendererEvent, a: unknown) => cb(a);
      ipcRenderer.on('desktop:tray-action', h);
      return () => ipcRenderer.removeListener('desktop:tray-action', h);
    },
  },
});
