#!/usr/bin/env node
// 启动竞态红线脚本：打包目录版 Portunus 启动，经 CDP 监听渲染进程的 console 与网络，
// 断言「启动窗口内后端 API 零连接失败」——这是 scripts 侧对启动竞态红 toast 的
// 自动化判据（与 request.js 拦截器 toast 同源的 [HTTP Error] console 信号）。
//
// 用法：
//   node scripts/startup-race-check.mjs                    # 全量构建（vite + electron-builder --dir）+ 3s 延迟钩子
//   node scripts/startup-race-check.mjs --skip-build       # 复用已有构建产物（改过 src/main/src/renderer 后必须全量）
//   node scripts/startup-race-check.mjs --delay-ms 0       # 自然启动冒烟（不注入延迟）
//   node scripts/startup-race-check.mjs --mode degraded    # 后端二进制缺失的降级路径：内容照常加载而非白屏
//
// 判据：
//   normal:   PASS = 窗口内 0 次 [HTTP Error] 且 >=1 个 /api 请求成功（apiOk=0 视为脚本失效而非通过）
//   degraded: PASS = 渲染层照常加载（Page.loadEventFired），即主进程 startServer 失败也放行
//
// 退出码：0 通过 / 1 判据失败 / 2 环境错误（端口占用、产物缺失、CDP 连不上）。

import { spawn, execSync } from 'node:child_process';
import { createServer } from 'node:net';
import { existsSync, mkdtempSync, renameSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';

// ─── 参数 ───
const argv = process.argv.slice(2);
const argOf = (name, def) => {
  const i = argv.indexOf(name);
  return i >= 0 && i + 1 < argv.length ? argv[i + 1] : def;
};
const skipBuild = argv.includes('--skip-build');
const delayMs = Number(argOf('--delay-ms', '3000'));
const windowMs = Number(argOf('--window-ms', '12000'));
const mode = argOf('--mode', 'normal'); // normal | degraded
const CDP_PORT = 9333;
const BACKEND_PORT = 13060;

const webRoot = path.resolve(import.meta.dirname, '..');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const fail2 = (msg) => {
  console.error(`[e2e] 环境错误：${msg}`);
  process.exit(2);
};

// ─── 定位打包产物（--dir 产物：dist-electron/mac*/Portunus.app）───
function findAppExe() {
  const outDir = path.join(webRoot, 'dist-electron');
  if (!existsSync(outDir)) return null;
  for (const entry of readdirSync(outDir)) {
    if (!entry.startsWith('mac')) continue;
    const exe = path.join(outDir, entry, 'Portunus.app', 'Contents', 'MacOS', 'Portunus');
    if (existsSync(exe)) return exe;
  }
  return null;
}
function findAppDir() {
  const exe = findAppExe();
  return exe ? path.resolve(exe, '../../../..') : null;
}

// ─── 端口预检：被占用（如本机跑着已安装版）会让旧实例应答 ping，产生假绿 ───
async function assertPortFree(port) {
  await new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once('error', (e) => reject(new Error(`端口 ${port} 被占用（先退出本机在跑的 Portunus / 调试实例）`)));
    srv.listen(port, '127.0.0.1', () => srv.close(() => resolve()));
  });
}

// ─── 构建 ───
function run(cmd) {
  console.log(`[e2e] $ ${cmd}`);
  execSync(cmd, { cwd: webRoot, stdio: 'inherit' });
}
async function build() {
  const exe = findAppExe();
  if (skipBuild && exe) return;
  if (!existsSync(path.join(webRoot, 'bin', 'mac', 'portunus'))) {
    run('node scripts/build-go.mjs');
  }
  run('pnpm run build:renderer');
  run('pnpm exec electron-builder --dir');
}

// ─── CDP 监听 ───
const stats = { httpErrorSeen: 0, apiOk: 0, apiFail: 0, pageLoaded: false };
let ws = null;
let msgId = 0;
const pending = new Map();
const urlByRequestId = new Map();
let reloadScheduled = false;

function send(method, params = {}, sessionId) {
  return new Promise((resolve) => {
    const id = ++msgId;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  });
}

// 附着后判断页面状态：complete 立即调度、否则等 loadEventFired——两者都只调度
// 一次 1s 后的 Page.reload。红线场景若 CDP 附着晚于首屏报错，reload 会在延迟钩子
// 窗口内再次触发请求爆发保证必红；绿线场景 reload 落在 about:blank 上无副作用。
function scheduleReload(sessionId) {
  if (reloadScheduled) return;
  reloadScheduled = true;
  setTimeout(() => send('Page.reload', {}, sessionId).catch(() => {}), 1000);
}

function onCdpMessage(raw) {
  let msg;
  try {
    msg = JSON.parse(raw);
  } catch {
    return;
  }
  if (msg.id !== undefined && pending.has(msg.id)) {
    pending.get(msg.id)(msg);
    pending.delete(msg.id);
    return;
  }
  const { method, params, sessionId } = msg;
  if (method === 'Target.attachedToTarget' && params.targetInfo.type === 'page') {
    const sid = params.sessionId;
    send('Runtime.enable', {}, sid);
    send('Page.enable', {}, sid);
    send('Network.enable', {}, sid);
    // 页面可能已加载完（附着晚于 load）：先查 readyState 再决定是否调度 reload
    send('Runtime.evaluate', { expression: 'document.readyState', returnByValue: true }, sid).then((res) => {
      if (res?.result?.result?.value === 'complete') scheduleReload(sid);
    });
    return;
  }
  if (method === 'Page.loadEventFired') {
    stats.pageLoaded = true;
    scheduleReload(sessionId);
  }
  if (method === 'Runtime.consoleAPICalled' && params.type === 'error') {
    const text = (params.args || []).map((a) => a.value ?? a.description ?? '').join(' ');
    if (text.includes('[HTTP Error]')) stats.httpErrorSeen++;
  }
  if (method === 'Network.requestWillBeSent') {
    urlByRequestId.set(`${sessionId}:${params.requestId}`, params.request.url);
  }
  if (method === 'Network.responseReceived') {
    const url = params.response?.url || '';
    if (/:13060\/api/.test(url) && params.response.status < 400) stats.apiOk++;
  }
  if (method === 'Network.loadingFailed') {
    const url = urlByRequestId.get(`${sessionId}:${params.requestId}`) || '';
    if (/:13060\/api/.test(url)) stats.apiFail++;
  }
}

async function attachCdp() {
  // Electron 的 CDP HTTP server 在 app ready 前就绪；轮询 /json/version 拿浏览器级 ws 地址
  const deadline = Date.now() + 15000;
  let version = null;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`http://127.0.0.1:${CDP_PORT}/json/version`);
      if (res.ok) {
        version = await res.json();
        break;
      }
    } catch {
      // 尚未就绪，继续轮询
    }
    await sleep(50);
  }
  if (!version) fail2(`CDP 端口 ${CDP_PORT} 连不上`);
  ws = new WebSocket(version.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', () => reject(new Error('CDP WebSocket 连接失败')), { once: true });
  });
  ws.addEventListener('message', (ev) => onCdpMessage(typeof ev.data === 'string' ? ev.data : String(ev.data)));
  // flatten autoAttach：窗口在 whenReady 后创建，附着几乎必然早于页面加载；
  // 后续对 page session 的命令都带 sessionId
  await send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: false, flatten: true });
}

// ─── 主流程 ───
let child = null;
let tmpHome = null;
let finished = false;
let cleanupDone = false;

function verdict() {
  console.log('[e2e] ── 统计 ──');
  console.log(`[e2e] [HTTP Error] console 次数: ${stats.httpErrorSeen}`);
  console.log(`[e2e] /api 成功响应: ${stats.apiOk}  失败请求: ${stats.apiFail}  页面加载完成: ${stats.pageLoaded}`);
  let pass;
  if (mode === 'degraded') {
    pass = stats.pageLoaded; // 后端缺失时渲染层仍照常加载 = 降级放行分支成立
  } else {
    pass = stats.httpErrorSeen === 0 && stats.apiOk >= 1;
  }
  console.log(`[e2e] ${pass ? 'PASS ✅' : 'FAIL ❌'}（mode=${mode}, delay=${delayMs}ms）`);
  return pass ? 0 : 1;
}

function finish() {
  if (finished) return;
  finished = true;
  const code = verdict();
  try {
    if (child && child.exitCode === null) child.kill('SIGTERM');
  } catch {
    // 进程已退出
  }
  setTimeout(() => {
    cleanup();
    process.exit(code);
  }, 500);
}

function cleanup() {
  if (cleanupDone) return;
  cleanupDone = true;
  try {
    ws?.close();
  } catch {
    // 未连接
  }
  if (mode === 'degraded') {
    const appDir = findAppDir();
    const bak = appDir && path.join(appDir, 'Contents', 'Resources', 'bin', 'portunus.bak');
    if (bak && existsSync(bak)) renameSync(bak, bak.replace(/\.bak$/, ''));
  }
  try {
    rmSync(tmpHome, { recursive: true, force: true });
  } catch {
    // 临时目录清理失败不影响结论
  }
}

async function main() {
  if (mode !== 'normal' && mode !== 'degraded') fail2(`未知 --mode: ${mode}`);
  await assertPortFree(BACKEND_PORT);
  await assertPortFree(CDP_PORT);

  await build();
  const exe = findAppExe();
  if (!exe) fail2('找不到 dist-electron 下的 Portunus.app（先不带 --skip-build 跑一次）');

  // HOME 指向临时目录：userData / 数据库隔离，不污染真实数据
  tmpHome = mkdtempSync(path.join(tmpdir(), 'portunus-e2e-'));

  if (mode === 'degraded') {
    const appBin = path.join(findAppDir(), 'Contents', 'Resources', 'bin', 'portunus');
    if (!existsSync(appBin)) fail2('降级模式需要打包产物内有 bin/portunus');
    renameSync(appBin, `${appBin}.bak`);
    console.log('[e2e] 降级模式：已临时移走内置后端二进制');
  }

  console.log(`[e2e] 启动 ${exe}（delay=${delayMs}ms, window=${windowMs}ms, mode=${mode}）`);
  child = spawn(exe, [`--remote-debugging-port=${CDP_PORT}`], {
    env: { ...process.env, HOME: tmpHome, PORTUNUS_SIDECAR_DELAY_MS: String(delayMs) },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout.on('data', (d) => process.stdout.write(`[app] ${d}`));
  child.stderr.on('data', (d) => process.stdout.write(`[app] ${d}`));
  child.on('exit', (code) => {
    console.log(`[e2e] app 进程退出 code=${code}`);
    // 窗口未结束就退出：提前裁决（正常路径 app 不应退出）
    finish();
  });

  await attachCdp();
  console.log('[e2e] CDP 已附着，开始监听');
  setTimeout(finish, windowMs);
}

main().catch((e) => {
  cleanup();
  fail2(e.message);
});
