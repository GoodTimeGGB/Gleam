// Gleam desktop shell (Electron) — Phase 0 spike entry point.
import { app, BrowserWindow, dialog, session } from 'electron';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { systemLang } from './locale';
import { APP_ORIGIN, installAppProtocol, registerAppScheme } from './protocol';
import { hardenContents, hardenSession, installIpc } from './security';
import { Sidecar, type SidecarSpec } from './sidecar';
import { createMainWindow } from './window';
import { installGlobalShortcut } from './shortcut';
import { probeRuntimes, runtimeBinDirs } from './runtimes';
import { Splash } from './splash';

const t0 = Date.now();
const log = (msg: string) => console.log(`[desktop +${Date.now() - t0}ms] ${msg}`);

/** 内置运行时没就绪时，启动屏停几秒让人看清原因（主界面同时在后面加载）。 */
const SPLASH_HOLD_SECONDS = 5;

// Dev/test knob: isolate Electron's profile (localStorage prefs, single-instance lock) from the real one.
// Must be set before the single-instance lock, which is scoped to userData.
if (process.env.GLEAM_DESKTOP_USER_DATA) app.setPath('userData', resolve(process.env.GLEAM_DESKTOP_USER_DATA));

registerAppScheme();
app.enableSandbox();

function sidecarSpec(): SidecarSpec {
  const exe = process.platform === 'win32' ? 'gleam.exe' : 'gleam';
  // Extra sidecar flags, e.g. "--mock-llm --data-dir /tmp/x". Dev/test knob; Phase 1 decides whether it ships.
  const args = (process.env.GLEAM_SIDECAR_ARGS || '').split(/\s+/).filter(Boolean);
  const extraPath = runtimeBinDirs();
  if (process.env.GLEAM_SIDECAR_BIN) {
    return { bin: resolve(process.env.GLEAM_SIDECAR_BIN), args, cwd: process.cwd(), extraPath };
  }
  if (app.isPackaged) {
    return { bin: join(process.resourcesPath, 'bin', exe), args, cwd: homedir(), extraPath };
  }
  // Dev: desktop/dist/main/index.js -> desktop/.sidecar/<platform>-<arch>/gleam; run from the repo root
  // so the sidecar picks up configs/config.yaml exactly like `gleam webui` does.
  const desktopDir = resolve(__dirname, '..', '..');
  const bin = join(desktopDir, '.sidecar', `${process.platform}-${process.arch}`, exe);
  return { bin, args, cwd: resolve(desktopDir, '..'), extraPath };
}

if (!app.requestSingleInstanceLock()) {
  // Another instance owns the profile: it gets 'second-instance' and focuses its window. We just leave.
  console.log('[desktop] another instance is running; handing over and exiting');
  app.exit(0);
} else {
  run();
}

function run(): void {
  let win: BrowserWindow | null = null;
  let sidecar: Sidecar | null = null;
  let quitting = false;

  app.on('second-instance', () => {
    log('second-instance: focusing the existing window');
    if (!win) return;
    if (win.isMinimized()) win.restore();
    win.show();
    win.focus();
  });

  app.on('web-contents-created', (_e, contents) => hardenContents(contents));

  // Spike behaviour: closing the window quits (close-to-tray needs the tray, which is Phase 1).
  app.on('window-all-closed', () => app.quit());

  app.on('before-quit', (e) => {
    if (quitting || !sidecar?.running) return;
    e.preventDefault();
    quitting = true;
    log('quit: stopping sidecar');
    void sidecar.stop().then((how) => {
      log(`sidecar stopped (${how})`);
      app.quit();
    });
  });

  for (const sig of ['SIGINT', 'SIGTERM'] as const) process.on(sig, () => app.quit());

  const w = Number(process.env.GLEAM_DESKTOP_WIDTH) || undefined;
  const h = Number(process.env.GLEAM_DESKTOP_HEIGHT) || undefined;

  app.whenReady().then(async () => {
    hardenSession(session.defaultSession);
    installIpc(() => ({ sidecarVersion: sidecar?.ready?.version ?? null }), () => {
      // File ▸ New window (Ctrl+Shift+N): another window on the same sidecar / app:// origin.
      const extra = createMainWindow({ width: w, height: h });
      void extra.loadURL(`${APP_ORIGIN}/`);
    });
    installAppProtocol(() => (sidecar?.ready ? { addr: sidecar.ready.addr, token: sidecar.token } : null));

    const spec = sidecarSpec();
    if (!existsSync(spec.bin)) {
      dialog.showErrorBox('Gleam', `Backend binary not found:\n${spec.bin}\n\nRun "npm run build:sidecar" first.`);
      app.exit(1);
      return;
    }

    // ---------- 环境准备 ----------
    // 先亮这一屏，再起后端：用户双击之后看到的第一眼应该是"在准备"，而不是一段空白。
    // 检查是真的（跑 node -v / uv --version / git --version），所以通常几百毫秒就完；
    // 内置运行时跑不起来时才停下来说清楚——那时候让它自己消失，用户永远不知道哪里坏了。
    const lang = systemLang();
    let splash: Splash | null = null;
    if (!process.env.GLEAM_NO_SPLASH) {
      splash = new Splash(lang);
      splash.render({
        status: lang === 'zh' ? '正在检查内置运行时…' : 'Checking bundled runtimes…',
        items: [], total: 3, holdSeconds: 0,
      });
    }
    const probes = await probeRuntimes(lang);
    const broken = probes.filter((p) => p.bundled && !p.ok);
    for (const p of probes) log(`runtime ${p.name}: ${p.ok ? 'ok' : 'missing'} ${p.version || ''}`);
    // 有坏消息时留几秒让人看清原因，到点自己放行；一切正常就不留——这屏是给"坏消息"用的，
    // 不是给"每次都看一遍"用的。
    const holdSeconds = broken.length ? SPLASH_HOLD_SECONDS : 0;
    splash?.render({
      status: broken.length
        ? (lang === 'zh'
          ? `${broken.length} 项没就绪——可以继续，用到它的功能会提示`
          : `${broken.length} item(s) not ready — you can continue; features that need them will tell you`)
        : (lang === 'zh' ? '环境已就绪，正在启动…' : 'Environment ready — starting…'),
      items: probes,
      total: probes.length,
      holdSeconds,
    });

    sidecar = new Sidecar(spec);
    sidecar.on('unexpected-exit', (code, signal) => {
      // Phase 1: restart with backoff and show a reconnect state. The spike surfaces it and quits.
      log(`sidecar exited unexpectedly (code=${code}, signal=${signal})`);
      if (quitting) return;
      quitting = true;
      dialog.showErrorBox('Gleam', `The Gleam backend stopped unexpectedly (code=${code}, signal=${signal}).`);
      app.quit();
    });
    try {
      const ready = await sidecar.start();
      log(`sidecar ready pid=${ready.pid} addr=${ready.addr} version=${ready.version}`);
    } catch (err) {
      log(`sidecar failed to start: ${String(err)}`);
      dialog.showErrorBox('Gleam', `The Gleam backend failed to start:\n${String(err)}`);
      app.exit(1);
      return;
    }

    win = createMainWindow({ width: w, height: h });
    // 全局唤起快捷键：装在这一个窗口上（它才是"那个 Gleam"）
    installGlobalShortcut(win);
    win.on('closed', () => (win = null));
    win.webContents.once('did-finish-load', () => log('renderer did-finish-load'));
    await win.loadURL(`${APP_ORIGIN}/`);
    // 有坏消息时让那屏多留几秒（页面上有倒计时）；没坏消息时立刻收掉。
    if (holdSeconds > 0) await new Promise((r) => setTimeout(r, holdSeconds * 1000));
    splash?.close();
  });
}
