const { test } = require('node:test');
const assert = require('node:assert/strict');
const os = require('os');
const { createBackend } = require('./sidecar.js');

// 后端守护状态机离线单测：deps 注入 stub spawn / ping / portFree，
// 不碰真实二进制与端口。见 CONTEXT.md「后端守护 sidecar」词条。

function fakeChild() {
  const listeners = {};
  return {
    stdout: { on: () => {} },
    stderr: { on: () => {} },
    killed: false,
    on(ev, cb) {
      listeners[ev] = cb;
    },
    kill() {
      this.killed = true;
    },
  };
}

// 可编程 ping：按脚本顺序返回成功/失败，越界用最后一项。
function scriptedPing(script) {
  let i = 0;
  return () => {
    const ok = script[Math.min(i, script.length - 1)];
    i++;
    return ok ? Promise.resolve() : Promise.reject(new Error('未就绪'));
  };
}

function makeBackend(overrides = {}) {
  const spawnCalls = [];
  const children = [];
  const backend = createBackend({
    binPath: process.execPath, // 真实存在，满足 existsSync；spawn 已被 stub 不会真执行
    dataDir: os.tmpdir(), // mkdirSync 已存在目录为 no-op
    port: 13060,
    host: () => '127.0.0.1',
    pingRetries: 3,
    pingIntervalMs: 1,
    portFreeRetries: 3,
    portFreeIntervalMs: 1,
    spawn: (bin, args, opts) => {
      spawnCalls.push({ bin, opts });
      const c = fakeChild();
      children.push(c);
      return c;
    },
    ping: scriptedPing([true]),
    portFree: () => Promise.resolve(true),
    ...overrides,
  });
  return { backend, spawnCalls, children };
}

test('start：spawn 后 ping 成功 → ready，ready settle', async () => {
  const { backend, spawnCalls } = makeBackend();
  await backend.start();
  assert.equal(backend.state, 'ready');
  assert.equal(backend.error, null);
  assert.equal(spawnCalls.length, 1);
  await backend.ready; // 不 reject
});

test('binPath 不存在 → failed，error 暴露、ready 仍 settle', async () => {
  const { backend, spawnCalls } = makeBackend({ binPath: '/nonexistent/portunus' });
  await backend.start();
  assert.equal(backend.state, 'failed');
  assert.match(backend.error, /后端二进制缺失/);
  assert.equal(spawnCalls.length, 0);
  await backend.ready; // 失败也 resolve，不 reject
});

test('ping 先失败后成功 → 轮询重试后 ready', async () => {
  const { backend } = makeBackend({ ping: scriptedPing([false, false, true]) });
  await backend.start();
  assert.equal(backend.state, 'ready');
});

test('ping 一直失败 → failed（服务端启动超时）', async () => {
  const { backend } = makeBackend({ ping: () => Promise.reject(new Error('未就绪')) });
  await backend.start();
  assert.equal(backend.state, 'failed');
  assert.equal(backend.error, '服务端启动超时');
});

test('binPath=null（开发模式）→ 不 spawn，只等外部后端', async () => {
  const { backend, spawnCalls } = makeBackend({ binPath: null });
  await backend.start();
  assert.equal(backend.state, 'ready');
  assert.equal(spawnCalls.length, 0);
});

test('spawn 注入后端环境变量（host/port/db 路径）', async () => {
  const { backend, spawnCalls } = makeBackend({ host: () => '0.0.0.0' });
  await backend.start();
  const env = spawnCalls[0].opts.env;
  assert.equal(env.PORTUNUS_SERVER_HOST, '0.0.0.0');
  assert.equal(env.PORTUNUS_SERVER_PORT, '13060');
  assert.ok(env.PORTUNUS_DATABASE_PATH.endsWith('portunus.db'));
  assert.ok(env.PORTUNUS_DATABASE_LOG_PATH.endsWith('portunus-log.db'));
});

test('restart：stop 后等端口释放再 spawn，回 ready', async () => {
  const portFreeCalls = [];
  const { backend, spawnCalls } = makeBackend({
    ping: scriptedPing([true, true]),
    portFree: () => {
      portFreeCalls.push(1);
      return Promise.resolve(true);
    },
  });
  await backend.start();
  assert.equal(spawnCalls.length, 1);
  await backend.restart();
  assert.equal(backend.state, 'ready');
  assert.equal(spawnCalls.length, 2);
  assert.ok(portFreeCalls.length >= 1);
});

test('stop：杀掉子进程，state → stopped', async () => {
  const { backend, children } = makeBackend();
  await backend.start();
  backend.stop();
  assert.equal(backend.state, 'stopped');
  assert.ok(children[0].killed);
});

test('start 幂等：ready 后再次 start 不重复 spawn', async () => {
  const { backend, spawnCalls } = makeBackend();
  await backend.start();
  await backend.start();
  assert.equal(spawnCalls.length, 1);
});
