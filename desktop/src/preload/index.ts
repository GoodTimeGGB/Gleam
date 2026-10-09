// Sandboxed preload: only `electron`'s contextBridge/ipcRenderer are available here.
// Exposes a frozen, minimal API; the UI does not depend on it (it runs unchanged in a plain browser).
import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('gleamDesktop', {
  isDesktop: true,
  info: (): Promise<unknown> => ipcRenderer.invoke('desktop:info'),
});
