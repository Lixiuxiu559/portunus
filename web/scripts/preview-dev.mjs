import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { createServer } from 'node:net';
import path from 'node:path';
const webRoot = path.resolve(import.meta.dirname, '..');
// 发现占用即退出，绝不关闭已运行的 Dev 或正式应用。
await new Promise((resolve, reject) => {
  const server = createServer();
  server.once('error', () => reject(new Error('13061 已被占用，请使用已打开的 Dev，或先退出该 Dev 实例')));
  server.listen(13061, '127.0.0.1', () => server.close(resolve));
});
const archDir = process.arch === 'arm64' ? 'mac-arm64' : 'mac';
const target = process.platform === 'darwin'
  ? path.join(webRoot, 'dist-dev', archDir, 'Portunus Dev.app')
  : path.join(webRoot, 'dist-dev', process.platform === 'win32' ? 'win-unpacked/Portunus Dev.exe' : 'linux-unpacked/portunus-dev');
if (!existsSync(target)) throw new Error('找不到 Portunus Dev，请先执行 pnpm build:dev');
const child = spawn(process.platform === 'darwin' ? 'open' : target, process.platform === 'darwin' ? ['-n', target] : [], { detached: true, stdio: 'ignore' });
child.on('error', (error) => { console.error(error); process.exitCode = 1; });
child.unref();
