// 独立预览产物，复用本机 Electron，无需下载或安装到 /Applications。
import { spawnSync } from 'node:child_process';
import path from 'node:path';
const cwd = path.resolve(import.meta.dirname, '..');
function run(command, args, extraEnv = {}) {
  const result = spawnSync(command, args, { cwd, stdio: 'inherit', shell: process.platform === 'win32', env: { ...process.env, ...extraEnv } });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
}
run('pnpm', ['run', 'build:go']);
run('pnpm', ['run', 'build:renderer']);
run('pnpm', ['exec', 'electron-builder', '--dir', '--config', 'electron-builder.dev.cjs', '-c.electronDist=node_modules/electron/dist'], { CSC_IDENTITY_AUTO_DISCOVERY: 'false' });
