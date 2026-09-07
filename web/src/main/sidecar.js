// 后端守护（sidecar）状态机——生命周期受控的 Go 后端，见 CONTEXT.md「后端守护 sidecar」词条。
//
// 打包模式（deps.binPath 有值）：spawn 随包二进制；开发模式（binPath=null）：
// 只轮询等外部后端就绪，不 spawn。
//
// 本模块 electron-free：一切环境（binPath / dataDir / port / host）经 createBackend(deps)
// 注入，真实依赖由 main.js 组装、测试注入 stub。spawn / ping / portFree 是三个 adapter，
// 生产走内置默认实现、测试覆盖。
//
// 接口语义：ready 恒 settle（进入 ready 或 failed 即 resolve、绝不 reject），
// 失败通过 state='failed' + error 表达——调用方 await ready 后无条件放行渲染层即可。

const { spawn: defaultSpawn } = require('child_process');
const http = require('http');
const net = require('net');
const fs = require('fs');
const path = require('path');

const DEFAULT_PING_RETRIES = 60;
const DEFAULT_PING_INTERVAL_MS = 500;
const DEFAULT_PORT_FREE_RETRIES = 30;
const DEFAULT_PORT_FREE_INTERVAL_MS = 100;

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** 默认 ping：GET /api/ping，200 即就绪（reject = 未就绪 / 连接失败）。 */
function defaultPing(port) {
  return new Promise((resolve, reject) => {
    http
      .get(`http://127.0.0.1:${port}/api/ping`, (res) => {
        res.resume();
        if (res.statusCode === 200) resolve();
        else reject(new Error(`/api/ping 返回 ${res.statusCode}`));
      })
      .on('error', reject);
  });
}

/** 默认端口释放探测：能 bind 成功即已释放。 */
function defaultPortFree(port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once('error', () => resolve(false));
    server.listen(port, '127.0.0.1', () => server.close(() => resolve(true)));
  });
}

/**
 * 后端守护状态机。
 * deps: {
 *   spawn?,              // (binPath, args, opts) => ChildProcess；默认 child_process.spawn
 *   ping?,               // () => Promise<void>（就绪 resolve、未就绪 reject）；默认探测 /api/ping
 *   portFree?,           // () => Promise<boolean>（已释放 true）；默认 net 探测
 *   binPath,             // string | null；null = 开发模式（不 spawn，只等外部后端）
 *   dataDir,             // 数据库目录（spawn 前确保存在）
 *   port,                // 后端监听端口
 *   host,                // () => string，每次 spawn 现算（restart 后随 networkMode 变）
 *   delayMs,             // 冷启动延迟，只作用 start、不影响 restart
 *   pingRetries?,        // 就绪轮询次数（默认 60）
 *   pingIntervalMs?,     // 就绪轮询间隔（默认 500）
 *   portFreeRetries?,    // 端口释放轮询次数（默认 30）
 *   portFreeIntervalMs?, // 端口释放轮询间隔（默认 100）
 * }
 */
function createBackend(deps) {
  const spawn = deps.spawn || defaultSpawn;
  const ping = deps.ping || (() => defaultPing(deps.port));
  const portFree = deps.portFree || (() => defaultPortFree(deps.port));
  const { binPath, dataDir, port } = deps;
  const host = deps.host || (() => '127.0.0.1');
  const pingRetries = deps.pingRetries ?? DEFAULT_PING_RETRIES;
  const pingIntervalMs = deps.pingIntervalMs ?? DEFAULT_PING_INTERVAL_MS;
  const portFreeRetries = deps.portFreeRetries ?? DEFAULT_PORT_FREE_RETRIES;
  const portFreeIntervalMs = deps.portFreeIntervalMs ?? DEFAULT_PORT_FREE_INTERVAL_MS;

  let state = 'stopped';
  let error = null;
  let child = null;
  let settleFirst;
  const ready = new Promise((resolve) => {
    settleFirst = resolve;
  });

  function setState(next, err = null) {
    state = next;
    error = err || null;
    if (next === 'ready' || next === 'failed') settleFirst();
  }

  function spawnBackend() {
    if (!binPath) return Promise.reject(new Error('后端二进制不存在'));
    if (!fs.existsSync(binPath)) return Promise.reject(new Error(`后端二进制缺失: ${binPath}`));
    fs.mkdirSync(dataDir, { recursive: true });

    const env = {
      ...process.env,
      PORTUNUS_SERVER_HOST: host(),
      PORTUNUS_SERVER_PORT: String(port),
      PORTUNUS_DATABASE_PATH: path.join(dataDir, 'portunus.db'),
      PORTUNUS_DATABASE_LOG_PATH: path.join(dataDir, 'portunus-log.db'),
    };

    child = spawn(binPath, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout?.on('data', (d) => console.log('[backend]', d.toString().trimEnd()));
    child.stderr?.on('data', (d) => console.error('[backend]', d.toString().trimEnd()));
    child.on('exit', (code) => {
      console.log(`[sidecar] 后端退出，code=${code}`);
      child = null;
    });
    return Promise.resolve();
  }

  async function waitForReady() {
    for (let i = 0; i < pingRetries; i++) {
      try {
        await ping();
        return;
      } catch {
        /* 未就绪，继续轮询 */
      }
      await sleep(pingIntervalMs);
    }
    throw new Error('服务端启动超时');
  }

  async function waitForPortFree() {
    for (let i = 0; i < portFreeRetries; i++) {
      if (await portFree()) return;
      await sleep(portFreeIntervalMs);
    }
    throw new Error('端口释放超时');
  }

  // 拉起（打包 spawn / 开发等外部）并一直等就绪：starting → ready | failed。
  async function boot() {
    setState('starting');
    if (binPath) {
      await spawnBackend();
    }
    await waitForReady();
    setState('ready');
  }

  async function start() {
    if (state === 'starting' || state === 'ready') return;
    const delay = Number(deps.delayMs || 0);
    if (delay > 0) {
      console.log(`[sidecar] 启动延迟 ${delay}ms`);
      await sleep(delay);
    }
    try {
      await boot();
    } catch (err) {
      setState('failed', err.message || String(err));
    }
  }

  function stop() {
    if (child) {
      child.kill();
      child = null;
    }
    setState('stopped');
  }

  async function restart() {
    if (child) {
      child.kill();
      child = null;
    }
    setState('stopped');
    try {
      await waitForPortFree();
      await boot();
    } catch (err) {
      setState('failed', err.message || String(err));
    }
  }

  return {
    get state() {
      return state;
    },
    get error() {
      return error;
    },
    ready,
    start,
    stop,
    restart,
  };
}

module.exports = { createBackend };
