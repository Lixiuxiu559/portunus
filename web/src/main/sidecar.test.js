const { test } = require('node:test');
const assert = require('node:assert/strict');
const os = require('os');
const { createBackend } = require('./sidecar.js');

// 后端守护状态机离线单测：deps 注入 stub spawn / ping / portFree，
// 不碰真实二进制与端口。见 CONTEXT.md「后端守护 sidecar」词条。

function fakeChild() {
  const child = new (require('node:events').EventEmitter)();
  child.stdout = { on: () => {} };
  child.stderr = { on: () => {} };
  child.killed = false;
  child.kill = function () { this.killed = true; };
  return child;
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

test('端口被占用时不 spawn，也不 ping 已有服务', async () => {
  let pingCount = 0;
  const { backend, spawnCalls } = makeBackend({
    portFree: async () => false,
    ping: async () => { pingCount++; },
  });
  await backend.start();
  assert.equal(backend.state, 'failed');
  assert.match(backend.error, /端口.*已被占用/);
  assert.equal(spawnCalls.length, 0);
  assert.equal(pingCount, 0);
});

test('ping 响应期间自己进程提前退出，不能接受其他服务的成功响应', async () => {
  let child;
  const { backend } = makeBackend({
    spawn: () => (child = fakeChild()),
    ping: async () => { child.emit('exit', 1); },
  });
  await backend.start();
  assert.equal(backend.state, 'failed');
  assert.match(backend.error, /后端已退出/);
});

test('ready 后子进程退出也更新为 failed', async () => {
  const { backend, children } = makeBackend();
  await backend.start();
  children[0].emit('exit', 1);
  assert.equal(backend.state, 'failed');
});

test('异步 spawn error 有监听并转为 failed', async () => {
  const { backend } = makeBackend({
    spawn: () => {
      const child = fakeChild();
      queueMicrotask(() => child.emit('error', new Error('EACCES')));
      return child;
    },
  });
  await backend.start();
  assert.equal(backend.state, 'failed');
  assert.match(backend.error, /EACCES/);
});

test('旧进程延迟退出不会清空新进程，stop 只结束新进程', async () => {
  const { backend, children } = makeBackend();
  await backend.start();
  await backend.restart();
  children[0].emit('exit', 0);
  assert.equal(backend.state, 'ready');
  assert.equal(children[1].killed, false);
  backend.stop();
  assert.equal(children[1].killed, true);
});

test('cwd 传给后端，隔离工作目录中的配置文件', async () => {
  const { backend, spawnCalls } = makeBackend({ cwd: '/tmp/portunus-dev-test' });
  await backend.start();
  assert.equal(spawnCalls[0].opts.cwd, '/tmp/portunus-dev-test');
});

test('启动轮询期间 stop，不会被晚到的 ping 恢复为 ready', async () => {
  let completePing;
  const { backend, children } = makeBackend({
    ping: () => new Promise((resolve) => { completePing = resolve; }),
  });
  const starting = backend.start();
  while (!completePing) await new Promise((resolve) => setImmediate(resolve));
  backend.stop();
  completePing();
  await starting;
  assert.equal(backend.state, 'stopped');
  assert.equal(children[0].killed, true);
});

test('每次启动和重启都使用独立实例标识，Dev 显式固定 SQLite', async () => {
  const { backend, spawnCalls } = makeBackend({ databaseType: 'sqlite' });
  await backend.start();
  const first = spawnCalls[0].opts.env.PORTUNUS_INSTANCE_ID;
  assert.ok(first);
  assert.equal(spawnCalls[0].opts.env.PORTUNUS_DATABASE_TYPE, 'sqlite');
  await backend.restart();
  assert.notEqual(spawnCalls[1].opts.env.PORTUNUS_INSTANCE_ID, first);
  backend.stop();
});

test('真实健康检查适配器拒绝其它实例的 200，仅接受自己的响应头', async () => {
  const fs = require('node:fs');
  const vm = require('node:vm');
  const path = require('node:path');
  let create;
  let launchedId;
  let requests = 0;
  const source = fs.readFileSync(path.join(__dirname, 'sidecar.js'), 'utf8');
  vm.runInNewContext(source, {
    console, process, setTimeout,
    module: { set exports(value) { create = value.createBackend; } },
    require(id) {
      if (id === 'http') return {
        get(url, respond) {
          requests++;
          respond({ statusCode: 200, headers: { 'x-portunus-instance': requests === 1 ? 'other-process' : launchedId }, resume() {} });
          return { on() { return this; }, setTimeout() { return this; } };
        },
      };
      return require(id);
    },
  });
  const backend = create({
    binPath: process.execPath, dataDir: os.tmpdir(), port: 13061,
    pingRetries: 3, pingIntervalMs: 1, portFree: async () => true,
    spawn(bin, args, options) { launchedId = options.env.PORTUNUS_INSTANCE_ID; return fakeChild(); },
  });
  await backend.start();
  assert.equal(backend.state, 'ready');
  assert.equal(requests, 2);
  backend.stop();
});
