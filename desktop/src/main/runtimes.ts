// 内置运行时（Node 含 npx、uv 含 uvx）的落点与探测。
//
// **为什么不把它们加进系统 PATH**：那是替用户改他自己机器上的状态，而这三样只在 Gleam
// 内部用到（市场装 MCP 服务器要 npx/uvx，编程任务要 git）。同一口径已经立在
// internal/webui/goinstall.go：Go 工具链也是装进 Gleam 自己的目录、不碰全局 PATH。
// 所以这里的做法是——**只把目录放进 Gleam 自己拉起的进程的 PATH**：
//   Electron 主进程 → Go sidecar（环境变量）→ sidecar 起的 shell.exec / MCP 子进程。
// 这样 `npx` / `uvx` 在 Gleam 内部可用，用户自己的终端里不会凭空多出东西。
import { execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import type { Lang } from './locale';

export interface RuntimeProbe {
  name: 'node' | 'uv' | 'git';
  label: string;
  /** 能干活吗：内置的两样必须能跑；git 缺失只算「未装」，不是失败。 */
  ok: boolean;
  bundled: boolean;
  version: string;
  /** 给人看的一句：装在哪、缺了会怎样。 */
  detail: string;
}

/** 打包后在 resources/runtimes；开发态（没打包）退到 desktop/.runtimes/<platform>-<arch>。 */
export function runtimesRoot(): string {
  const packaged = join(process.resourcesPath, 'runtimes');
  if (existsSync(packaged)) return packaged;
  return join(__dirname, '..', '..', '.runtimes', `${process.platform}-${process.arch}`);
}

/** 要补进子进程 PATH 的目录。不存在的直接跳过——开发态没跑过 fetch:runtimes 是正常情况。 */
export function runtimeBinDirs(): string[] {
  const root = runtimesRoot();
  return [join(root, 'node'), join(root, 'uv')].filter((d) => existsSync(d));
}

/** 把运行时目录接到一段 PATH 前面。空列表时原样返回（不要平白多出一个分隔符）。 */
export function pathWithRuntimes(pathValue: string | undefined, dirs = runtimeBinDirs()): string {
  if (dirs.length === 0) return pathValue ?? '';
  return dirs.join(process.platform === 'win32' ? ';' : ':') + (pathValue ? (process.platform === 'win32' ? ';' : ':') + pathValue : '');
}

/** 探测版本。失败返回空串——首启探测不能因为一个命令超时就整个卡住。 */
function versionOf(command: string, args: string[]): Promise<string> {
  return new Promise((resolve) => {
    execFile(command, args, { timeout: 8000, windowsHide: true }, (err, stdout, stderr) => {
      if (err) return resolve('');
      const first = String(stdout || stderr || '').trim().split(/\r?\n/)[0] ?? '';
      resolve(first.replace(/^v/, '').trim());
    });
  });
}

/**
 * 首启检查。**内置的两样用绝对路径去问**（不靠 PATH 解析，否则 PATH 没接好时
 * 会把"没接上"误报成"没装"），git 则按系统 PATH 找——它本来就是系统级的东西。
 */
export async function probeRuntimes(lang: Lang = 'zh'): Promise<RuntimeProbe[]> {
  const root = runtimesRoot();
  const isWin = process.platform === 'win32';
  const nodeExe = join(root, 'node', isWin ? 'node.exe' : 'bin/node');
  const uvExe = join(root, 'uv', isWin ? 'uv.exe' : 'uv');
  const npxName = isWin ? 'npx.cmd' : 'npx';
  const uvxName = isWin ? 'uvx.exe' : 'uvx';

  const nodeVer = existsSync(nodeExe) ? await versionOf(nodeExe, ['-v']) : '';
  const npxThere = existsSync(join(root, 'node', npxName));
  const uvVer = existsSync(uvExe) ? await versionOf(uvExe, ['--version']) : '';
  const uvxThere = existsSync(join(root, 'uv', uvxName));
  const gitVer = await versionOf('git', ['--version']);

  const zh = lang === 'zh';
  return [
    {
      name: 'node',
      label: zh ? 'Node.js（内置，含 npx）' : 'Node.js (bundled, includes npx)',
      ok: !!nodeVer && npxThere,
      bundled: true,
      version: nodeVer,
      detail: nodeVer && npxThere
        ? (zh ? '市场里用 npx 起的 MCP 服务器可以直接装' : 'MCP servers that run through npx install straight from the market')
        : (zh ? '内置的 Node 没跑起来（可能被安全软件拦了）。用到 npx 的功能会提示重装' : 'The bundled Node did not start (security software may have blocked it). Features that need npx will offer to reinstall'),
    },
    {
      name: 'uv',
      label: zh ? 'uv（内置，含 uvx）' : 'uv (bundled, includes uvx)',
      ok: !!uvVer && uvxThere,
      bundled: true,
      version: uvVer,
      detail: uvVer && uvxThere
        ? (zh ? '市场里用 uvx 起的 Python 类 MCP 服务器可以直接装' : 'Python-based MCP servers that run through uvx install straight from the market')
        : (zh ? '内置的 uv 没跑起来。用到 uvx 的功能会提示重装' : 'The bundled uv did not start. Features that need uvx will offer to reinstall'),
    },
    {
      name: 'git',
      label: zh ? 'Git（用你系统里的）' : 'Git (from your system)',
      ok: !!gitVer,
      bundled: false,
      version: gitVer.replace(/^git version\s*/i, ''),
      detail: gitVer
        ? (zh ? '编程任务里的分支、提交、worktree 都用它' : 'Used for branches, commits and worktrees in coding tasks')
        : (zh ? '没检测到 Git：编程类任务会提示先装。内置不打包它（体积几百 MB，系统里通常已经有了）' : 'No Git detected: coding tasks will ask you to install it. Not bundled (hundreds of MB, and most systems already have it)'),
    },
  ];
}
