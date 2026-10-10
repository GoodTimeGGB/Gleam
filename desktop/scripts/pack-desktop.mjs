// 打包桌面端，但把产物放到**工作区之外**。
//
// 为什么不放在 desktop/release/：编辑器/Agent 的工作区索引器会对仓库里新出现的文件持句柄，
// 而 electron-builder 在打包前要清空自己的解包目录——索引器正抓着那批新文件时，清理就会
// 以 `EBUSY: resource busy or locked` 失败，整个打包中断（2026-10-10 连撞两次，用 Windows
// Restart Manager 查到句柄持有者是宿主的索引进程，不是杀软、也不是残留进程）。
// 放到系统临时目录后，索引器扫不到，这类竞争就不会再发生；顺带 130+ MB 的产物也不再堆在仓库里。
//
//   node scripts/pack-desktop.mjs --win nsis
//   node scripts/pack-desktop.mjs --linux dir
import { execFileSync } from 'node:child_process';
import { existsSync, statSync, readdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import os from 'node:os';

const here = dirname(fileURLToPath(import.meta.url));
const desktopDir = resolve(here, '..');
const out = join(os.tmpdir(), 'gleam-pack');
const args = process.argv.slice(2);
if (!args.length) {
  console.error('用法：node scripts/pack-desktop.mjs <electron-builder 的参数，如 --win nsis>');
  process.exit(2);
}

console.log(`[pack-desktop] 输出目录：${out}（工作区之外，索引器扫不到）`);
execFileSync('npx', ['electron-builder', ...args, `--config.directories.output=${out}`], {
  cwd: desktopDir, stdio: 'inherit', shell: process.platform === 'win32',
});

// 打完把产物列出来：产物不在仓库里了，得让人一眼看到它在哪、多大。
// 只看输出目录的**顶层**——递归下去会把 win-unpacked 里的 node.exe、npm 自带的 .yml 也列进来。
const show = (dir) => {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (!e.isFile()) continue;
    const p = join(dir, e.name);
    if (!/\.(exe|dmg|AppImage|deb|blockmap|yml)$/i.test(e.name)) continue;
    console.log(`[pack-desktop] ${(statSync(p).size / 1048576).toFixed(1)} MB  ${p}`);
  }
};
if (existsSync(out)) { console.log('[pack-desktop] 产物：'); show(out); }
