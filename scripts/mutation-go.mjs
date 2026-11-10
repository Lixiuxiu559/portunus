// 固定版本 Gremlins；只在当前 dirty/untracked Go 源码的隔离副本中运行。
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { appendFileSync, chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join, resolve } from 'node:path';

const repo = resolve(import.meta.dirname, '..');
const version = 'v0.6.0';
const state = join(repo, 'tmp/mutation-go');
const binary = join(state, 'bin/gremlins');
const args = process.argv.slice(2);
const allowedFlags = new Set(['--install', '--dry-run', '--strict', '--help']);
for (const arg of args) if (arg.startsWith('--') && !allowedFlags.has(arg)) throw new Error(`未知参数 ${arg}；使用 --help 查看用法`);
if (args.includes('--help')) {
  console.log('用法：node scripts/mutation-go.mjs [all|codec|rules|recovery|storage|api|domain] [--dry-run] [--strict]\n安装：node scripts/mutation-go.mjs --install\n默认 all；仅支持 macOS/Linux POSIX。报告在仓库 tmp/mutation-go/reports/。');
  process.exit(0);
}
if (process.platform === 'win32') throw new Error('此适配器使用 POSIX 可执行 shim，当前仅支持 macOS/Linux；Windows 尚未适配。');
if (args.filter((arg) => !arg.startsWith('--')).length > 1) throw new Error('每次只接受一个 profile；批量执行请使用 all');
const profile = args.find((arg) => !arg.startsWith('--')) || 'all';
let activeChild;
let interruption;
function signalOwnedChild(signal) {
  interruption = signal;
  if (activeChild?.pid) {
    try { process.kill(-activeChild.pid, signal); } catch (error) { if (error.code !== 'ESRCH') throw error; }
  }
}
process.on('SIGINT', () => signalOwnedChild('SIGINT'));
process.on('SIGTERM', () => signalOwnedChild('SIGTERM'));
function groupAlive(pid) {
  try { process.kill(-pid, 0); return true; } catch (error) { if (error.code === 'ESRCH') return false; throw error; }
}
async function cleanOwnedGroup(pid) {
  if (!pid || !groupAlive(pid)) return;
  try { process.kill(-pid, 'SIGTERM'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
  const termDeadline = Date.now() + 1000;
  while (groupAlive(pid) && Date.now() < termDeadline) await new Promise((done) => setTimeout(done, 25));
  if (groupAlive(pid)) {
    try { process.kill(-pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
    const killDeadline = Date.now() + 2000;
    while (groupAlive(pid) && Date.now() < killDeadline) await new Promise((done) => setTimeout(done, 25));
  }
  if (groupAlive(pid)) throw new Error(`本次进程组 ${pid} 未完全退出，拒绝记录清理成功`);
}
async function managed(command, parameters, options) {
  if (interruption) return { status: interruption === 'SIGINT' ? 130 : 143, stdout: '', stderr: '' };
  const child = spawn(command, parameters, { ...options, detached: true });
  activeChild = child;
  let stdout = '', stderr = '';
  child.stdout?.on('data', (data) => { stdout += data; options.onData?.(data); });
  child.stderr?.on('data', (data) => { stderr += data; options.onData?.(data); });
  let status;
  try {
    status = await new Promise((done) => {
      child.on('exit', (code, signal) => done(code ?? (signal === 'SIGINT' ? 130 : signal === 'SIGTERM' ? 143 : 1)));
      child.on('error', (error) => { stderr += String(error); done(1); });
    });
  } finally {
    // Gremlins kills a timed-out shim, while the shim's go/test descendants may
    // still be alive. Own/reap the whole detached group before deleting sources.
    await cleanOwnedGroup(child.pid);
    activeChild = undefined;
  }
  return { status, stdout, stderr, ownedProcessGroupCleaned: true };
}
if (profile === 'all' && !args.includes('--install')) {
  let result = 0;
  for (const part of ['codec', 'rules', 'recovery', 'storage', 'api']) {
    const child = await managed(process.execPath, [import.meta.filename, part, ...args.filter((arg) => arg.startsWith('--'))], { cwd: repo, env: process.env, stdio: 'inherit' });
    if (child.status === 10 && args.includes('--strict')) result = 10;
    else if (child.status !== 0) process.exit(child.status ?? 1);
  }
  process.exit(result);
}
const profiles = {
  codec: { package: 'backend/configtransfer', files: ['codec.go'], tests: '^(TestCodec|TestMutationCodec)' },
  rules: { package: 'backend/configtransfer', files: ['json.go', 'validate.go', 'plan.go', 'types.go'], tests: '^(TestStrictJSON|TestSourceIntegrity|TestInvalidCustomPNG|TestMutationRule|TestMutationJSON|TestMutationPlan)' },
  recovery: { package: 'backend/configtransfer', files: ['restore.go', 'service.go'], tests: '^TestMutation(Restore|Recovery|Stored|Service|Commit)' },
  storage: { package: 'backend/configtransfer', files: ['snapshot.go', 'schema.go'], tests: '^(TestSchemaIsExact|TestSourceIntegrity|TestMutationRestoreRoundTrip|TestMutationStorage)' },
  domain: { package: 'backend/configtransfer', files: null, tests: '' },
  api: { package: 'backend/api', files: ['config_transfer.go'], tests: '^TestConfigTransfer' },
};
if (profile !== 'all' && !profiles[profile]) throw new Error(`未知 profile ${profile}；可用 all, ${Object.keys(profiles).join(', ')}`);
const env = { ...process.env, GOCACHE: process.env.GOCACHE || join(state, 'build-cache'), GOTOOLCHAIN: 'local' };
mkdirSync(dirname(binary), { recursive: true });
const run = (command, parameters, options = {}) => spawnSync(command, parameters, { cwd: repo, env, encoding: 'utf8', ...options });
if (args.includes('--install')) {
  const installed = await managed('go', ['install', `github.com/go-gremlins/gremlins/cmd/gremlins@${version}`], { cwd: repo, env: { ...env, GOBIN: dirname(binary) }, stdio: 'inherit' });
  if (installed.status !== 0) process.exit(installed.status || 1);
  const preparation = mkdtempSync(join(tmpdir(), 'portunus-mutation-deps-'));
  let downloaded;
  try {
    for (const file of ['go.mod', 'go.sum']) copyFileSync(join(repo, file), join(preparation, file));
    downloaded = await managed('go', ['mod', 'download'], { cwd: preparation, env: { ...env, GOFLAGS: '-mod=readonly' }, stdio: 'inherit' });
  } finally { rmSync(preparation, { recursive: true, force: true }); }
  if (downloaded.status !== 0) process.exit(downloaded.status || 1);
  console.log(`已安装 ${binary} (${version})`);
  process.exit(0);
}
if (!existsSync(binary)) throw new Error('先执行 node scripts/mutation-go.mjs --install');
const buildInfo = run('go', ['version', '-m', binary]);
if (buildInfo.status !== 0 || !buildInfo.stdout.includes(`github.com/go-gremlins/gremlins\t${version}\t`)) throw new Error('Gremlins 版本不匹配；请重新执行 --install');
const selected = profiles[profile];
const report = join(state, 'reports', `${new Date().toISOString().replaceAll(':', '-')}-${profile}${args.includes('--dry-run') ? '-dry' : ''}`);
mkdirSync(join(report, 'test-runs'), { recursive: true });
function enumerateSources() {
  const listing = run('git', ['ls-files', '-co', '--exclude-standard', '-z']);
  if (listing.status !== 0) throw new Error('无法枚举当前工作区源码');
  return [...new Set(listing.stdout.split('\0').filter(Boolean).filter((file) => file.endsWith('.go') || file === 'go.mod' || file === 'go.sum' || file.startsWith('backend/') && !file.includes('/data/')))].sort();
}
const sourceFiles = enumerateSources();
const hash = (data) => createHash('sha256').update(data).digest('hex');
const manifest = () => {
  try {
    return Object.fromEntries(enumerateSources().map((file) => {
      try { return [file, hash(readFileSync(join(repo, file)))]; }
      catch (error) { return [file, `READ_ERROR:${error.code || error.message}`]; }
    }));
  } catch (error) { return { $enumerationError: String(error) }; }
};
const before = manifest();
if (Object.values(before).some((value) => value.startsWith('READ_ERROR:')) || before.$enumerationError) throw new Error('开始时无法完整读取源码，已终止');
const work = mkdtempSync(join(tmpdir(), 'portunus-mutation-go-'));
const snapshot = join(work, 'source');
const shim = join(work, 'shim');
const baselineArgs = ['test', '-count=1', '-timeout=120s', './' + selected.package];
const metadata = { tool: 'gremlins', version, profile, files: selected.files, testPattern: selected.tests || '(全部)', sourceManifest: before, baselineCommand: ['go', ...baselineArgs], dryRun: args.includes('--dry-run'), startedAt: new Date().toISOString() };
let status = 1;
let summary;
let actualGo;
try {
mkdirSync(shim, { recursive: true });
for (const file of sourceFiles) { mkdirSync(dirname(join(snapshot, file)), { recursive: true }); copyFileSync(join(repo, file), join(snapshot, file)); }
actualGo = run('go', ['env', 'GOROOT']).stdout.trim() + '/bin/go';
// v0.6.0 将退出 1 当作已杀死；Go 编译失败也可能退出 1。仅将明确编译失败改为 2，保持测试失败为 1。
// shim 同时保存真实测试输出与实际变异源码，便于独立核对，绝不在工作区写突变。
writeFileSync(join(shim, 'go'), `#!${process.execPath}\n` + `
const { spawnSync } = require('node:child_process');
const { readFileSync, writeFileSync, existsSync } = require('node:fs');
const { join, dirname } = require('node:path');
const args = process.argv.slice(2);
const mutant = args[0] === 'test' && args.includes('-failfast');
let changed = [];
if (mutant) {
  let root = process.cwd();
  while (!existsSync(join(root, 'go.mod')) && dirname(root) !== root) root = dirname(root);
  for (const file of ${JSON.stringify(sourceFiles.filter((file) => file.startsWith(selected.package + '/') && file.endsWith('.go') && !file.endsWith('_test.go')))}) {
    const original = readFileSync(join(${JSON.stringify(snapshot)}, file), 'utf8');
    const mutated = readFileSync(join(root, file), 'utf8');
    if (original !== mutated) changed.push({ file, original, mutated });
  }
}
const result = spawnSync(${JSON.stringify(actualGo)}, args, { encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
const stdout = result.stdout || '', stderr = result.stderr || '';
const notViable = result.status !== 0 && /\\[build failed\\]|\\"Action\\":\\"build-fail\\"/.test(stdout + stderr);
const timedOut = /panic: test timed out|\\"Action\\":\\"timeout\\"/.test(stdout + stderr);
if (mutant) writeFileSync(join(${JSON.stringify(join(report, 'test-runs'))}, process.pid + '.json'), JSON.stringify({ args, cwd: process.cwd(), status: result.status, signal: result.signal, notViable, timedOut, stdout, stderr, changed }));
process.stdout.write(stdout); process.stderr.write(stderr);
// Gremlins has no timeout exit code: retain this process until its own bounded
// context expires, so an inner go test timeout cannot be reported as KILLED.
if (mutant && timedOut) setInterval(() => {}, 1000);
else process.exit(notViable ? 2 : result.status === null ? 3 : result.status);
`);
chmodSync(join(shim, 'go'), 0o755);
  const baseline = await managed(actualGo, baselineArgs, { cwd: snapshot, env: { ...env, GOPROXY: 'off', GOSUMDB: 'off', GOFLAGS: '-mod=readonly' }, stdio: ['ignore', 'pipe', 'pipe'] });
  writeFileSync(join(report, 'baseline.log'), baseline.stdout + baseline.stderr);
  if (baseline.status !== 0) throw new Error(`普通测试基线失败，已终止；${report}/baseline.log`);
  console.log(`普通测试基线通过；${profile}；报告 ${report}`);
  const excluded = selected.files ? readdirSync(join(snapshot, selected.package)).filter((file) => file.endsWith('.go') && !file.endsWith('_test.go') && !selected.files.includes(file)) : [];
  const parameters = ['unleash', '--workers=2', '--timeout-coefficient=5', '--output=' + join(report, 'gremlins.json')];
  for (const file of excluded) parameters.push('--exclude-files', file.replaceAll('.', '\\.') + '$');
  if (args.includes('--dry-run')) parameters.push('--dry-run');
  metadata.command = [binary, ...parameters];
  const childEnv = { ...env, PATH: shim + delimiter + process.env.PATH, GOPROXY: 'off', GOSUMDB: 'off', GOFLAGS: '-mod=readonly -count=1' + (selected.tests ? ` -run=${selected.tests}` : ''), TMPDIR: work };
  metadata.goFlags = childEnv.GOFLAGS;
  const log = join(report, 'gremlins.log');
  const child = await managed(binary, parameters, { cwd: join(snapshot, selected.package), env: childEnv, stdio: ['ignore', 'pipe', 'pipe'], onData: (data) => { appendFileSync(log, data); process.stdout.write(data); } });
  status = child.status;
  metadata.ownedProcessGroupCleaned = child.ownedProcessGroupCleaned;
  if (status === 0 && !existsSync(join(report, 'gremlins.json'))) throw new Error('Gremlins 未生成机器报告，运行不完整');
  if (existsSync(join(report, 'gremlins.json'))) {
    const raw = JSON.parse(readFileSync(join(report, 'gremlins.json')));
    if (!Array.isArray(raw.files) || raw.files.length === 0) throw new Error('Gremlins 报告为空或结构错误，运行不完整');
    const counts = Object.fromEntries(['KILLED', 'LIVED', 'NOT COVERED', 'TIMED OUT', 'NOT VIABLE', 'SKIPPED', 'RUNNABLE'].map((key) => [key, 0]));
    for (const file of raw.files) {
      if (!Array.isArray(file.mutations)) throw new Error('Gremlins 缺少 mutations 数组');
      for (const mutation of file.mutations) {
        if (!(mutation.status in counts)) throw new Error(`未知突变状态 ${mutation.status}`);
        counts[mutation.status]++;
      }
    }
    if (!args.includes('--dry-run') && counts.RUNNABLE > 0) throw new Error('实际运行仍有 RUNNABLE 项，结果不完整');
    const total = Object.values(counts).reduce((sum, n) => sum + n, 0);
    // v0.6.0 的 mutants_total 只累加 killed/lived/not_viable，遗漏 timeout 和 uncovered。
    // 项目总数以逐文件状态为准，同时核对其原始汇总而不沿用不完整的 total。
    if (total === 0 || counts.KILLED + counts.LIVED + counts['NOT VIABLE'] !== raw.mutants_total) throw new Error('Gremlins 目标突变总数缺失或不一致');
    const expectedFiles = selected.files || readdirSync(join(snapshot, selected.package)).filter((file) => file.endsWith('.go') && !file.endsWith('_test.go'));
    const reported = new Set(raw.files.map((file) => file.file_name));
    // 无突变点的 DTO/常量文件可能不出现在 files，不能静默当作遗漏。
    const missing = expectedFiles.filter((file) => ![...reported].some((name) => name === file || name.endsWith('/' + file)));
    metadata.filesWithoutMutations = missing;
    if (missing.some((file) => file !== 'types.go')) throw new Error(`目标文件缺失：${missing.join(', ')}`);
    const toolCompleted = status === 0;
    const runCompleted = toolCompleted && !args.includes('--dry-run') && counts.RUNNABLE === 0;
    const resultComplete = runCompleted && ['TIMED OUT', 'NOT VIABLE', 'SKIPPED'].every((key) => counts[key] === 0);
    const allKilled = resultComplete && counts.LIVED === 0 && counts['NOT COVERED'] === 0;
    summary = { counts, total, executionExitCode: status, completed: toolCompleted, toolCompleted, runCompleted, resultComplete, allKilled };
    console.log(JSON.stringify({ profile, counts, report }));
    if (status === 0 && args.includes('--strict') && !allKilled) status = 10;
  }
} catch (error) {
  status = interruption ? interruption === 'SIGINT' ? 130 : 143 : 1;
  metadata.error = String(error);
  throw error;
} finally {
  metadata.finishedAt = new Date().toISOString();
  metadata.interruptedBy = interruption;
  const after = manifest();
  metadata.workspaceUnchanged = JSON.stringify(before) === JSON.stringify(after);
  if (!metadata.workspaceUnchanged) metadata.sourceManifestAfter = after;
  metadata.snapshotUnchanged = sourceFiles.every((file) => {
    try { return hash(readFileSync(join(snapshot, file))) === before[file]; } catch { return false; }
  });
  try { rmSync(work, { recursive: true, force: true }); }
  catch (error) { metadata.cleanupError = String(error); }
  metadata.temporarySourceRemoved = !existsSync(work);
  const integrityOK = metadata.workspaceUnchanged && metadata.snapshotUnchanged && metadata.temporarySourceRemoved && !metadata.error;
  if (!integrityOK) status = 1;
  metadata.exitCode = status;
  writeFileSync(join(report, 'run.json'), JSON.stringify(metadata, null, 2));
  if (summary) writeFileSync(join(report, 'summary.json'), JSON.stringify({ ...metadata, ...summary, completed: summary.completed && integrityOK, toolCompleted: summary.toolCompleted && integrityOK, runCompleted: summary.runCompleted && integrityOK, resultComplete: summary.resultComplete && integrityOK, allKilled: summary.allKilled && integrityOK }, null, 2));
  if (!integrityOK && !metadata.error) throw new Error(`源码指纹或临时目录清理检查失败；详见 ${report}/run.json`);
}
process.exit(status);
