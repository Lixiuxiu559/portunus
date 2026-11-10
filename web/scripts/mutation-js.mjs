import { randomUUID } from 'node:crypto';
import { copyFile, mkdir, mkdtemp, readFile, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { changedSources, classify, expandTestFiles, missingReportFiles, parseArgs, profiles, sourceManifest, strictExitCode, summaryHTML } from './mutation-js-lib.mjs';
import { runNode } from './mutation-js-process.mjs';

const webRoot = fileURLToPath(new URL('..', import.meta.url));

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log('用法：node scripts/mutation-js.mjs [--profile core|main|renderer] [--dry-run] [--strict] [--concurrency 1..8]');
    return;
  }
  const runId = `${new Date().toISOString().replace(/[:.]/g, '-')}-${randomUUID().slice(0, 8)}`;
  const reportDir = path.join(webRoot, 'reports/mutation/js', options.profile, runId);
  await mkdir(reportDir, { recursive: true });
  const stage = await realpath(await mkdtemp(path.join(tmpdir(), 'portunus-mutation-js-')));
  let before;
  let report;
  let failure;
  let baseline;
  let stryker;
  let testFiles = [];
  let toolVersion;
  const startedAt = new Date().toISOString();
  const abortController = new AbortController();
  let interruptedSignal;
  const interrupt = (signal) => {
    interruptedSignal ||= signal;
    abortController.abort(new Error(`收到 ${signal}，终止本次拥有的测试进程树`));
  };
  const onInterrupt = () => interrupt('SIGINT');
  const onTerminate = () => interrupt('SIGTERM');
  process.on('SIGINT', onInterrupt);
  process.on('SIGTERM', onTerminate);
  try {
    before = await sourceManifest(webRoot, stage);
    for (const file of profiles[options.profile].mutate) {
      if (!Object.hasOwn(before, file)) throw new Error(`必须突变的生产文件未找到: ${file}`);
    }
    await mkdir(path.join(stage, 'scripts'));
    for (const file of ['package.json', 'stryker.config.mjs', 'scripts/mutation-js-lib.mjs']) {
      await copyFile(path.join(webRoot, file), path.join(stage, file));
    }
    await symlink(path.join(webRoot, 'node_modules'), path.join(stage, 'node_modules'), 'dir');
    toolVersion = JSON.parse(await readFile(path.join(webRoot, 'node_modules/@stryker-mutator/core/package.json'), 'utf8')).version;
    testFiles = expandTestFiles(profiles[options.profile].tests, stage);
    const env = { ...process.env,
      PORTUNUS_MUTATION_STAGE: stage,
      PORTUNUS_MUTATION_PROFILE: options.profile,
      PORTUNUS_MUTATION_DRY_RUN: options.dryRun ? '1' : '0',
      PORTUNUS_MUTATION_CONCURRENCY: String(options.concurrency),
    };
    console.log(`隔离快照：${stage}\n报告目录：${reportDir}\n基线测试：${testFiles.join(', ')}`);
    baseline = await runNode(['--test', '--test-reporter=tap', ...testFiles], stage, env, path.join(reportDir, 'baseline.log'), { signal: abortController.signal });
    if (baseline.code !== 0) throw new Error('普通 Node 测试基线失败，未启动活跃突变');
    stryker = await runNode([path.join(webRoot, 'node_modules/@stryker-mutator/core/bin/stryker.js'), 'run', 'stryker.config.mjs'],
      stage, env, path.join(reportDir, 'stryker.log'), { signal: abortController.signal });
    for (const name of ['mutation.json', 'mutation.html']) {
      try { await copyFile(path.join(stage, 'reports', name), path.join(reportDir, name)); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
    try { report = JSON.parse(await readFile(path.join(reportDir, 'mutation.json'), 'utf8')); }
    catch (error) { if (!options.dryRun || error.code !== 'ENOENT') throw error; }
    if (stryker.code !== 0) throw new Error(`Stryker 失败，退出码 ${stryker.code}，信号 ${stryker.signal}`);
  } catch (error) {
    failure = error.stack || String(error);
  } finally {
    let after;
    try { after = await sourceManifest(webRoot); }
    catch (error) { failure = [failure, error.stack].filter(Boolean).join('\n'); }
    const changed = before && after ? changedSources(before, after) : [];
    const { counts, findings } = classify(report);
    const missingFiles = missingReportFiles(report, profiles[options.profile].mutate);
    const runCompleted = !options.dryRun && !failure && !missingFiles.length && counts.total > 0 && !counts.pending && !counts.unknown;
    const resultComplete = runCompleted && !counts.timeout && !counts.compileError && !counts.runtimeError && !counts.ignored;
    const summary = {
      runId, profile: options.profile, mode: options.dryRun ? 'dry-run' : 'mutation',
      tool: { name: 'StrykerJS + official TAP runner', version: toolVersion || null, node: process.version },
      startedAt, finishedAt: new Date().toISOString(),
      outcome: failure ? '工具或基线失败，结果不完整' : options.dryRun ? 'dry-run 完成，未执行活跃突变'
        : runCompleted ? resultComplete ? '突变运行完成；请逐类查看未明确杀死项'
          : '突变运行已结束；存在超时或错误，测试有效性结论不完整' : '突变结果不完整',
      runCompleted, resultComplete, dryRunVerified: options.dryRun && !failure && stryker?.code === 0,
      missingReportFiles: missingFiles, interruptedSignal: interruptedSignal || null,
      sourceUnchanged: Boolean(before && after && !changed.length), changedSources: changed,
      // 并发编辑只报告，不通过回滚覆盖用户或其他 agent 的修改。
      sourceCheckNote: changed.length ? '原源码在运行期间发生变化；工具只写临时快照，本脚本没有回滚任何文件' : '原源码 SHA-256 前后相同',
      counts, findings, baseline, stryker, failure: failure || null,
      scope: { mutate: profiles[options.profile].mutate, testFiles,
        coverageGranularity: 'test-file', concurrency: options.concurrency,
        excluded: ['bootstrap / main.js / preload.js / sidecar.js 旧代码', 'JSX 界面组件'],
        note: '部分测试导入旧代码，但只对 mutate 列出的生产文件生成突变；使用全部默认生产 mutator，包含静态突变，无 dashboard 上传' },
    };
    try {
      await writeFile(path.join(reportDir, 'source-before.json'), JSON.stringify(before || {}, null, 2));
      await writeFile(path.join(reportDir, 'source-after.json'), JSON.stringify(after || {}, null, 2));
      await writeFile(path.join(reportDir, 'summary.json'), JSON.stringify(summary, null, 2));
      await writeFile(path.join(reportDir, 'summary.html'), summaryHTML(summary));
    } finally {
      // 报告写入失败时也只清理由本次 mkdtemp 创建的目录。
      try { await rm(stage, { recursive: true, force: true }); }
      finally {
        process.removeListener('SIGINT', onInterrupt);
        process.removeListener('SIGTERM', onTerminate);
      }
    }
    console.log(`${summary.outcome}\n${JSON.stringify(counts)}\n摘要：${path.join(reportDir, 'summary.json')}`);
    process.exitCode = interruptedSignal ? interruptedSignal === 'SIGINT' ? 130 : 143
      : failure || (!options.dryRun && !runCompleted) ? 1
      : options.strict ? strictExitCode(summary) : 0;
  }
}

main().catch((error) => { console.error(error.stack || String(error)); process.exitCode = 1; });
