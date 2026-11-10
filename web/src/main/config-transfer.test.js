const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { EventEmitter } = require('node:events');
const { pathToFileURL } = require('node:url');
const { createConfigTransfer, CHANNELS, LIMITS } = require('./config-transfer');
const { createBackend } = require('./sidecar');

const OP = '11111111-1111-4111-8111-111111111111';
const PREVIEW = 'a'.repeat(32);
const FILE_HASH = 'f'.repeat(64);
const PLAN_HASH = 'e'.repeat(64);
const preview = (overrides = {}) => ({ preview_id: PREVIEW, operation_id: OP, file_sha256: FILE_HASH, plan_sha256: PLAN_HASH, ...overrides });
const counts = { channels: 1, models: 2, groups: 3 };
function fixture(t, overrides = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'portunus-transfer-test-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const selected = path.join(root, 'synthetic.portunus');
  fs.writeFileSync(selected, 'encrypted-fixture');
  const rendererFile = path.join(root, 'renderer.html');
  const wc = new EventEmitter();
  wc.mainFrame = { url: pathToFileURL(rendererFile).href };
  const win = { webContents: wc, isDestroyed: () => false };
  const event = { sender: wc, senderFrame: wc.mainFrame };
  let access = { ready: true, instanceId: 'owned-instance', generation: 1 };
  const backend = { get access() { return access; } };
  const calls = [];
  const reply = (data, options = {}) => ({ status: 200, headers: { 'x-portunus-instance': access.instanceId }, body: Buffer.from(JSON.stringify(data)), ...options });
  const handlers = {};
  const dialogs = { save: 0, open: 0 };
  const transfer = createConfigTransfer({
    getWindow: () => win, rendererFile, profile: 'development', backend,
    dialog: {
      showOpenDialog: async () => { dialogs.open++; return { filePaths: [selected] }; },
      showSaveDialog: async () => { dialogs.save++; return { filePath: selected }; },
    },
    getVersion: () => 'test-app',
    transport: async (options) => {
      calls.push({ ...options, bodySnapshot: Buffer.concat(options.parts) });
      if (overrides.respond) return overrides.respond(options, reply, () => access, (next) => { access = next; });
      if (options.url.endsWith('/previews') && options.method === 'POST') return reply(preview());
      if (options.url.endsWith('/summary')) return reply({ instance_id: 'private', counts, can_export: true, limits: {}, ready: true, format_version: 1 });
      return reply({ operation_id: OP, state: 'committed', counts });
    },
    ...Object.fromEntries(Object.entries(overrides).filter(([key]) => key !== 'respond')),
  });
  transfer.register({ handle: (channel, handler) => { handlers[channel] = handler; } });
  transfer.bindWindow(win);
  t.after(() => transfer.dispose());
  return { root, selected, rendererFile, win, wc, event, calls, dialogs, reply,
    setAccess: (next) => { access = next; }, transfer,
    invoke: (name, ...args) => handlers[CHANNELS[name]](event, ...args),
    from: (name, ev, ...args) => handlers[CHANNELS[name]](ev, ...args),
    async choosePreview() {
      const chosen = await handlers[CHANNELS.chooseImportFile](event);
      assert.equal(chosen.ok, true);
      const result = await handlers[CHANNELS.previewImport](event, { file_token: chosen.data.file_token, password: ' secret ' });
      assert.equal(result.ok, true);
      return result.data;
    },
  };
}
function code(result, expected) { assert.equal(result.ok, false); assert.equal(result.error.code, expected); }
function exportReply(reply, bytes = Buffer.from('encrypted-export')) {
  return reply(null, { body: bytes, headers: { 'x-portunus-instance': 'owned-instance', 'content-type': 'application/octet-stream',
    'content-length': String(bytes.length), 'x-portunus-channels': '1', 'x-portunus-models': '2', 'x-portunus-groups': '3' } });
}

test('IPC白名单与真实来源验证：非主窗口、子frame、伪装URL和任意额外参数均拒绝', async (t) => {
  const f = fixture(t);
  assert.equal(Object.keys(CHANNELS).length, 10);
  code(await f.from('summary', { ...f.event, sender: {} }), 'INVALID_INPUT');
  code(await f.from('summary', { ...f.event, senderFrame: { url: f.wc.mainFrame.url } }), 'INVALID_INPUT');
  for (const url of ['https://localhost:5173', 'file:///tmp/evil.html', `${f.wc.mainFrame.url}?unexpected=1`]) {
    f.wc.mainFrame.url = url;
    code(await f.invoke('summary'), 'INVALID_INPUT');
  }
  f.wc.mainFrame.url = `${pathToFileURL(f.rendererFile).href}#/settings`;
  code(await f.invoke('summary', {}), 'INVALID_INPUT');
  code(await f.invoke('chooseImportFile', { path: '/tmp/private' }), 'INVALID_INPUT');
  code(await f.invoke('exportConfig', { password: 'x', path: '/tmp/private' }), 'INVALID_INPUT');
  code(await f.invoke('previewImport', { file_token: 'f'.repeat(64), password: 'x', file: 'raw' }), 'INVALID_INPUT');
  const summary = await f.invoke('summary');
  assert.equal(summary.ok, true);
  assert.equal(Object.hasOwn(summary.data, 'instance_id'), false);
  assert.equal(f.calls.length, 1);
  assert.equal(f.calls[0].url, 'http://127.0.0.1:13061/api/config-transfer/summary');
});

test('开发来源只允许固定http localhost:5173 origin', async (t) => {
  const f = fixture(t, { useDevServer: true });
  for (const url of ['http://127.0.0.1:5173/', 'http://localhost:5174/', 'http://localhost:5173.evil/', 'http://user@localhost:5173/']) {
    f.wc.mainFrame.url = url; code(await f.invoke('summary'), 'INVALID_INPUT');
  }
  f.wc.mainFrame.url = 'http://localhost:5173/#/settings';
  assert.equal((await f.invoke('summary')).ok, true);
});

test('真实读文件前全局准入，重入不会再lstat/open', async (t) => {
  let finish;
  let stats = 0;
  const f = fixture(t, { fs: { ...fs.promises, lstat: async (...args) => {
    stats++; await new Promise((resolve) => { finish = resolve; }); return fs.promises.lstat(...args);
  } } });
  const pending = f.invoke('chooseImportFile');
  while (!finish) await new Promise((resolve) => setImmediate(resolve));
  code(await f.invoke('chooseImportFile'), 'TRANSFER_BUSY');
  assert.equal(stats, 1);
  finish();
  assert.equal((await pending).ok, true);
});

test('普通文件、大小与symlink检查在读取前执行，错误不泄漏路径', async (t) => {
  let opens = 0;
  const f = fixture(t, { fs: { ...fs.promises, open: (...args) => { opens++; return fs.promises.open(...args); } } });
  fs.truncateSync(f.selected, LIMITS.file + 1);
  code(await f.invoke('chooseImportFile'), 'LIMIT_EXCEEDED');
  assert.equal(opens, 0);
  fs.unlinkSync(f.selected);
  fs.mkdirSync(f.selected);
  const directory = await f.invoke('chooseImportFile');
  code(directory, 'FILE_READ_FAILED');
  assert.equal(JSON.stringify(directory).includes(f.root), false);
  assert.equal(opens, 0);
  fs.rmdirSync(f.selected);
  const real = path.join(f.root, 'real'); fs.writeFileSync(real, 'cipher');
  fs.symlinkSync(real, f.selected);
  code(await f.invoke('chooseImportFile'), 'FILE_READ_FAILED');
  assert.equal(opens, 0);
});

test('lstat/open竞态用同fd身份复核，替换后的文件不会读取', async (t) => {
  let reads = 0;
  const f = fixture(t, { fs: { ...fs.promises, open: async (...args) => {
    const fd = await fs.promises.open(...args);
    return { stat: async () => ({ ...(await fd.stat()), isFile: () => true, ino: -123 }),
      read: async (...readArgs) => { reads++; return fd.read(...readArgs); }, close: () => fd.close() };
  } } });
  code(await f.invoke('chooseImportFile'), 'FILE_READ_FAILED');
  assert.equal(reads, 0);
});

test('选中后使用不可变密文副本，multipart恰好两部分并保持密码字节', async (t) => {
  const f = fixture(t);
  const chosen = await f.invoke('chooseImportFile');
  assert.deepEqual(Object.keys(chosen.data).sort(), ['bytes', 'file_name', 'file_token']);
  fs.writeFileSync(f.selected, 'replaced-file');
  const pw = '  e\u0301\0😀\r\n ';
  assert.equal((await f.invoke('previewImport', { file_token: chosen.data.file_token, password: pw })).ok, true);
  const request = f.calls[0];
  const body = request.bodySnapshot;
  assert.equal(body.includes(Buffer.from('encrypted-fixture')), true);
  assert.equal(body.includes(Buffer.from('replaced-file')), false);
  assert.equal(body.includes(Buffer.from(pw, 'utf8')), true);
  assert.equal(request.parts[1].every((byte) => byte === 0), true);
  assert.equal(body.toString().match(/Content-Disposition/g).length, 2);
  assert.equal(request.headers['Content-Length'], String(body.length));
  assert.equal(fs.readdirSync(f.root).length, 1);
});

test('密码限制按原UTF8字节，空白有效、组合字符不归一化，未配对代理项拒绝', async (t) => {
  const f = fixture(t);
  for (const password of ['', '\ud800', '\udc00', '😀'.repeat(257), 'x'.repeat(1025), 42]) {
    code(await f.invoke('exportConfig', { password }), 'INVALID_INPUT');
  }
  assert.equal(f.dialogs.save, 0);
  const cancelled = fixture(t, { dialog: { showSaveDialog: async () => ({ canceled: true }) } });
  for (const password of [' ', '😀'.repeat(256), 'e\u0301', 'é']) assert.deepEqual(await cancelled.invoke('exportConfig', { password }), { ok: true, data: { cancelled: true } });
});

test('令牌绑定窗口文档代次：hash保留、完整导航/renderer退出/TTL失效', async (t) => {
  let time = 0;
  const f = fixture(t, { now: () => time });
  const first = await f.invoke('chooseImportFile');
  f.wc.emit('did-start-navigation', {}, f.wc.mainFrame.url + '#/groups', true, true);
  assert.equal((await f.invoke('previewImport', { file_token: first.data.file_token, password: 'x' })).ok, true);
  f.wc.emit('did-start-navigation', {}, f.wc.mainFrame.url, false, true);
  code(await f.invoke('previewImport', { file_token: first.data.file_token, password: 'x' }), 'PREVIEW_EXPIRED');
  const second = await f.invoke('chooseImportFile');
  time += LIMITS.ttl;
  code(await f.invoke('previewImport', { file_token: second.data.file_token, password: 'x' }), 'PREVIEW_EXPIRED');
  const third = await f.invoke('chooseImportFile');
  f.wc.emit('render-process-gone');
  code(await f.invoke('previewImport', { file_token: third.data.file_token, password: 'x' }), 'PREVIEW_EXPIRED');
});

test('换文件先终结旧预览；关闭带ID，不能用空discard绕过当前预览', async (t) => {
  const f = fixture(t);
  await f.choosePreview();
  code(await f.invoke('discard', {}), 'OPERATION_MISMATCH');
  const changed = await f.invoke('chooseImportFile');
  assert.equal(changed.ok, true);
  assert.equal(f.calls.at(-1).method, 'DELETE');
  assert.equal(f.calls.at(-1).url.endsWith(`/previews/${PREVIEW}`), true);
  assert.equal((await f.invoke('discard', {})).ok, true);
  code(await f.invoke('previewImport', { file_token: changed.data.file_token, password: 'x' }), 'PREVIEW_EXPIRED');
});

test('保存取消是正常结果，不发起导出/KDF', async (t) => {
  const f = fixture(t, { dialog: { showSaveDialog: async () => ({ canceled: true }) } });
  assert.deepEqual(await f.invoke('exportConfig', { password: 'secret' }), { ok: true, data: { cancelled: true } });
  assert.equal(f.calls.length, 0);
});

test('导出只保存完整密文：0600同目录临时文件、同步关闭后原子替换、快照计数', async (t) => {
  const events = [];
  const f = fixture(t, { respond: (_, reply) => exportReply(reply), fs: { ...fs.promises,
    open: async (name, flags, mode) => {
      const fd = await fs.promises.open(name, flags, mode);
      if (name.includes('.portunus-transfer-')) {
        events.push(['open', path.dirname(name), flags, mode]);
        return { writeFile: async (bytes) => { events.push('write'); await fd.writeFile(bytes); },
          sync: async () => { events.push('sync'); await fd.sync(); }, close: async () => { events.push('close'); await fd.close(); } };
      }
      return fd;
    }, rename: async (...args) => { events.push('rename'); return fs.promises.rename(...args); },
  } });
  const saved = await f.invoke('exportConfig', { password: ' secret ' });
  assert.deepEqual(saved, { ok: true, data: { file_name: 'synthetic.portunus', bytes: 16, counts, format_version: 1 } });
  assert.equal(fs.readFileSync(f.selected, 'utf8'), 'encrypted-export');
  assert.equal(events[0][1], f.root);
  assert.equal(events[0][2] & fs.constants.O_EXCL, fs.constants.O_EXCL);
  assert.equal(events[0][3], 0o600);
  assert.deepEqual(events.slice(1), ['write', 'sync', 'close', 'rename']);
  assert.equal(fs.readdirSync(f.root).length, 1);
  assert.equal(JSON.parse(f.calls[0].bodySnapshot).app_version, 'test-app');
  assert.equal(f.calls[0].parts[0].every((byte) => byte === 0), true);
});

test('部分写入失败只清理本次临时密文，已有目标保持不变', async (t) => {
  const f = fixture(t, { respond: (_, reply) => exportReply(reply), fs: { ...fs.promises,
    open: async (name, ...args) => {
      const fd = await fs.promises.open(name, ...args);
      return { writeFile: async (bytes) => { await fd.writeFile(bytes.subarray(0, 3)); throw new Error('secret path'); }, close: () => fd.close() };
    },
  } });
  const result = await f.invoke('exportConfig', { password: 'secret' });
  code(result, 'FILE_SAVE_FAILED');
  assert.equal(fs.readFileSync(f.selected, 'utf8'), 'encrypted-fixture');
  assert.deepEqual(fs.readdirSync(f.root), ['synthetic.portunus']);
  assert.equal(JSON.stringify(result).includes('secret path'), false);
});

test('临时文件O_EXCL碰撞不删除非本次文件，目标目录/symlink拒绝', async (t) => {
  const f = fixture(t, { randomBytes: (bytes) => Buffer.alloc(bytes), respond: (_, reply) => exportReply(reply) });
  const collision = path.join(f.root, `.portunus-transfer-${'00'.repeat(24)}.tmp`);
  fs.writeFileSync(collision, 'owned by another job');
  code(await f.invoke('exportConfig', { password: 'x' }), 'FILE_SAVE_FAILED');
  assert.equal(fs.readFileSync(collision, 'utf8'), 'owned by another job');
  fs.unlinkSync(f.selected); fs.mkdirSync(f.selected);
  code(await f.invoke('exportConfig', { password: 'x' }), 'FILE_SAVE_FAILED');
  fs.rmdirSync(f.selected); fs.symlinkSync(collision, f.selected);
  code(await f.invoke('exportConfig', { password: 'x' }), 'FILE_SAVE_FAILED');
  assert.equal(fs.readFileSync(collision, 'utf8'), 'owned by another job');
});

test('响应实例头、owned进程切换和选择后实例切换都拒绝能力', async (t) => {
  const mismatch = fixture(t, { respond: (_, reply) => reply({}, { headers: { 'x-portunus-instance': 'foreign-instance' } }) });
  code(await mismatch.invoke('summary'), 'BACKEND_CHANGED');
  const f = fixture(t);
  const chosen = await f.invoke('chooseImportFile');
  f.setAccess({ ready: true, instanceId: 'new-instance', generation: 2 });
  code(await f.invoke('previewImport', { file_token: chosen.data.file_token, password: 'x' }), 'BACKEND_CHANGED');
  assert.equal(f.calls.length, 0);
  const switched = fixture(t, { respond: (_, reply, access, setAccess) => {
    const response = exportReply(reply); setAccess({ ...access(), generation: 2 }); return response;
  } });
  code(await switched.invoke('exportConfig', { password: 'x' }), 'BACKEND_CHANGED');
  assert.equal(fs.readFileSync(switched.selected, 'utf8'), 'encrypted-fixture');
});

test('预览哈希与当前窗口绑定，伪造commit绝不发送HTTP', async (t) => {
  const f = fixture(t); await f.choosePreview();
  code(await f.invoke('commitImport', preview({ plan_sha256: '1'.repeat(64) })), 'OPERATION_MISMATCH');
  code(await f.invoke('commitImport', { ...preview(), url: 'http://evil' }), 'INVALID_INPUT');
  assert.equal(f.calls.filter((c) => c.url.endsWith('/commit')).length, 0);
});

test('确认响应丢失只查询同op，查询成功回放结果，绝不重复commit或建新预览', async (t) => {
  const f = fixture(t, { respond: (options, reply) => {
    if (options.url.endsWith('/previews')) return reply(preview());
    if (options.url.endsWith('/commit')) throw new Error('connection lost after commit');
    return reply({ operation_id: OP, state: 'committed', counts, replayed: true });
  } });
  const data = await f.choosePreview();
  const result = await f.invoke('commitImport', data);
  assert.equal(result.data.state, 'committed');
  assert.equal(result.data.replayed, true);
  assert.deepEqual(f.calls.map((c) => [c.method, new URL(c.url).pathname]), [
    ['POST', '/api/config-transfer/previews'], ['POST', `/api/config-transfer/operations/${OP}/commit`], ['GET', `/api/config-transfer/operations/${OP}`],
  ]);
});

test('确认后查询预算耗尽返回RESULT_UNCONFIRMED并保留op，不报告回滚', async (t) => {
  let time = 0;
  const f = fixture(t, { now: () => time, sleep: async (ms) => { time += ms; }, pollBudgetMs: 3000,
    respond: (options, reply) => options.url.endsWith('/previews') ? reply(preview()) : reply({ operation_id: OP, state: 'prepared', counts: { channels: 0, models: 0, groups: 0 } }),
  });
  const data = await f.choosePreview();
  const result = await f.invoke('commitImport', data);
  code(result, 'RESULT_UNCONFIRMED');
  assert.equal(result.error.operation_id, OP);
  assert.equal(f.calls.filter((c) => c.url.endsWith('/commit')).length, 1);
  assert.equal(f.calls.filter((c) => c.method === 'GET').every((c) => c.url.endsWith(`/operations/${OP}`)), true);
});

test('轻量GET网络重试至多三次且500ms，写请求不自动重试', async (t) => {
  const delays = [];
  const f = fixture(t, { sleep: async (ms) => delays.push(ms), respond: () => { throw new Error('offline'); } });
  code(await f.invoke('summary'), 'TRANSFER_UNAVAILABLE');
  assert.equal(f.calls.length, 3);
  assert.deepEqual(delays, [500, 500]);
  code(await f.invoke('recoverOperation', { operation_id: OP }), 'TRANSFER_UNAVAILABLE');
  assert.equal(f.calls.length, 4);
});

test('固定错误映射过滤底层message，仅安全字段路径透传', async (t) => {
  const f = fixture(t, { respond: (_, reply) => reply({ code: 'CONFIG_INVALID', error: 'sk-secret', message: '/private/secret', stage: 'validate', retryable: false, field: 'channels[12].name' }, { status: 422 }) });
  const result = await f.invoke('summary');
  code(result, 'CONFIG_INVALID');
  assert.equal(result.error.field, 'channels[12].name');
  assert.equal(JSON.stringify(result).includes('sk-secret'), false);
  const unsafe = fixture(t, { respond: (_, reply) => reply({ code: 'CONFIG_INVALID', field: '/secret value', stage: 'validate' }, { status: 422 }) });
  assert.equal(Object.hasOwn((await unsafe.invoke('summary')).error, 'field'), false);
});

test('恢复列表参数有界且固定查询，ACK不暴露通用URL/方法', async (t) => {
  const f = fixture(t);
  for (const input of [{ limit: 0 }, { limit: 101 }, { cursor: 'x'.repeat(257) }, { cursor: ';select' }, { url: 'evil' }]) code(await f.invoke('recoveryList', input), 'INVALID_INPUT');
  assert.equal((await f.invoke('recoveryList', { cursor: 'YWJj', limit: 100 })).ok, true);
  const url = new URL(f.calls[0].url);
  assert.equal(url.searchParams.get('unacknowledged'), '1');
  assert.equal(url.searchParams.get('limit'), '100');
  assert.equal(url.searchParams.get('cursor'), 'YWJj');
  assert.equal((await f.invoke('acknowledge', { operation_id: OP })).ok, true);
  assert.equal(f.calls.at(-1).url.endsWith(`/operations/${OP}/ack`), true);
});

test('preload只暴露固定配置迁移方法和固定IPC名称', () => {
  let api;
  const invoked = [];
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'preload.js'), 'utf8'), {
    process: { argv: [], platform: 'darwin' },
    require(id) {
      if (id === 'electron') return { contextBridge: { exposeInMainWorld: (_name, value) => { api = value; } }, ipcRenderer: { invoke: (...args) => invoked.push(args) } };
      return require(id);
    },
  });
  assert.deepEqual(Object.keys(api.configTransfer).sort(), Object.keys(CHANNELS).sort());
  api.configTransfer.summary();
  api.configTransfer.commitImport({ operation_id: OP });
  assert.equal(invoked[0][0], CHANNELS.summary);
  assert.equal(invoked[1][0], CHANNELS.commitImport);
});

test('sidecar私有access只授予owned存活子进程，冻结快照且换代失效', async () => {
  let child;
  const backend = createBackend({ binPath: process.execPath, dataDir: os.tmpdir(), port: 13061,
    portFree: async () => true, ping: async () => {}, spawn: () => {
      child = new EventEmitter(); child.kill = () => { child.killed = true; }; return child;
    },
  });
  assert.equal(backend.access.ready, false);
  await backend.start();
  const first = backend.access;
  assert.equal(first.ready, true);
  assert.equal(Object.isFrozen(first), true);
  backend.stop();
  assert.equal(backend.access.ready, false);
  assert.notEqual(first.generation, backend.access.generation);
  const unowned = createBackend({ binPath: null, dataDir: os.tmpdir(), port: 13061, ping: async () => {} });
  await unowned.start();
  assert.equal(unowned.state, 'ready');
  assert.equal(unowned.access.ready, false);
  unowned.stop();
});

test('真实HTTP传输贯穿handler：截断/超限/超时均拒绝，响应实例不匹配不能回传', async (t) => {
  const http = require('node:http');
  const { requestHTTP } = require('./config-transfer');
  let mode = 'normal';
  let received = 0;
  const server = http.createServer((req, res) => {
    received++;
    assert.equal(req.headers['x-portunus-instance'], 'owned-instance');
    res.setHeader('X-Portunus-Instance', mode === 'instance' ? 'unowned' : 'owned-instance');
    if (mode === 'timeout') return;
    if (mode === 'truncated') {
      res.setHeader('Content-Type', 'application/octet-stream');
      res.setHeader('Content-Length', '100');
      res.setHeader('Connection', 'close');
      res.end('only-three'); return;
    }
    if (mode === 'large') {
      res.write('x'.repeat(8193)); res.end(); return;
    }
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ counts, can_export: true, format_version: 1, limits: {}, ready: true }));
  });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  t.after(() => { server.closeAllConnections(); server.close(); });
  const port = server.address().port;
  const f = fixture(t, { sleep: async () => {}, transport: (options) => requestHTTP({ ...options,
    url: options.url.replace('127.0.0.1:13061', `127.0.0.1:${port}`), timeoutMs: 30,
  }) });
  assert.equal((await f.invoke('summary')).ok, true);
  mode = 'instance'; code(await f.invoke('summary'), 'BACKEND_CHANGED');
  mode = 'large'; code(await f.invoke('summary'), 'LIMIT_EXCEEDED');
  mode = 'truncated'; code(await f.invoke('exportConfig', { password: 'synthetic-secret' }), 'TRANSFER_UNAVAILABLE');
  assert.equal(fs.readFileSync(f.selected, 'utf8'), 'encrypted-fixture');
  assert.deepEqual(fs.readdirSync(f.root), ['synthetic.portunus']);
  mode = 'timeout'; code(await f.invoke('summary'), 'TRANSFER_UNAVAILABLE');
  assert.equal(received, 7); // 正常/实例/超限/截断各一次，轻量超时最多三次。
});

test('提交/ACK后Go已释放预览，410清理仍能关闭与重新选择文件；409运行中保留保护', async (t) => {
  let running = false;
  const f = fixture(t, { respond: (options, reply) => {
    if (options.url.endsWith('/previews')) return reply(preview());
    if (options.method === 'DELETE') return reply({ code: running ? 'OPERATION_RUNNING' : 'PREVIEW_EXPIRED', stage: 'commit' }, { status: running ? 409 : 410 });
    return reply({ operation_id: OP, state: 'committed', counts });
  } });
  let data = await f.choosePreview();
  assert.equal((await f.invoke('commitImport', data)).data.state, 'committed');
  assert.equal((await f.invoke('acknowledge', { operation_id: OP })).ok, true);
  assert.equal((await f.invoke('chooseImportFile')).ok, true);
  data = await f.choosePreview();
  assert.equal((await f.invoke('discard', { preview_id: data.preview_id })).ok, true);
  await f.choosePreview();
  running = true;
  code(await f.invoke('chooseImportFile'), 'OPERATION_RUNNING');
  code(await f.invoke('discard', { preview_id: PREVIEW }), 'OPERATION_RUNNING');
  running = false;
  assert.equal((await f.invoke('discard', { preview_id: PREVIEW })).ok, true);
});

test('commit已完成但响应序列化槽繁忙，Main先查同op并返回committed', async (t) => {
  const f = fixture(t, { respond: (options, reply) => {
    if (options.url.endsWith('/previews')) return reply(preview());
    if (options.url.endsWith('/commit')) return reply({ code: 'TRANSFER_BUSY', stage: 'body_read', retryable: true }, { status: 423 });
    return reply({ operation_id: OP, state: 'committed', counts, replayed: true });
  } });
  const data = await f.choosePreview();
  const result = await f.invoke('commitImport', data);
  assert.equal(result.ok, true);
  assert.equal(result.data.state, 'committed');
  assert.equal(f.calls.filter((c) => c.url.endsWith('/commit')).length, 1);
  assert.equal(f.calls.at(-1).url.endsWith(`/operations/${OP}`), true);
});

test('可能返回大型终态结果的查询也占Main作业槽，轻量摘要仍可读取', async (t) => {
  let finish;
  const f = fixture(t, { respond: (options, reply) => {
    if (options.url.endsWith(`/operations/${OP}`)) return new Promise((resolve) => { finish = () => resolve(reply({ operation_id: OP, state: 'committed', counts })); });
    return reply({ counts, can_export: true, limits: {}, ready: true, format_version: 1 });
  } });
  const pending = f.invoke('operationResult', { operation_id: OP });
  while (!finish) await new Promise((resolve) => setImmediate(resolve));
  code(await f.invoke('chooseImportFile'), 'TRANSFER_BUSY');
  code(await f.invoke('recoverOperation', { operation_id: OP }), 'TRANSFER_BUSY');
  assert.equal(f.dialogs.open, 0);
  assert.equal((await f.invoke('summary')).ok, true);
  finish(); assert.equal((await pending).data.state, 'committed');
});

test('所有带参IPC拒绝额外参数和非法标识，读取前不产生副作用', async (t) => {
  const f = fixture(t);
  const inputs = { exportConfig: { password: 'x' }, previewImport: { file_token: FILE_HASH, password: 'x' },
    commitImport: preview(), operationResult: { operation_id: OP }, recoverOperation: { operation_id: OP },
    recoveryList: {}, acknowledge: { operation_id: OP }, discard: {} };
  for (const [method, input] of Object.entries(inputs)) {
    code(await f.invoke(method, input, 'extra'), 'INVALID_INPUT');
    for (const invalid of [null, [], 'value', { ...input, path: '/untrusted' }]) {
      code(await f.invoke(method, invalid), 'INVALID_INPUT');
    }
  }
  for (const method of ['operationResult', 'recoverOperation', 'acknowledge']) {
    for (const operation_id of [undefined, `!${OP}`, `${OP}!`, OP.replace('-4', '-5'), 'other']) {
      code(await f.invoke(method, { operation_id }), 'INVALID_INPUT');
    }
  }
  for (const file_token of [null, 123, `!${FILE_HASH}`, `${FILE_HASH}!`]) {
    code(await f.invoke('previewImport', { file_token, password: 'x' }), 'INVALID_INPUT');
  }
  for (const preview_id of [123, 'short', `!${PREVIEW}`, `${PREVIEW}!`]) code(await f.invoke('discard', { preview_id }), 'INVALID_INPUT');
  for (const key of ['preview_id', 'operation_id', 'file_sha256', 'plan_sha256']) {
    code(await f.invoke('commitImport', preview({ [key]: `!${preview()[key]}` })), 'INVALID_INPUT');
    code(await f.invoke('commitImport', preview({ [key]: `${preview()[key]}!` })), 'INVALID_INPUT');
  }
  assert.equal(f.calls.length, 0);
  assert.deepEqual(f.dialogs, { open: 0, save: 0 });
});

test('IPC来源在关闭、销毁、缺少URL时失效；没有就绪的owned后端不能发请求', async (t) => {
  const f = fixture(t);
  f.wc.mainFrame.url = undefined;
  code(await f.invoke('summary'), 'INVALID_INPUT');
  f.wc.mainFrame.url = pathToFileURL(f.rendererFile).href;
  f.win.isDestroyed = () => true;
  code(await f.invoke('summary'), 'INVALID_INPUT');
  f.win.isDestroyed = () => false;
  for (const access of [null, { ready: false, instanceId: 'owned-instance', generation: 1 },
    { ready: true, instanceId: '', generation: 1 }, { ready: true, instanceId: 'owned-instance', generation: 1.5 }]) {
    f.setAccess(access);
    const result = await f.invoke('summary');
    code(result, 'TRANSFER_UNAVAILABLE');
    assert.equal(result.error.stage, 'recovery');
    assert.equal(result.error.retryable, true);
  }
  f.transfer.dispose();
  code(await f.invoke('summary'), 'INVALID_INPUT');
  assert.equal(f.calls.length, 0);
});

test('提交必须匹配四个预览标识，实例ID或代次单独变化均使能力失效', async (t) => {
  const f = fixture(t); await f.choosePreview();
  for (const change of [{ preview_id: 'b'.repeat(32) }, { operation_id: OP.replace(/^1/, '2') },
    { file_sha256: 'd'.repeat(64) }, { plan_sha256: 'c'.repeat(64) }]) {
    code(await f.invoke('commitImport', preview(change)), 'OPERATION_MISMATCH');
  }
  assert.equal(f.calls.length, 1);
  for (const changed of [{ ready: true, instanceId: 'new-instance', generation: 1 },
    { ready: true, instanceId: 'owned-instance', generation: 2 }]) {
    f.setAccess(changed);
    const result = await f.invoke('commitImport', preview());
    code(result, 'BACKEND_CHANGED');
    assert.equal(result.error.operation_id, OP);
    assert.equal(result.error.retryable, true);
    assert.equal(result.error.stage, 'commit');
  }
  assert.equal(f.calls.length, 1);
});

test('后端错误使用安全文案、阶段和布尔重试标记，未知字段不能泄漏', async (t) => {
  let payload;
  const f = fixture(t, { respond: (_, reply) => reply(payload, { status: 422 }) });
  const codes = ['INVALID_INPUT', 'LIMIT_EXCEEDED', 'EMPTY_CONFIGURATION', 'FORMAT_UNSUPPORTED',
    'CRYPTO_PROFILE_UNSUPPORTED', 'DECRYPT_FAILED', 'FILE_CORRUPT', 'CONFIG_INVALID', 'SOURCE_INTEGRITY_ERROR',
    'NAME_SPACE_EXHAUSTED', 'UNSUPPORTED_DATABASE_SCHEMA', 'PREVIEW_CHANGED', 'PREVIEW_EXPIRED',
    'OPERATION_RUNNING', 'TRANSFER_BUSY', 'OPERATION_MISMATCH', 'OPERATION_ABORTED', 'DB_BUSY',
    'TRANSFER_UNAVAILABLE', 'BACKEND_CHANGED', 'RESULT_UNCONFIRMED', 'FILE_READ_FAILED', 'FILE_SAVE_FAILED'];
  for (const errorCode of codes) {
    payload = { code: errorCode, stage: 'validate', retryable: true, message: 'sk-synthetic-secret',
      field: 'groups[123456].items[123456].model_ref', operation_id: OP };
    const result = await f.invoke('summary');
    code(result, errorCode);
    assert.ok(result.error.message.length > 0);
    assert.equal(result.error.stage, 'validate');
    assert.equal(result.error.retryable, true);
    assert.equal(result.error.operation_id, OP);
    assert.equal(result.error.field, payload.field);
    assert.equal(JSON.stringify(result).includes('sk-synthetic-secret'), false);
  }
  for (const stage of ['input', 'body_read', 'snapshot', 'encrypt', 'decrypt', 'validate', 'plan', 'commit', 'recovery', 'file_read', 'file_save']) {
    payload = { code: 'CONFIG_INVALID', stage, retryable: false };
    assert.equal((await f.invoke('summary')).error.stage, stage);
  }
  payload = { code: 'UNKNOWN', stage: 'private-secret', retryable: 'true', operation_id: `${OP}!`, field: 'channels[1].name!' };
  const fallback = await f.invoke('summary');
  code(fallback, 'TRANSFER_UNAVAILABLE');
  assert.equal(fallback.error.stage, 'input');
  assert.equal(fallback.error.retryable, false);
  assert.equal(Object.hasOwn(fallback.error, 'field'), false);
  assert.equal(Object.hasOwn(fallback.error, 'operation_id'), false);
  payload = { code: 'CONFIG_INVALID', field: 'a'.repeat(256) };
  assert.equal((await f.invoke('summary')).error.field.length, 256);
  payload.field += 'a';
  assert.equal(Object.hasOwn((await f.invoke('summary')).error, 'field'), false);
});

test('成功和错误响应都校验JSON，预览响应须包含合法完整绑定', async (t) => {
  let response;
  const f = fixture(t, { respond: (_, reply) => response(reply) });
  for (const status of [199, 300, 400]) {
    response = (reply) => reply({ code: 'CONFIG_INVALID', stage: 'validate' }, { status });
    code(await f.invoke('summary'), 'CONFIG_INVALID');
  }
  for (const status of [200, 422]) {
    response = (reply) => reply(null, { status, body: Buffer.from('{invalid') });
    const result = await f.invoke('summary');
    code(result, 'TRANSFER_UNAVAILABLE');
    assert.equal(result.error.stage, 'body_read');
  }
  response = (reply) => reply(null, { body: 'not-a-buffer' });
  code(await f.invoke('summary'), 'LIMIT_EXCEEDED');
  const chosen = await f.invoke('chooseImportFile');
  for (const key of ['preview_id', 'operation_id', 'file_sha256', 'plan_sha256']) {
    response = (reply) => reply(preview({ [key]: 'invalid' }));
    code(await f.invoke('previewImport', { file_token: chosen.data.file_token, password: 'x' }), 'TRANSFER_UNAVAILABLE');
  }
});

test('预览变化返回的新能力须再次确认，提交正文和multipart与契约一致', async (t) => {
  const replacement = preview({ preview_id: 'b'.repeat(32), operation_id: OP.replace(/^1/, '2'), plan_sha256: 'd'.repeat(64) });
  const f = fixture(t, { respond: (options, reply) => {
    if (options.url.endsWith('/previews')) return reply(preview());
    if (options.url.endsWith(`/operations/${OP}/commit`)) return reply({ code: 'PREVIEW_CHANGED', stage: 'plan', replacement_preview: replacement }, { status: 409 });
    return reply({ operation_id: replacement.operation_id, state: 'aborted', counts: { channels: 0, models: 0, groups: 0 } });
  } });
  await f.choosePreview();
  const upload = f.calls[0];
  const boundary = upload.headers['Content-Type'].match(/^multipart\/form-data; boundary=(.+)$/)[1];
  assert.deepEqual(upload.parts.map((part, i) => i === 1 ? '<cleared-password>' : part.toString()), [
    `--${boundary}\r\nContent-Disposition: form-data; name="password"\r\n\r\n`, '<cleared-password>',
    `\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="config.portunus"\r\nContent-Type: application/octet-stream\r\n\r\n`,
    'encrypted-fixture', `\r\n--${boundary}--\r\n`,
  ]);
  assert.equal(upload.parts[1].every((byte) => byte === 0), true);
  assert.equal(Number(upload.headers['Content-Length']), upload.bodySnapshot.length);
  const changed = await f.invoke('commitImport', preview());
  code(changed, 'PREVIEW_CHANGED');
  assert.deepEqual(changed.error.replacement_preview, replacement);
  code(await f.invoke('commitImport', preview()), 'OPERATION_MISMATCH');
  const finished = await f.invoke('commitImport', replacement);
  assert.equal(finished.data.state, 'aborted');
  assert.deepEqual(JSON.parse(f.calls.at(-1).bodySnapshot), { preview_id: replacement.preview_id,
    file_sha256: replacement.file_sha256, plan_sha256: replacement.plan_sha256 });
  assert.equal(f.calls.filter((call) => call.method === 'GET').length, 0);
});

test('导出拒绝不完整响应头，计数接受精确上限，取消不依赖路径是否存在', async (t) => {
  let changes = {};
  const f = fixture(t, { respond: (_, reply) => {
    const response = exportReply(reply);
    return { ...response, headers: { ...response.headers, ...changes } };
  } });
  for (const header of [{ 'content-type': 'text/plain' }, { 'content-length': '15' },
    { 'x-portunus-channels': '1001' }, { 'x-portunus-models': '50001' }, { 'x-portunus-groups': '5001' },
    { 'x-portunus-models': '!12' }, { 'x-portunus-models': '12!' }, { 'x-portunus-groups': undefined }]) {
    changes = header;
    code(await f.invoke('exportConfig', { password: 'x' }), 'TRANSFER_UNAVAILABLE');
    assert.equal(fs.readFileSync(f.selected, 'utf8'), 'encrypted-fixture');
  }
  changes = { 'x-portunus-channels': '1000', 'x-portunus-models': '50000', 'x-portunus-groups': '5000' };
  assert.deepEqual((await f.invoke('exportConfig', { password: 'x' })).data.counts, { channels: 1000, models: 50000, groups: 5000 });
  const canceled = fixture(t, { dialog: { showSaveDialog: async () => ({ canceled: true, filePath: '/unused' }),
    showOpenDialog: async () => ({ canceled: true, filePaths: ['/unused'] }) } });
  assert.deepEqual(await canceled.invoke('exportConfig', { password: 'x' }), { ok: true, data: { cancelled: true } });
  assert.deepEqual(await canceled.invoke('chooseImportFile'), { ok: true, data: { cancelled: true } });
  assert.equal(canceled.calls.length, 0);
});

test('文件读取完成仍复核同fd大小和时间，失败关闭fd并归还作业槽', async (t) => {
  for (const finalChanges of [{ size: 1 }, { mtimeMs: 2 }, { ctimeMs: 2 }, { isFile: () => false }]) {
    let closed = 0;
    const stat = { dev: 1, ino: 1, size: 0, mtimeMs: 1, ctimeMs: 1, isFile: () => true, isSymbolicLink: () => false };
    const f = fixture(t, { fs: { lstat: async () => stat, open: async () => {
      let stats = 0;
      return { stat: async () => ++stats === 1 ? stat : { ...stat, ...finalChanges },
        read: async () => ({ bytesRead: 0 }), close: async () => { closed++; } };
    } } });
    code(await f.invoke('chooseImportFile'), 'FILE_READ_FAILED');
    code(await f.invoke('chooseImportFile'), 'FILE_READ_FAILED');
    assert.equal(closed, 2);
    assert.equal(f.calls.length, 0);
  }
});

test('导航中止在途请求并丢弃迟到响应，旧预览只向原实例尽力释放', async (t) => {
  let complete; let signal;
  const f = fixture(t, { respond: (options, reply) => {
    signal = options.signal;
    return new Promise((resolve) => { complete = () => resolve(reply({ counts })); });
  } });
  const pending = f.invoke('summary');
  while (!complete) await new Promise((resolve) => setImmediate(resolve));
  f.wc.emit('did-start-navigation', {}, 'file:///elsewhere', false, true);
  assert.equal(signal.aborted, true);
  complete();
  code(await pending, 'PREVIEW_EXPIRED');
  for (const changeBackend of [false, true]) {
    const owned = fixture(t); await owned.choosePreview();
    if (changeBackend) owned.setAccess({ ready: true, instanceId: 'owned-instance', generation: 2 });
    owned.wc.emit('destroyed');
    await new Promise((resolve) => setImmediate(resolve));
    const deletes = owned.calls.filter((call) => call.method === 'DELETE');
    assert.equal(deletes.length, changeBackend ? 0 : 1);
    if (!changeBackend) {
      assert.equal(new URL(deletes[0].url).pathname, `/api/config-transfer/previews/${PREVIEW}`);
      assert.equal(deletes[0].headers['X-Portunus-Instance'], 'owned-instance');
    }
  }
});

test('临时密文同步期间导航或后端换代，原子替换前再次校验并保留已有目标', async (t) => {
  for (const navigation of [false, true]) {
    const f = fixture(t, { respond: (_, reply) => exportReply(reply), fs: { ...fs.promises,
      open: async (name, ...args) => {
        const fd = await fs.promises.open(name, ...args);
        if (!name.includes('.portunus-transfer-')) return fd;
        return { writeFile: (bytes) => fd.writeFile(bytes), close: () => fd.close(), sync: async () => {
          await fd.sync();
          if (navigation) f.wc.emit('did-start-navigation', {}, 'file:///elsewhere', false, true);
          else f.setAccess({ ready: true, instanceId: 'owned-instance', generation: 2 });
        } };
      },
    } });
    code(await f.invoke('exportConfig', { password: 'x' }), navigation ? 'PREVIEW_EXPIRED' : 'BACKEND_CHANGED');
    assert.equal(fs.readFileSync(f.selected, 'utf8'), 'encrypted-fixture');
    assert.deepEqual(fs.readdirSync(f.root), ['synthetic.portunus']);
  }
});

test('同一文件再次校验先释放旧预览，旧能力失效且密码缓存归零', async (t) => {
  let number = 0;
  const f = fixture(t, { respond: (options, reply) => options.method === 'DELETE' ? reply({})
    : reply(preview({ preview_id: (++number === 1 ? 'a' : 'b').repeat(32) })) });
  const selected = await f.invoke('chooseImportFile');
  for (let i = 0; i < 2; i++) assert.equal((await f.invoke('previewImport', { file_token: selected.data.file_token, password: 'x' })).ok, true);
  assert.deepEqual(f.calls.map((call) => call.method), ['POST', 'DELETE', 'POST']);
  assert.equal(new URL(f.calls[1].url).pathname, `/api/config-transfer/previews/${PREVIEW}`);
  for (const call of f.calls.filter((call) => call.method === 'POST')) assert.equal(call.parts[1].every((byte) => byte === 0), true);
  code(await f.invoke('discard', { preview_id: PREVIEW }), 'OPERATION_MISMATCH');
  assert.equal((await f.invoke('discard', { preview_id: 'b'.repeat(32) })).ok, true);
  code(await f.invoke('discard', { preview_id: 'b'.repeat(32) }), 'PREVIEW_EXPIRED');
});

test('HTTP传输接受精确字节上限，声明超限在接收前拒绝，预先取消不会请求服务器', async (t) => {
  const http = require('node:http');
  const { requestHTTP } = require('./config-transfer');
  let received = 0;
  const server = http.createServer((_req, res) => { received++; res.setHeader('Content-Length', '8'); res.end('12345678'); });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  t.after(() => { server.closeAllConnections(); server.close(); });
  const options = { url: `http://127.0.0.1:${server.address().port}`, method: 'GET', headers: {}, maxBytes: 8, timeoutMs: 1000 };
  assert.equal((await requestHTTP(options)).body.toString(), '12345678');
  await assert.rejects(requestHTTP({ ...options, maxBytes: 7 }), { code: 'LIMIT_EXCEEDED', stage: 'body_read' });
  const controller = new AbortController(); controller.abort();
  await assert.rejects(requestHTTP({ ...options, signal: controller.signal }), /cancelled request/);
  assert.equal(received, 2);
});
