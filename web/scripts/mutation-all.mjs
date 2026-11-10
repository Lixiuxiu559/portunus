import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const flags = process.argv.slice(2).filter((arg) => arg !== '--');
const allowed = new Set(['--dry-run', '--strict', '--help']);
if (flags.some((arg) => !allowed.has(arg))) {
  console.error('用法：pnpm test:mutation [--dry-run] [--strict]');
  process.exitCode = 2;
} else if (flags.includes('--help')) {
  console.log('串行运行配置迁移的 JS core 与 Go all 突变测试。');
  console.log('--dry-run：仅验证基线与工具接入；--strict：对存活、无覆盖或不完整结果返回失败。');
  console.log('普通完成仅表示分析结束，是否检出错误请查看各端的分类报告。');
} else {
  let active;
  let interrupted = false;
  const stop = () => { interrupted = true; active?.kill('SIGTERM'); };
  process.once('SIGINT', stop);
  process.once('SIGTERM', stop);
  const suites = [
    { name: 'JS core', cwd: path.resolve(here, '..'), args: [path.join(here, 'mutation-js.mjs'), '--profile', 'core', ...flags] },
    { name: 'Go all', cwd: path.resolve(here, '../..'), args: [path.resolve(here, '../../scripts/mutation-go.mjs'), 'all', ...flags] },
  ];
  const results = [];
  for (const suite of suites) {
    if (interrupted) break;
    console.log(`[mutation] 开始 ${suite.name}`);
    const code = await new Promise((resolve) => {
      active = spawn(process.execPath, suite.args, { cwd: suite.cwd, env: process.env, stdio: 'inherit' });
      active.once('error', () => resolve(2));
      active.once('exit', (status) => resolve(status ?? 2));
    });
    active = undefined;
    results.push({ name: suite.name, exitCode: code });
  }
  process.removeListener('SIGINT', stop);
  process.removeListener('SIGTERM', stop);
  console.log(`[mutation] 执行结果：${JSON.stringify(results)}`);
  process.exitCode = interrupted ? 130 : results.some((result) => result.exitCode !== 0) ? 1 : 0;
}
