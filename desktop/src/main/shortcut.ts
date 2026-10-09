// 全局唤起快捷键：在任何应用里按这个组合，把 Gleam 拉到前台。
//
// 这是**桌面壳专属**能力（浏览器里没有 globalShortcut），所以偏好也存在壳这边：
//   <userData>/global-shortcut.json
// 不放 Go 侧的配置里，是因为它跟窗口生命周期绑着，Go 那边既用不上也管不着。
//
// 注册可能失败：组合被别的程序占了、或者根本不是合法组合，Electron 会返回 false 或直接抛。
// 两种都要如实回给界面——不然用户设完发现没反应，只能猜。
import { app, globalShortcut, BrowserWindow } from 'electron';
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

export interface ShortcutResult {
  ok: boolean;
  accelerator: string;
  error?: string;
}

let target: BrowserWindow | null = null;
let current = '';

function storePath(): string {
  return join(app.getPath('userData'), 'global-shortcut.json');
}

function load(): string {
  try {
    const raw = JSON.parse(readFileSync(storePath(), 'utf8')) as { accelerator?: unknown };
    return typeof raw.accelerator === 'string' ? raw.accelerator : '';
  } catch {
    return ''; // 没存过 / 文件坏了：都当没设过
  }
}

function persist(accelerator: string): void {
  try {
    writeFileSync(storePath(), JSON.stringify({ accelerator }));
  } catch {
    // 写不进去就只生效本次运行：不该因为存不下而让快捷键失效
  }
}

function reveal(): void {
  if (!target || target.isDestroyed()) return;
  if (target.isMinimized()) target.restore();
  target.show();
  target.focus();
}

function apply(accelerator: string): ShortcutResult {
  if (current) {
    globalShortcut.unregister(current);
    current = '';
  }
  const accel = accelerator.trim();
  if (!accel) return { ok: true, accelerator: '' };
  try {
    if (!globalShortcut.register(accel, reveal)) {
      return { ok: false, accelerator: '', error: '这个组合已经被别的程序占用了' };
    }
  } catch (err) {
    return { ok: false, accelerator: '', error: `这个组合 Electron 不认：${(err as Error).message}` };
  }
  current = accel;
  return { ok: true, accelerator: accel };
}

/** 窗口就绪后装上：读上次存的，顺手把注册结果打出来，排障时不用猜。 */
export function installGlobalShortcut(win: BrowserWindow): void {
  target = win;
  const saved = load();
  if (!saved) return;
  const r = apply(saved);
  console.log(`[desktop] 全局快捷键 ${saved}：${r.ok ? '已注册' : '没注册上（' + r.error + '）'}`);
}

export function getShortcut(): string {
  return current;
}

export function setShortcut(accelerator: string): ShortcutResult {
  const r = apply(accelerator || '');
  if (r.ok) persist(r.accelerator);
  return r;
}
