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
});
