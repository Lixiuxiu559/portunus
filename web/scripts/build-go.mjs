// 交叉编译 Go 后端到 web/bin/<osKey>/portunus[.exe]。
// osKey 与 electron-builder extraResources 的 ${os} 宏对齐：mac | win | linux。
//
// 用法：
//   node scripts/build-go.mjs              # 按当前平台/架构
//   node scripts/build-go.mjs darwin arm64 # 显式指定（CI 用）
//   node scripts/build-go.mjs windows amd64
//   node scripts/build-go.mjs darwin arm64 --release # 正式构建，移除调试信息

import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';

const scriptsDir = import.meta.dirname; // web/scripts
const webRoot = dirname(scriptsDir); // web
const repoRoot = dirname(webRoot); // 仓库根（main.go 所在）

// 目标平台/架构：参数优先，否则取当前进程
const release = process.argv.includes('--release');
const argv = process.argv.slice(2).filter((arg) => arg !== '--release');
const targetOs = argv[0] || (process.platform === 'win32' ? 'windows' : process.platform === 'darwin' ? 'darwin' : 'linux');
const targetArch = argv[1] || (process.arch === 'arm64' ? 'arm64' : 'amd64');

// GOOS / 输出目录键 映射
const goos = targetOs === 'windows' ? 'windows' : targetOs === 'darwin' ? 'darwin' : 'linux';
// electron-builder 的 ${os} 宏：darwin→mac、windows→win、linux→linux
const osKey = goos === 'darwin' ? 'mac' : goos === 'windows' ? 'win' : 'linux';
const binName = goos === 'windows' ? 'portunus.exe' : 'portunus';
const outDir = join(webRoot, 'bin', osKey);

mkdirSync(outDir, { recursive: true });

console.log(`[build-go] ${release ? 'release' : 'debug'} GOOS=${goos} GOARCH=${targetArch} -> ${outDir}/${binName}`);

const buildArgs = ['build'];
if (release) buildArgs.push('-trimpath', '-ldflags=-s -w');
buildArgs.push('-o', join(outDir, binName), '.');

const r = spawnSync('go', buildArgs, {
  cwd: repoRoot,
  env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: targetArch },
  stdio: 'inherit',
});

if (r.status !== 0) process.exit(r.status ?? 1);
if (goos === 'darwin') {
  const helperArgs = ['build'];
  if (release) helperArgs.push('-trimpath', '-ldflags=-s -w');
  helperArgs.push('-o', join(outDir, 'portunus-updater'), './cmd/portunus-updater');
  const helper = spawnSync('go', helperArgs, {
    cwd: repoRoot,
    env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: targetArch },
    stdio: 'inherit',
  });
  process.exit(helper.status ?? 1);
}
