import { createHash } from 'node:crypto';
import { globSync } from 'node:fs';
import { mkdir, readdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const mainSource = 'src/main/config-transfer.js';
const rendererSources = [
  'src/renderer/api/configTransfer.js',
  'src/renderer/components/config-transfer/controller.js',
  'src/renderer/components/config-transfer/model.js',
];
const mainTests = ['src/main/config-transfer.test.js'];
const rendererTests = [
  'src/renderer/api/configTransfer.test.js',
  'src/renderer/components/config-transfer/*.test.js',
];

export const profiles = Object.freeze({
  core: { mutate: [mainSource, ...rendererSources], tests: [...mainTests, ...rendererTests] },
  main: { mutate: [mainSource], tests: [...mainTests, rendererTests[0]] },
  renderer: { mutate: rendererSources, tests: rendererTests },
});

export function configForProfile(profile = 'core', env = process.env) {
  if (!Object.hasOwn(profiles, profile)) throw new Error(`未知 profile: ${profile}`);
  const selected = profiles[profile];
  return {
    testRunner: 'tap',
    plugins: ['@stryker-mutator/tap-runner'],
    mutate: selected.mutate,
    tap: {
      testFiles: selected.tests,
      // 不传 --test：每个文件已由 TAP runner 单独启动；hook 必须与被测代码同进程。
      nodeArgs: ['--test-reporter=tap', '-r', '{{hookFile}}', '{{testFile}}'],
      forceBail: true,
    },
    coverageAnalysis: 'perTest', // TAP runner 的粒度为测试文件。
    ignoreStatic: false,
    inPlace: false,
    tempDirName: '.stryker-tmp',
    cleanTempDir: 'always',
    concurrency: Number(env.PORTUNUS_MUTATION_CONCURRENCY || 2),
    timeoutMS: 2500,
    timeoutFactor: 2,
    dryRunTimeoutMinutes: 2,
    dryRunOnly: env.PORTUNUS_MUTATION_DRY_RUN === '1',
    reporters: ['clear-text', 'progress', 'json', 'html'],
    jsonReporter: { fileName: 'reports/mutation.json' },
    htmlReporter: { fileName: 'reports/mutation.html' },
    thresholds: { high: 100, low: 80, break: 0 },
    ignorePatterns: ['reports/**', '.stryker-tmp/**'],
    incremental: false,
  };
}

export function parseArgs(args) {
  const result = { profile: 'core', dryRun: false, strict: false, concurrency: 2, help: false };
  for (let index = 0; index < args.length; index++) {
    const arg = args[index];
    if (arg === '--dry-run') result.dryRun = true;
    else if (arg === '--strict') result.strict = true;
    else if (arg === '--help' || arg === '-h') result.help = true;
    else if (arg === '--profile') result.profile = args[++index];
    else if (arg === '--concurrency') result.concurrency = Number(args[++index]);
    else throw new Error(`未知参数: ${arg}`);
  }
  if (!Object.hasOwn(profiles, result.profile)) throw new Error(`未知 profile: ${result.profile}`);
  if (!Number.isInteger(result.concurrency) || result.concurrency < 1 || result.concurrency > 8) {
    throw new Error('--concurrency 必须是 1 到 8 的整数');
  }
  return result;
}

export function expandTestFiles(patterns, cwd) {
  return [...new Set(patterns.flatMap((pattern) => {
    const matches = globSync(pattern, { cwd });
    if (!matches.length) throw new Error(`必须执行的测试未找到: ${pattern}`);
    return matches;
  }))].sort();
}

// 只遍历白名单 src；不用 git ls-files，保留尚未跟踪的本轮代码和测试。
// 拒绝符号链接，防止越过白名单读取用户数据。
export async function sourceManifest(root, destination) {
  const manifest = {};
  async function visit(relative) {
    const entries = await readdir(path.join(root, relative), { withFileTypes: true });
    for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
      const file = path.posix.join(relative, entry.name);
      if (entry.isDirectory()) await visit(file);
      else if (entry.isFile()) {
        const bytes = await readFile(path.join(root, file));
        manifest[file] = createHash('sha256').update(bytes).digest('hex');
        if (destination) {
          const target = path.join(destination, file);
          await mkdir(path.dirname(target), { recursive: true });
          await writeFile(target, bytes);
        }
      } else throw new Error(`源码白名单不接受符号链接或特殊文件: ${file}`);
    }
  }
  await visit('src');
  return manifest;
}

export function changedSources(before, after) {
  return [...new Set([...Object.keys(before), ...Object.keys(after)])].sort()
    .filter((file) => before[file] !== after[file]);
}

export function classify(report) {
  const counts = { killed: 0, survived: 0, noCoverage: 0, timeout: 0,
    compileError: 0, runtimeError: 0, ignored: 0, pending: 0, unknown: 0, total: 0 };
  const keys = { Killed: 'killed', Survived: 'survived', NoCoverage: 'noCoverage',
    Timeout: 'timeout', CompileError: 'compileError', RuntimeError: 'runtimeError',
    Ignored: 'ignored', Pending: 'pending' };
  const findings = [];
  for (const [file, detail] of Object.entries(report?.files || {})) {
    for (const mutant of detail.mutants || []) {
      counts[keys[mutant.status] || 'unknown']++;
      counts.total++;
      if (mutant.status !== 'Killed') findings.push({ file, ...mutant });
    }
  }
  return { counts, findings };
}

export function missingReportFiles(report, expected) {
  return expected.filter((file) => !Object.hasOwn(report?.files || {}, file));
}

export function strictExitCode(summary) {
  return summary.runCompleted && summary.sourceUnchanged
    && summary.counts.total > 0 && summary.counts.killed === summary.counts.total ? 0 : 1;
}

const escapeHTML = (value) => String(value).replace(/[&<>"']/g, (char) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
})[char]);

export function summaryHTML(summary) {
  const rows = summary.findings.map((finding) => `<tr><td>${escapeHTML(finding.file)}:${finding.location?.start?.line ?? '?'}</td><td>${escapeHTML(finding.status)}</td><td>${escapeHTML(finding.mutatorName)}</td><td><pre>${escapeHTML(finding.replacement)}</pre></td></tr>`).join('');
  return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>JavaScript 突变审计</title>
<style>body{font:16px/1.6 system-ui;max-width:1280px;margin:40px auto;padding:0 24px;color:#18212b}table{border-collapse:collapse;width:100%}th,td{border:1px solid #ccd3da;padding:8px;text-align:left;vertical-align:top}pre{white-space:pre-wrap}code{overflow-wrap:anywhere}</style>
<h1>JavaScript 突变审计：${escapeHTML(summary.profile)}</h1><p>${escapeHTML(summary.outcome)}</p>
<p>运行：${escapeHTML(summary.runId)}；模式：${escapeHTML(summary.mode)}；源码未变：${summary.sourceUnchanged}</p>
<pre>${escapeHTML(JSON.stringify(summary.counts, null, 2))}</pre>
<p>只有 killed 计为测试明确杀死。timeout、compileError、runtimeError 单独统计。运行完成不代表全部杀死；dry-run 不执行活跃突变。</p>
<h2>实际范围</h2><pre>${escapeHTML(JSON.stringify(summary.scope, null, 2))}</pre>
<h2>未明确杀死的突变</h2><table><thead><tr><th>位置</th><th>分类</th><th>类型</th><th>替换</th></tr></thead><tbody>${rows}</tbody></table>
<p>每项的完整位置、原因和测试关联见 mutation.json；源码级交互报告见 <a href="mutation.html">mutation.html</a>。</p></html>`;
}
