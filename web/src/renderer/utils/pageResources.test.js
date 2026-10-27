import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { createPageResources, warmPageResources } from './pageResources.js';

function idleQueue() {
  const tasks = new Map();
  let id = 0;
  return {
    schedule(callback) { tasks.set(++id, callback); return id; },
    cancel(handle) { tasks.delete(handle); },
    size: () => tasks.size,
    async run() {
      const [handle, callback] = tasks.entries().next().value;
      tasks.delete(handle);
      callback();
      await setImmediate();
    },
  };
}

test('预加载与首次导航共享同一个未决加载，完成后可同步读取页面组件', async () => {
  let resolve;
  let calls = 0;
  const module = { default: () => null };
  const resources = createPageResources({ models: () => { calls++; return new Promise((done) => { resolve = done; }); } });
  const preload = resources.load('models');
  assert.equal(resources.get('models'), undefined);
  assert.equal(resources.load('models'), preload);
  await Promise.resolve();
  resolve(module);
  assert.equal(await preload, module);
  assert.equal(resources.get('models').default, module.default);
  assert.equal(await resources.load('models'), module);
  assert.equal(calls, 1);
});

test('预加载失败不留下失败快照，后续进入页面可重新加载', async () => {
  let calls = 0;
  const module = { default: () => null };
  const resources = createPageResources({ logs: async () => {
    if (++calls === 1) throw new Error('temporary module failure');
    return module;
  } });
  await assert.rejects(resources.load('logs'), /temporary module failure/);
  assert.equal(resources.get('logs'), undefined);
  assert.equal(await resources.load('logs'), module);
  assert.equal(calls, 2);
});

test('空闲预加载跳过首屏、逐页调度，单页失败不阻断后续页面', async () => {
  const calls = [];
  const resources = createPageResources(Object.fromEntries(['channels', 'models', 'logs'].map((id) => [id, async () => {
    calls.push(id);
    if (id === 'models') throw new Error('failed');
    return { default: () => null };
  }])));
  await resources.load('channels');
  const queue = idleQueue();
  warmPageResources(resources, ['channels', 'models', 'logs'], queue);
  assert.deepEqual(calls, ['channels']);
  assert.equal(queue.size(), 1);
  await queue.run();
  assert.deepEqual(calls, ['channels', 'models']);
  assert.equal(queue.size(), 1);
  await queue.run();
  assert.deepEqual(calls, ['channels', 'models', 'logs']);
  assert.equal(queue.size(), 0);
});

test('取消空闲任务及加载途中卸载都不会继续预加载其他页面', async () => {
  let resolve;
  const calls = [];
  const resources = createPageResources({
    first: () => { calls.push('first'); return new Promise((done) => { resolve = done; }); },
    second: async () => { calls.push('second'); return { default: () => null }; },
  });
  const queue = idleQueue();
  const cancelBeforeStart = warmPageResources(resources, ['first', 'second'], queue);
  cancelBeforeStart();
  assert.equal(queue.size(), 0);
  assert.deepEqual(calls, []);
  const cancelDuringLoad = warmPageResources(resources, ['first', 'second'], queue);
  await queue.run();
  cancelDuringLoad();
  resolve({ default: () => null });
  await setImmediate();
  assert.deepEqual(calls, ['first']);
  assert.equal(queue.size(), 0);
});
