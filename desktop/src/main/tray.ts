// 托盘：图标常驻通知区域，右键出菜单（最近会话 / 新建聊天 / 打开 Gleam / 设置 / 退出 Gleam）。
//
// 菜单里的动作分两类，界线是"有没有第二种实现"：
//   - 窗口自己的事（显示 / 收起 / 退出）就地做掉；
//   - 界面自己的事（新建聊天、进某个会话、进设置）这里**不重写一遍**：先亮窗口，再把动作
//     交给渲染层，由它调界面已有的入口（#convo-new / openConvo / showView）。托盘只是入口，
//     不是第二套行为。
//
// 「最近」那份数据由渲染层的托盘桥推过来（internal/webui/static/tray.js）：它读的是界面
// 同一条 /api/conversations（Go 侧已按最近更新倒序），取前若干条。主进程不自己去查——
// 同一个列表两个 owner 迟早会漂。
import { app, BrowserWindow, Menu, Tray, ipcMain, nativeImage, type IpcMainInvokeEvent, type MenuItemConstructorOptions, type NativeImage } from 'electron';
import { existsSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { APP_HOST, APP_SCHEME } from './protocol';

/** 「最近」直接列出的条数，其余收进「更多」子菜单。 */
const RECENT_TOP = 5;
const RECENT_MORE = 10;
const TITLE_MAX = 40;

export type TrayLang = 'zh' | 'en';
export interface TrayRecent { id: string; title: string }
export type TrayAction = { action: 'new-chat' | 'open-convo' | 'settings'; id?: string };

const TEXT = {
  zh: { recent: '最近', more: '更多', newChat: '新建聊天', open: '打开 Gleam', settings: '设置', quit: '退出 Gleam' },
  en: { recent: 'Recent', more: 'More', newChat: 'New chat', open: 'Open Gleam', settings: 'Settings', quit: 'Quit Gleam' },
} as const;

const HINT = {
  zh: { title: 'Gleam 仍在后台运行', content: '关闭窗口只是把它收起来。要退出，请用托盘图标右键菜单里的「退出 Gleam」。' },
  en: { title: 'Gleam is still running', content: 'Closing the window only hides it. To quit, use "Quit Gleam" in the tray menu.' },
} as const;

let tray: Tray | null = null;
let lang: TrayLang = 'zh';
let recents: TrayRecent[] = [];

// 与 security.ts、updater.ts 同一个口径：只认自己的顶层 app://gleam 框架。
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

// 托盘图标偏小，用 512 那张窗口图标缩下来会糊：Windows 用多尺寸 ico（16/24/32/48…），
// 其它平台用 32px png。dev 下从仓库 assets 取，打包后从 resources/tray 取
// （electron-builder.yml 的 extraResources 带过去）。
function trayIcon(): NativeImage {
  const assets = join(__dirname, '..', '..', '..', 'assets');
  const res = process.resourcesPath ?? '';
  const candidates = process.platform === 'win32'
    ? [join(res, 'tray', 'gleam.ico'), join(assets, 'gleam.ico')]
    : [join(res, 'tray', 'gleam-32.png'), join(assets, 'icons', 'gleam-32.png')];
  for (const p of candidates) {
    if (p && existsSync(p)) {
      const img = nativeImage.createFromPath(p);
      if (!img.isEmpty()) return img;
    }
  }
  console.warn(`[tray] 没找到托盘图标，先用空图（图标位置仍然在，只是透明的）：${candidates.join(' | ')}`);
  return nativeImage.createEmpty();
}

/** 菜单动作交给谁：优先用户正在看的那个窗口；全在后台时就给第一个。 */
function target(): BrowserWindow | null {
  const focused = BrowserWindow.getFocusedWindow();
  if (focused && !focused.isDestroyed()) return focused;
  return BrowserWindow.getAllWindows().find((w) => !w.isDestroyed()) ?? null;
}

function reveal(): void {
  const w = target();
  if (!w) return;
  if (w.isMinimized()) w.restore();
  w.show();
  w.focus();
}

/** 界面自己的动作：先亮窗口再交出去——窗口还藏着的时候点菜单，不该"什么都没发生"。 */
export function dispatchTrayAction(a: TrayAction): void {
  reveal();
  const w = target();
  if (w) w.webContents.send('desktop:tray-action', a);
}

function label(title: string, fallback: string): string {
  const t = (title || '').replace(/\s+/g, ' ').trim() || fallback;
  return t.length > TITLE_MAX ? `${t.slice(0, TITLE_MAX - 1)}…` : t;
}

/** 菜单当前长什么样，一行说完。dev 下打日志用：这样不看托盘也能核对结构与文案。 */
export function describeTrayMenu(): string {
  const t = TEXT[lang];
  const head = recents.slice(0, RECENT_TOP).map((r) => JSON.stringify(label(r.title, r.id.slice(0, 8))));
  const rest = recents.length - RECENT_TOP;
  const recent = recents.length === 0
    ? `${t.recent}(灰)`
    : `${t.recent}[${head.join(' / ')}${rest > 0 ? ` + ${t.more}(${Math.min(rest, RECENT_MORE)})` : ''}]`;
  return [recent, t.newChat, t.open, t.settings, t.quit].join(' | ');
}

function buildMenu(): Menu {
  const t = TEXT[lang];
  const convoItem = (r: TrayRecent): MenuItemConstructorOptions => ({
    label: label(r.title, r.id.slice(0, 8)),
    click: () => dispatchTrayAction({ action: 'open-convo', id: r.id }),
  });

  const items: MenuItemConstructorOptions[] = [];
  if (recents.length === 0) {
    // 一条都没有时给个灰项占位：菜单结构不随数据跳来跳去，也让"还没有会话"看得见。
    items.push({ label: t.recent, enabled: false });
  } else {
    const sub = recents.slice(0, RECENT_TOP).map(convoItem);
    const rest = recents.slice(RECENT_TOP, RECENT_TOP + RECENT_MORE);
    if (rest.length) sub.push({ type: 'separator' }, { label: t.more, submenu: rest.map(convoItem) });
    items.push({ label: t.recent, submenu: sub });
  }

  items.push(
    { type: 'separator' },
    { label: t.newChat, click: () => dispatchTrayAction({ action: 'new-chat' }) },
    { label: t.open, click: reveal },
    { label: t.settings, click: () => dispatchTrayAction({ action: 'settings' }) },
    { type: 'separator' },
    { label: t.quit, click: () => app.quit() }, // 不设 quitting：让 before-quit 照常去停后端
  );
  return Menu.buildFromTemplate(items);
}

/** 渲染层推来的状态：语言 + 最近会话。形状不对的部分一律忽略，只吃认得的。 */
function applySync(state: unknown): void {
  const s = (typeof state === 'object' && state !== null ? state : {}) as { lang?: unknown; recents?: unknown };
  if (s.lang === 'en' || s.lang === 'zh') lang = s.lang;
  if (Array.isArray(s.recents)) {
    recents = s.recents
      .filter((r): r is TrayRecent => !!r && typeof r === 'object'
        && typeof (r as TrayRecent).id === 'string' && (r as TrayRecent).id !== ''
        && typeof (r as TrayRecent).title === 'string')
      .slice(0, RECENT_TOP + RECENT_MORE)
      .map((r) => ({ id: r.id, title: r.title }));
  }
  tray?.setContextMenu(buildMenu());
  console.log(`[tray] 菜单已更新：lang=${lang} recents=${recents.length}`);
  if (!app.isPackaged) console.log(`[tray] 内容：${describeTrayMenu()}`);
}

export function installTray(): void {
  if (tray) return;
  const icon = trayIcon();
  tray = new Tray(icon);
  tray.setToolTip('Gleam');
  tray.setContextMenu(buildMenu());
  // 左键：显示 / 收起。右键由 setContextMenu 负责。
  tray.on('click', () => {
    const w = target();
    if (!w) return;
    if (w.isVisible() && !w.isMinimized()) w.hide();
    else reveal();
  });

  ipcMain.handle('desktop:tray:sync', (e, state: unknown) => {
    if (!fromAppFrame(e)) throw new Error('forbidden');
    applySync(state);
    return { ok: true };
  });
  console.log(`[tray] 已创建：icon=${icon.isEmpty() ? '(empty)' : 'ok'} 后端语言默认=${lang}`);
}

/**
 * 第一次把窗口收进托盘时说一句"它没退出"。只说一次——这句是给"点完 ✕ 发现应用还在"这个
 * 困惑用的，之后用户已经知道了，再弹就是打扰。
 */
export function trayHintOnce(): void {
  if (!tray) return;
  const flag = join(app.getPath('userData'), 'tray-hint.json');
  if (existsSync(flag)) return;
  try {
    writeFileSync(flag, JSON.stringify({ shown: new Date().toISOString() }));
  } catch {
    return; // 存不下（用户目录异常）就干脆不提示，好过每次点 ✕ 都弹一遍
  }
  // displayBalloon 只有 Windows 有；其它平台没这个方法。
  if (process.platform !== 'win32') return;
  const t = HINT[lang];
  try {
    tray.displayBalloon({ title: t.title, content: t.content });
  } catch {
    // 气泡弹不出来不影响窗口已经收起来了
  }
}
