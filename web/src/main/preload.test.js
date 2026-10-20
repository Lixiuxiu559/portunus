const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const { EventEmitter } = require('node:events');

test('preload 只暴露网关应用能力，不再暴露客户端文件读写桥', () => {
  const exposed = {};
  const invoked = [];
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'preload.js'), 'utf8'), {
    process: { argv: ['--portunus-profile=production'], env: { NODE_ENV: 'production' }, platform: 'darwin' },
    require(id) {
      if (id === 'electron') return {
        contextBridge: { exposeInMainWorld(name, value) { exposed[name] = value; } },
        ipcRenderer: { invoke(channel) { invoked.push(channel); return Promise.resolve(); }, on() {} },
      };
      return require(id);
    },
  });
  assert.deepEqual(Object.keys(exposed), ['api']);
  assert.equal(exposed.api.getServerUrl(), 'http://127.0.0.1:13060');
  for (const [name, fn] of Object.entries(exposed.api)) {
    if (typeof fn === 'function' && name !== 'onUpdateEvent') fn();
  }
  assert.equal(invoked.some((channel) => channel.startsWith('client-config:')), false);
});

test('更新状态桥可读取快照，监听可移除且拒绝白名单外事件', async () => {
  const ipc = new EventEmitter();
  const invoked = [];
  ipc.invoke = async (channel) => { invoked.push(channel); return { revision: 2, downloaded: true }; };
  let api;
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'preload.js'), 'utf8'), {
    process: { env: {}, platform: 'win32' },
    require(id) {
      if (id === 'electron') return {
        contextBridge: { exposeInMainWorld(name, value) { api = value; } },
        ipcRenderer: ipc,
      };
      return require(id);
    },
  });
  assert.equal((await api.getUpdateState()).downloaded, true);
  assert.deepEqual(invoked, ['update:get-state']);
  const received = [];
  const off = api.onUpdateEvent('update:state', (state) => received.push(state));
  ipc.emit('update:state', {}, { revision: 3 });
  off();
  ipc.emit('update:state', {}, { revision: 4 });
  assert.deepEqual(received, [{ revision: 3 }]);
  api.onUpdateEvent('arbitrary:channel', () => {});
  assert.equal(ipc.listenerCount('arbitrary:channel'), 0);
  assert.equal(ipc.listenerCount('update:state'), 0);
});
