// 交叉编译 Go 后端到 web/bin/<osKey>/portunus[.exe]。
// osKey 与 electron-builder extraResources 的 ${os} 宏对齐：mac | win | linux。
//
// 用法：
//   node scripts/build-go.mjs              # 按当前平台/架构
//   node scripts/build-go.mjs darwin arm64 # 显式指定（CI 用）
//   node scripts/build-go.mjs windows amd64

import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';

const scriptsDir = import.meta.dirname; // web/scripts
const webRoot = dirname(scriptsDir); // web
const repoRoot = dirname(webRoot); // 仓库根（main.go 所在）

// 目标平台/架构：参数优先，否则取当前进程
const argv = process.argv.slice(2);
const targetOs = argv[0] || (process.platform === 'win32' ? 'windows' : process.platform === 'darwin' ? 'darwin' : 'linux');
const targetArch = argv[1] || (process.arch === 'arm64' ? 'arm64' : 'amd64');

// GOOS / 输出目录键 映射
const goos = targetOs === 'windows' ? 'windows' : targetOs === 'darwin' ? 'darwin' : 'linux';
// electron-builder 的 ${os} 宏：darwin→mac、windows→win、linux→linux
const osKey = goos === 'darwin' ? 'mac' : goos === 'windows' ? 'win' : 'linux';
const binName = goos === 'windows' ? 'portunus.exe' : 'portunus';
const outDir = join(webRoot, 'bin', osKey);

mkdirSync(outDir, { recursive: true });

console.log(`[build-go] GOOS=${goos} GOARCH=${targetArch} -> ${outDir}/${binName}`);

const r = spawnSync('go', ['build', '-o', join(outDir, binName), '.'], {
  cwd: repoRoot,
  env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: targetArch },
  stdio: 'inherit',
});

process.exit(r.status ?? 1);