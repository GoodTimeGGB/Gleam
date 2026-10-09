// Gleam desktop shell (Electron) — Phase 0 spike entry point.
import { app, BrowserWindow, dialog, session } from 'electron';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, resolve } from 'node:path';
import { APP_ORIGIN, installAppProtocol, registerAppScheme } from './protocol';
import { hardenContents, hardenSession, installIpc } from './security';
import { Sidecar, type SidecarSpec } from './sidecar';
import { createMainWindow } from './window';

const t0 = Date.now();
const log = (msg: string) => console.log(`[desktop +${Date.now() - t0}ms] ${msg}`);

// Dev/test knob: isolate Electron's profile (localStorage prefs, single-instance lock) from the real one.
// Must be set before the single-instance lock, which is scoped to userData.
if (process.env.GLEAM_DESKTOP_USER_DATA) app.setPath('userData', resolve(process.env.GLEAM_DESKTOP_USER_DATA));

registerAppScheme();
app.enableSandbox();

function sidecarSpec(): SidecarSpec {
  const exe = process.platform === 'win32' ? 'gleam.exe' : 'gleam';
  // Extra sidecar flags, e.g. "--mock-llm --data-dir /tmp/x". Dev/test knob; Phase 1 decides whether it ships.
  const args = (process.env.GLEAM_SIDECAR_ARGS || '').split(/\s+/).filter(Boolean);
  if (process.env.GLEAM_SIDECAR_BIN) {
    return { bin: resolve(process.env.GLEAM_SIDECAR_BIN), args, cwd: process.cwd() };
  }
  if (app.isPackaged) {
    return { bin: join(process.resourcesPath, 'bin', exe), args, cwd: homedir() };
  }
  // Dev: desktop/dist/main/index.js -> desktop/.sidecar/<platform>-<arch>/gleam; run from the repo root
  // so the sidecar picks up configs/config.yaml exactly like `gleam webui` does.
  const desktopDir = resolve(__dirname, '..', '..');
  const bin = join(desktopDir, '.sidecar', `${process.platform}-${process.arch}`, exe);
  return { bin, args, cwd: resolve(desktopDir, '..') };
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

  app.whenReady().then(async () => {
    hardenSession(session.defaultSession);
    installIpc(() => ({ sidecarVersion: sidecar?.ready?.version ?? null }));
    installAppProtocol(() => (sidecar?.ready ? { addr: sidecar.ready.addr, token: sidecar.token } : null));

    const spec = sidecarSpec();
    if (!existsSync(spec.bin)) {
      dialog.showErrorBox('Gleam', `Backend binary not found:\n${spec.bin}\n\nRun "npm run build:sidecar" first.`);
      app.exit(1);
      return;
    }
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

    const w = Number(process.env.GLEAM_DESKTOP_WIDTH) || undefined;
    const h = Number(process.env.GLEAM_DESKTOP_HEIGHT) || undefined;
    win = createMainWindow({ width: w, height: h });
    win.on('closed', () => (win = null));
    win.webContents.once('did-finish-load', () => log('renderer did-finish-load'));
    await win.loadURL(`${APP_ORIGIN}/`);
  });
}
