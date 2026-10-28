// 后端守护（sidecar）状态机——生命周期受控的 Go 后端，见 docs/wiki/modules/desktop/README.md。
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
const { randomUUID } = require('node:crypto');

const DEFAULT_PING_RETRIES = 60;
const DEFAULT_PING_INTERVAL_MS = 500;
const DEFAULT_PORT_FREE_RETRIES = 30;
const DEFAULT_PORT_FREE_INTERVAL_MS = 100;

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** 默认 ping：GET /api/ping；要求 200 与当前实例标识一致。 */
function defaultPing(port, instanceId) {
  return new Promise((resolve, reject) => {
    http
      .get(`http://127.0.0.1:${port}/api/ping`, (res) => {
        res.resume();
        if (res.statusCode === 200 && (!instanceId || res.headers['x-portunus-instance'] === instanceId)) resolve();
        else reject(new Error(`/api/ping 返回 ${res.statusCode}`));
      })
      .on('error', reject)
      .setTimeout(2000, function () { this.destroy(new Error('健康检查超时')); });
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
 *   cwd?,                // 后端工作目录（开发版应指向独立 userData）
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
  const ping = deps.ping || (() => defaultPing(deps.port, instanceId));
  let instanceId = null;
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
  let generation = 0;
  let settleFirst;
  const ready = new Promise((resolve) => {
    settleFirst = resolve;
  });

  function setState(next, err = null) {
    state = next;
    error = err || null;
    if (next === 'ready' || next === 'failed') settleFirst();
  }

  function assertCurrent(run, expectedChild) {
    if (run !== generation) throw new Error('后端启动已取消');
    if (binPath && (!expectedChild || child !== expectedChild ||
        expectedChild.exitCode != null || expectedChild.signalCode != null || expectedChild.killed)) {
      throw new Error(error || '后端已退出');
    }
  }

  async function spawnBackend(run) {
    if (!binPath) throw new Error('后端二进制不存在');
    if (!fs.existsSync(binPath)) throw new Error(`后端二进制缺失: ${binPath}`);
    // 不接管已有服务，也不根据它的 /api/ping 将自己标为 ready。
    if (!await portFree()) throw new Error(`后端端口 ${port} 已被占用`);
    if (run !== generation) throw new Error('后端启动已取消');
    fs.mkdirSync(dataDir, { recursive: true });

    instanceId = randomUUID();
    const env = {
      ...process.env,
      PORTUNUS_INSTANCE_ID: instanceId,
      ...(deps.databaseType ? { PORTUNUS_DATABASE_TYPE: deps.databaseType } : {}),
      PORTUNUS_SERVER_HOST: host(),
      PORTUNUS_SERVER_PORT: String(port),
      PORTUNUS_DATABASE_PATH: path.join(dataDir, 'portunus.db'),
      PORTUNUS_DATABASE_LOG_PATH: path.join(dataDir, 'portunus-log.db'),
    };

    const ownedChild = spawn(binPath, [], {
      env, stdio: ['ignore', 'pipe', 'pipe'],
      ...(deps.cwd ? { cwd: deps.cwd } : {}),
    });
    child = ownedChild;
    ownedChild.stdout?.on('data', (d) => console.log('[backend]', d.toString().trimEnd()));
    ownedChild.stderr?.on('data', (d) => console.error('[backend]', d.toString().trimEnd()));
    const failChild = (message) => {
      // 旧进程延迟退出不能清空 restart 创建的新进程。
      if (child !== ownedChild || run !== generation) return;
      child = null;
      setState('failed', message);
    };
    ownedChild.on('exit', (code) => {
      console.log(`[sidecar] 后端退出，code=${code}`);
      failChild(`后端已退出，code=${code}`);
    });
    ownedChild.on('error', (err) => {
      const isCurrent = child === ownedChild && run === generation;
      failChild(`后端进程错误: ${err.message || String(err)}`);
      if (isCurrent) ownedChild.kill();
    });
    return ownedChild;
  }

  async function waitForReady(run, expectedChild) {
    for (let i = 0; i < pingRetries; i++) {
      assertCurrent(run, expectedChild);
      let reachable = false;
      try {
        await ping();
        reachable = true;
      } catch {
        /* 未就绪，继续轮询 */
      }
      assertCurrent(run, expectedChild);
      if (reachable) return;
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

  // 只操作本次创建的进程；清空引用后再 kill，避免同步 exit 改写状态。
  function stopChild() {
    const ownedChild = child;
    child = null;
    if (ownedChild) ownedChild.kill();
  }

  async function boot(run) {
    const expectedChild = binPath ? await spawnBackend(run) : null;
    await waitForReady(run, expectedChild);
    assertCurrent(run, expectedChild);
    setState('ready');
  }

  async function start() {
    if (state === 'starting' || state === 'ready') return;
    const run = ++generation;
    stopChild();
    setState('starting');
    try {
      const delay = Number(deps.delayMs || 0);
      if (delay > 0) {
        console.log(`[sidecar] 启动延迟 ${delay}ms`);
        await sleep(delay);
      }
      if (run !== generation) return;
      await boot(run);
    } catch (err) {
      if (run !== generation) return;
      stopChild();
      setState('failed', err.message || String(err));
    }
  }

  function stop() {
    generation++;
    stopChild();
    setState('stopped');
  }

  // macOS 更新替换前等待自己启动的后端退出，避免新版立即启动时端口仍占用。
  function stopAndWait() {
    const ownedChild = child;
    if (!ownedChild || ownedChild.exitCode != null || ownedChild.signalCode != null) {
      stop();
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      const finish = () => {
        clearTimeout(forceTimer);
        clearTimeout(deadlineTimer);
        resolve();
      };
      const forceTimer = setTimeout(() => ownedChild.kill('SIGKILL'), 6000);
      const deadlineTimer = setTimeout(finish, 8000);
      ownedChild.once('exit', finish);
      stop();
    });
  }

  async function restart() {
    const run = ++generation;
    stopChild();
    setState('starting');
    try {
      await waitForPortFree();
      if (run !== generation) return;
      await boot(run);
    } catch (err) {
      if (run !== generation) return;
      stopChild();
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
    stopAndWait,
    restart,
  };
}

module.exports = { createBackend };
