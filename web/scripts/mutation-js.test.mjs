import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { changedSources, classify, configForProfile, expandTestFiles, missingReportFiles, parseArgs, profiles, sourceManifest, strictExitCode, summaryHTML } from './mutation-js-lib.mjs';
import { runNode } from './mutation-js-process.mjs';

test('profile 只突变四个指定生产文件，renderer 测试使用目录 glob', () => {
  assert.equal(profiles.core.mutate.length, 4);
  for (const profile of Object.keys(profiles)) {
    const config = configForProfile(profile, {});
    assert.equal(config.inPlace, false);
    assert.equal(config.ignoreStatic, false);
    assert.equal(config.coverageAnalysis, 'perTest');
    assert.equal(config.reporters.includes('dashboard'), false);
    assert.ok(config.mutate.every((file) => !/test|\.jsx$|preload|sidecar|main\.js/.test(file)));
    assert.ok(!config.tap.nodeArgs.includes('--test'));
  }
  assert.ok(profiles.renderer.tests.includes('src/renderer/components/config-transfer/*.test.js'));
});

test('入口拒绝未知范围和并发，不能把任意路径交给工具', () => {
  assert.deepEqual(parseArgs(['--profile', 'main', '--dry-run', '--strict', '--concurrency', '1']),
    { profile: 'main', dryRun: true, strict: true, concurrency: 1, help: false });
  for (const args of [['--profile', '../src'], ['--concurrency', '0'], ['--concurrency', '2.5'], ['--concurrency'], ['--mutate', '*']]) {
    assert.throws(() => parseArgs(args));
  }
});

test('严格分类不把超时、编译或运行错误视为 killed', () => {
  const statuses = ['Killed', 'Survived', 'NoCoverage', 'Timeout', 'CompileError', 'RuntimeError', 'Ignored', 'Pending', 'FutureStatus'];
  const result = classify({ files: { 'src/a.js': { mutants: statuses.map((status, id) => ({ id, status })) } } });
  assert.deepEqual(result.counts, { killed: 1, survived: 1, noCoverage: 1, timeout: 1,
    compileError: 1, runtimeError: 1, ignored: 1, pending: 1, unknown: 1, total: 9 });
  assert.equal(result.findings.length, 8);
  assert.ok(result.findings.every((finding) => finding.file === 'src/a.js' && finding.status !== 'Killed'));
});

test('strict 对无报告、dry-run、并发源码变化和未解决项均返回非零', () => {
  const complete = { runCompleted: true, sourceUnchanged: true, counts: { killed: 2, total: 2 } };
  assert.equal(strictExitCode(complete), 0);
  for (const value of [
    { ...complete, runCompleted: false }, { ...complete, sourceUnchanged: false },
    { ...complete, counts: { killed: 1, total: 2 } }, { ...complete, counts: { killed: 0, total: 0 } },
  ]) assert.equal(strictExitCode(value), 1);
});

test('报告缺任一目标文件即标记不完整', () => {
  assert.deepEqual(missingReportFiles({ files: { 'src/a.js': {} } }, ['src/a.js', 'src/b.js']), ['src/b.js']);
  assert.deepEqual(missingReportFiles(undefined, ['src/a.js']), ['src/a.js']);
});

test('取消时只终止拥有的Node进程树，等待退出后返回', { skip: process.platform === 'win32', timeout: 5000 }, async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'portunus-mutation-process-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const controller = new AbortController();
  let grandchildPid;
  const code = `const {spawn}=require('node:child_process'); const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'ignore'}); console.log(child.pid); setInterval(()=>{},1000);`;
  const running = runNode(['-e', code], root, process.env, path.join(root, 'process.log'), {
    signal: controller.signal, forwardOutput: false, stopGraceMs: 100,
    onOutput(bytes) { grandchildPid = Number(bytes.toString().trim()); controller.abort(new Error('fixture cancellation')); },
  });
  await assert.rejects(running, /fixture cancellation/);
  assert.ok(Number.isInteger(grandchildPid) && grandchildPid > 0);
  assert.throws(() => process.kill(grandchildPid, 0), { code: 'ESRCH' });
});

test('白名单快照包含未跟踪src文件，不复制根目录.env和用户数据', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'portunus-mutation-script-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const source = path.join(root, 'work');
  const stage = path.join(root, 'stage');
  await mkdir(path.join(source, 'src/nested'), { recursive: true });
  await writeFile(path.join(source, 'src/nested/new.js'), 'untracked source');
  await writeFile(path.join(source, '.env'), 'PRIVATE');
  const before = await sourceManifest(source, stage);
  assert.deepEqual(Object.keys(before), ['src/nested/new.js']);
  assert.equal(await readFile(path.join(stage, 'src/nested/new.js'), 'utf8'), 'untracked source');
  await assert.rejects(readFile(path.join(stage, '.env')), { code: 'ENOENT' });
  await writeFile(path.join(source, 'src/nested/new.js'), 'concurrent edit');
  assert.deepEqual(changedSources(before, await sourceManifest(source)), ['src/nested/new.js']);
  assert.equal(await readFile(path.join(source, 'src/nested/new.js'), 'utf8'), 'concurrent edit');
});

test('白名单拒绝指向src外部的符号链接', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'portunus-mutation-script-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(path.join(root, 'src'));
  await writeFile(path.join(root, 'private'), 'PRIVATE');
  await symlink('../private', path.join(root, 'src/escape'));
  await assert.rejects(sourceManifest(root), /不接受符号链接/);
});

test('缺少任一必须执行的测试模式时中止，不能静默缩小测试范围', async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), 'portunus-mutation-script-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await writeFile(path.join(root, 'present.test.js'), '');
  assert.deepEqual(expandTestFiles(['*.test.js', 'present.test.js'], root), ['present.test.js']);
  assert.throws(() => expandTestFiles(['*.test.js', 'missing.test.js'], root), /必须执行的测试未找到/);
});

test('HTML 摘要对突变替换文本转义，保留全部未杀死分类', () => {
  const html = summaryHTML({ profile: 'core', runId: 'fixture', mode: 'mutation', outcome: '完成',
    sourceUnchanged: true, scope: {}, counts: { timeout: 1 }, findings: [{
      file: 'src/a.js', status: 'Timeout', mutatorName: '<script>', replacement: '<img src=x>', location: { start: { line: 3 } },
    }] });
  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(!html.includes('<img src=x>'));
  assert.ok(html.includes('src/a.js:3'));
  assert.ok(html.includes('只有 killed 计为测试明确杀死'));
});
