import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { EventEmitter } from 'node:events';
import { fileURLToPath } from 'node:url';
import { createConfigTransferBridge, ConfigTransferError, configTransfer } from './configTransfer.js';

test('窄IPC适配原样传密码，系统取消是成功数据', async () => {
  let input;
  const bridge = createConfigTransferBridge(() => ({ exportConfig: async (value) => { input = value; return { ok: true, data: { cancelled: true } }; } }));
  assert.deepEqual(await bridge.exportConfig({ password: '  é  ' }), { cancelled: true });
  assert.deepEqual(input, { password: '  é  ' });
});

test('结构化错误保留操作和新计划，未安装桥时不退化为HTTP通用请求', async () => {
  const bridge = createConfigTransferBridge(() => ({ commitImport: async () => ({ ok: false, error: {
    code: 'PREVIEW_CHANGED', stage: 'plan', message: '请再次确认', retryable: false, operation_id: 'old', field: 'groups[0].name', replacement_preview: { preview_id: 'new' } } }) }));
  await assert.rejects(bridge.commitImport({}), (error) => error instanceof ConfigTransferError && error.code === 'PREVIEW_CHANGED'
    && error.field === 'groups[0].name' && error.replacement_preview.preview_id === 'new');
  await assert.rejects(createConfigTransferBridge(() => undefined).summary(), { code: 'TRANSFER_UNAVAILABLE' });
});

test('全部十个固定方法动态获取真实window桥，保留参数数量、顺序与this绑定', async (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window');
  t.after(() => previous ? Object.defineProperty(globalThis, 'window', previous) : delete globalThis.window);
  const methods = ['summary', 'exportConfig', 'chooseImportFile', 'previewImport', 'commitImport',
    'operationResult', 'recoverOperation', 'recoveryList', 'acknowledge', 'discard'];
  assert.deepEqual(Object.keys(configTransfer), methods);
  for (const method of methods) {
    const calls = [];
    const api = { [method](...args) { assert.equal(this, api); calls.push(args); return { ok: true, data: { method } }; } };
    globalThis.window = { api: { configTransfer: api } };
    assert.deepEqual(await configTransfer[method](), { method });
    const input = { operation_id: 'synthetic', password: '  raw  ' };
    assert.deepEqual(await configTransfer[method](input, undefined), { method });
    assert.deepEqual(calls, [[], [input, undefined]]);
  }
  globalThis.window = {};
  await assert.rejects(configTransfer.summary(), { code: 'TRANSFER_UNAVAILABLE' });
  delete globalThis.window;
  await assert.rejects(configTransfer.summary(), { code: 'TRANSFER_UNAVAILABLE' });
});

test('窄桥缺失方法、非函数和畸形响应都返回结构化默认错误', async () => {
  const expected = { name: 'ConfigTransferError', code: 'TRANSFER_UNAVAILABLE', stage: 'input',
    message: '配置迁移暂时不可用，请重试', retryable: false };
  for (const getApi of [() => undefined, () => ({}), () => ({ summary: true }),
    ...[undefined, null, {}, { ok: false }, { ok: 'true' }, { ok: 1 }].map((response) => () => ({ summary: async () => response }))]) {
    await assert.rejects(createConfigTransferBridge(getApi).summary(), expected);
  }
  const noData = createConfigTransferBridge(() => ({ summary: async () => ({ ok: true }) }));
  assert.equal(await noData.summary(), undefined);
});

test('IPC抛错不重试，不透传底层敏感消息，提交保留commit阶段', async () => {
  for (const method of ['commitImport', 'operationResult', 'exportConfig']) {
    let calls = 0;
    const bridge = createConfigTransferBridge(() => ({ [method]: async () => { calls++; throw new Error('/private/secret'); } }));
    await assert.rejects(bridge[method]({}), { name: 'ConfigTransferError', code: 'TRANSFER_UNAVAILABLE',
      stage: method === 'commitImport' ? 'commit' : 'input', message: '配置迁移暂时不可用，请重试', retryable: false });
    assert.equal(calls, 1);
  }
});

test('结构化错误完整保留展示字段，retryable归一为布尔值并有明确fallback', async () => {
  const payload = { code: 'DB_BUSY', stage: 'recovery', message: '请核实原操作', retryable: true,
    operation_id: 'same-operation', replacement_preview: { preview_id: 'new' }, field: 'groups[0].name' };
  const bridge = createConfigTransferBridge(() => ({ operationResult: async () => ({ ok: false, error: payload }) }));
  await assert.rejects(bridge.operationResult({}), (error) => {
    assert.ok(error instanceof ConfigTransferError);
    for (const [key, value] of Object.entries(payload)) assert.deepEqual(error[key], value);
    return true;
  });
  for (const [input, expected] of [[{}, false], [{ retryable: 0 }, false], [{ retryable: 'yes' }, true]]) {
    const error = new ConfigTransferError(input);
    assert.equal(error.retryable, expected);
    assert.equal(typeof error.retryable, 'boolean');
    assert.equal(error.stage, 'input');
    assert.equal(error.code, 'TRANSFER_UNAVAILABLE');
    assert.equal(error.message, '配置迁移暂时不可用，请重试');
    assert.equal(error.name, 'ConfigTransferError');
  }
});

test('真实renderer适配器、preload与Main串联时保留无参调用的参数数量', async () => {
  const preload = fileURLToPath(new URL('../../main/preload.js', import.meta.url));
  const require = createRequire(preload);
  const { createConfigTransfer } = require('./config-transfer.js');
  const webContents = new EventEmitter();
  webContents.mainFrame = { url: 'http://localhost:5173/#/settings' };
  const window = { webContents, isDestroyed: () => false };
  const handlers = new Map();
  const instanceId = 'synthetic-contract-instance';
  let api;
  let reads = 0;
  let selections = 0;
  const main = createConfigTransfer({
    getWindow: () => window, useDevServer: true, profile: 'development',
    backend: { access: { ready: true, instanceId, generation: 1 } },
    transport: async () => {
      reads++;
      return { status: 200, headers: { 'x-portunus-instance': instanceId }, body: Buffer.from(JSON.stringify({
        counts: { channels: 1, models: 2, groups: 3 }, can_export: true, ready: true, format_version: 1, limits: {},
      })) };
    },
    dialog: { showOpenDialog: async () => { selections++; return { canceled: true }; } },
  });
  main.register({ handle: (channel, callback) => handlers.set(channel, callback) });
  try {
    vm.runInNewContext(fs.readFileSync(preload, 'utf8'), {
      process: { argv: ['--portunus-profile=development'], env: {}, platform: 'darwin' },
      require(id) {
        if (id !== 'electron') return require(id);
        return {
          contextBridge: { exposeInMainWorld(_name, value) { api = value; } },
          ipcRenderer: {
            invoke: (channel, ...args) => handlers.get(channel)({ sender: webContents, senderFrame: webContents.mainFrame }, ...args),
            on() {},
          },
        };
      },
    }, { filename: preload });
    const bridge = createConfigTransferBridge(() => api.configTransfer);
    assert.deepEqual((await bridge.summary()).counts, { channels: 1, models: 2, groups: 3 });
    assert.equal((await bridge.chooseImportFile()).cancelled, true);
    assert.equal(reads, 1);
    assert.equal(selections, 1);
    // 明确传入非法额外参数时仍由Main拒绝，不能用丢弃参数来掩盖契约错误。
    await assert.rejects(bridge.summary(undefined), { code: 'INVALID_INPUT' });
    await assert.rejects(bridge.chooseImportFile({ path: '/untrusted' }), { code: 'INVALID_INPUT' });
    assert.equal(selections, 1);
  } finally { main.dispose(); }
});
