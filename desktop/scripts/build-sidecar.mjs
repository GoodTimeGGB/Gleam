// Builds the Go sidecar (cmd/gleam) for the current (or a given) platform into
// desktop/.sidecar/<platform>-<arch>/gleam[.exe]. Node naming (win32/darwin/linux, x64/arm64)
// is used so electron-builder's ${platform}-${arch} macros pick the right folder.
//
//   node scripts/build-sidecar.mjs                 # host platform
//   node scripts/build-sidecar.mjs win32 x64       # cross-compile (pure Go, no cgo)
import { execFileSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const desktopDir = resolve(here, '..');
const repoRoot = resolve(desktopDir, '..');

const platform = process.argv[2] || process.platform;
const arch = process.argv[3] || process.arch;
const goos = { win32: 'windows', darwin: 'darwin', linux: 'linux' }[platform];
const goarch = { x64: 'amd64', arm64: 'arm64' }[arch];
if (!goos || !goarch) {
  console.error(`unsupported target ${platform}-${arch}`);
  process.exit(2);
}

const outDir = join(desktopDir, '.sidecar', `${platform}-${arch}`);
mkdirSync(outDir, { recursive: true });
const out = join(outDir, goos === 'windows' ? 'gleam.exe' : 'gleam');

const args = ['build', '-trimpath', '-ldflags', '-s -w', '-o', out, './cmd/gleam'];
console.log(`[build-sidecar] GOOS=${goos} GOARCH=${goarch} go ${args.join(' ')}`);
execFileSync('go', args, {
  cwd: repoRoot,
  stdio: 'inherit',
  env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0' },
});
console.log(`[build-sidecar] -> ${out}`);
