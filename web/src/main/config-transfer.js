// 配置迁移专用文件能力：electron-free，所有路径只来自系统选择器。
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const { pathToFileURL } = require('node:url');
const { randomBytes } = require('node:crypto');
const { performance } = require('node:perf_hooks');
const { addressOf } = require('./server-address');

const LIMITS = Object.freeze({ file: 33554496, preview: 8 * 1024 * 1024, result: 12 * 1024 * 1024, password: 1024, ttl: 900000 });
const CHANNELS = require('./config-transfer-channels');
const MESSAGES = Object.freeze({
  INVALID_INPUT: '配置迁移请求无效', LIMIT_EXCEEDED: '配置迁移数据超过受理上限',
  EMPTY_CONFIGURATION: '没有可迁移的配置', FORMAT_UNSUPPORTED: '不支持此配置文件格式',
  CRYPTO_PROFILE_UNSUPPORTED: '不支持此加密文件', DECRYPT_FAILED: '密码错误或文件已损坏',
  FILE_CORRUPT: '配置文件无法读取', CONFIG_INVALID: '配置文件包含无效配置',
  SOURCE_INTEGRITY_ERROR: '配置关联不完整', NAME_SPACE_EXHAUSTED: '无法生成可用名称，请调整已有配置',
  UNSUPPORTED_DATABASE_SCHEMA: '当前数据库不支持配置迁移', PREVIEW_CHANGED: '名称计划已变化，请重新确认',
  PREVIEW_EXPIRED: '预览已失效，请重新选择或校验文件', OPERATION_RUNNING: '正在核实本次导入结果',
  TRANSFER_BUSY: '另一项配置迁移正在处理中，请稍后重试', OPERATION_MISMATCH: '导入操作与当前预览不一致',
  OPERATION_ABORTED: '本次导入已终止', DB_BUSY: '数据库暂时繁忙，请稍后核实',
  TRANSFER_UNAVAILABLE: '配置迁移服务暂不可用', BACKEND_CHANGED: '后端实例已变化，请重新核实操作',
  RESULT_UNCONFIRMED: '结果待核实，请继续查询本次操作', FILE_READ_FAILED: '所选文件不可读取，请重新选择',
  FILE_SAVE_FAILED: '无法保存到所选位置，请重新选择',
});
const STAGES = new Set(['input', 'body_read', 'snapshot', 'encrypt', 'decrypt', 'validate', 'plan', 'commit', 'recovery', 'file_read', 'file_save']);
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const OPAQUE = /^[A-Za-z0-9_-]{16,128}$/;
const HASH = /^[a-f0-9]{64}$/;

class TransferError extends Error {
  constructor(code, stage = 'input', options = {}) {
    super(MESSAGES[code] || MESSAGES.TRANSFER_UNAVAILABLE);
    this.code = MESSAGES[code] ? code : 'TRANSFER_UNAVAILABLE';
    this.stage = STAGES.has(stage) ? stage : 'input';
    this.retryable = options.retryable === true;
    if (UUID.test(options.operation_id)) this.operation_id = options.operation_id;
    if (typeof options.field === 'string' && options.field.length <= 256
      && /^[a-z_]+(?:\[[0-9]{1,6}\])?(?:[./][a-z_]+(?:\[[0-9]{1,6}\])?)*$/.test(options.field)) this.field = options.field;
    if (options.replacement_preview) this.replacement_preview = options.replacement_preview;
  }
}
function invalid() { throw new TransferError('INVALID_INPUT'); }
function object(input, allowed, required = allowed) {
  if (!input || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).some((key) => !allowed.includes(key))
    || required.some((key) => !Object.hasOwn(input, key))) invalid();
}
function noArgs(args) { if (args.length) invalid(); }
function password(value) {
  if (typeof value !== 'string' || /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value)) invalid();
  const bytes = Buffer.from(value, 'utf8');
  if (!bytes.length || bytes.length > LIMITS.password) invalid();
  return bytes;
}
function operation(input) { object(input, ['operation_id']); if (!UUID.test(input.operation_id)) invalid(); }
function previewShape(value) {
  return value && typeof value === 'object' && OPAQUE.test(value.preview_id) && UUID.test(value.operation_id)
    && HASH.test(value.file_sha256) && HASH.test(value.plan_sha256);
}
function header(headers, key) { return headers?.[key.toLowerCase()]; }

// 不跟随重定向，不使用 renderer 的 axios／自动写重试，接收前后均限制响应字节。
function requestHTTP({ url, method, headers, parts = [], maxBytes, timeoutMs, signal }) {
  return new Promise((resolve, reject) => {
    let settled = false;
    let timer;
    const finish = (error, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      error ? reject(error) : resolve(value);
    };
    const req = http.request(url, { method, headers }, (res) => {
      const declared = res.headers['content-length'];
      if (declared !== undefined && (!/^\d+$/.test(declared) || Number(declared) > maxBytes)) {
        finish(new TransferError('LIMIT_EXCEEDED', 'body_read')); res.destroy(); req.destroy(); return;
      }
      const chunks = [];
      let bytes = 0;
      res.on('data', (chunk) => {
        bytes += chunk.length;
        if (bytes > maxBytes) { finish(new TransferError('LIMIT_EXCEEDED', 'body_read')); res.destroy(); req.destroy(); return; }
        chunks.push(chunk);
      });
      res.on('end', () => {
        if (declared !== undefined && Number(declared) !== bytes) { finish(new Error('incomplete response')); return; }
        finish(null, { status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks, bytes) });
      });
      res.on('error', (error) => finish(error));
      res.on('aborted', () => finish(new Error('incomplete response')));
    });
    const abort = () => { req.destroy(); finish(new Error('cancelled request')); };
    signal?.addEventListener('abort', abort, { once: true });
    req.on('error', (error) => finish(error));
    timer = setTimeout(() => { req.destroy(); finish(new Error('request timeout')); }, timeoutMs);
    if (signal?.aborted) { abort(); return; }
    for (const part of parts) req.write(part);
    req.end();
  });
}

function createConfigTransfer(deps) {
  const files = deps.fs || fs.promises;
  const constants = deps.constants || fs.constants;
  const transport = deps.transport || requestHTTP;
  const now = deps.now || (() => performance.now());
  const sleep = deps.sleep || ((ms) => new Promise((resolve) => setTimeout(resolve, ms)));
  const random = deps.randomBytes || randomBytes;
  const states = new Map();
  const requests = new Set();
  let busy = false;
  let disposed = false;

  function clearToken(state) {
    clearTimeout(state.tokenTimer);
    state.tokenTimer = null;
    state.token = null;
  }
  function invalidate(state) {
    const preview = state.preview;
    state.generation++;
    clearToken(state);
    state.preview = null;
    for (const request of requests) if (request.state === state) request.controller.abort();
    if (preview) releasePreview(preview);
  }
  function releasePreview(preview) {
    // 文档离开后不再给页面返回结果；只尽力释放原实例拥有的预览。
    let access;
    try { access = endpoint(); } catch { return; }
    if (access.instanceId !== preview.instanceId || access.generation !== preview.backendGeneration) return;
    Promise.resolve().then(() => transport({ url: `${access.url}/api/config-transfer/previews/${preview.preview_id}`, method: 'DELETE',
      headers: { 'X-Portunus-Instance': access.instanceId }, parts: [], maxBytes: 8192, timeoutMs: 5000 }))
      .then((response) => {
        if (!sameBackend(access) || header(response.headers, 'x-portunus-instance') !== access.instanceId) throw new TransferError('BACKEND_CHANGED', 'recovery');
      })
      .catch(() => {});
  }
  function bindWindow(win) {
    if (!win || states.has(win)) return;
    const state = { win, generation: 0, token: null, preview: null, tokenTimer: null };
    states.set(win, state);
    const wc = win.webContents;
    wc.on('did-start-navigation', (_event, _url, inPlace, mainFrame) => { if (mainFrame && !inPlace) invalidate(state); });
    wc.on('render-process-gone', () => invalidate(state));
    wc.once('destroyed', () => { invalidate(state); states.delete(win); });
  }
  function source(event) {
    const win = deps.getWindow();
    if (disposed || !win || win.isDestroyed() || event.sender !== win.webContents || event.senderFrame !== win.webContents.mainFrame) invalid();
    const raw = event.senderFrame.url;
    if (typeof raw !== 'string') invalid();
    const withoutHash = raw.split('#')[0];
    const trusted = deps.useDevServer
      ? (() => { try { const u = new URL(raw); return u.origin === 'http://localhost:5173' && u.username === '' && u.password === ''; } catch { return false; } })()
      : withoutHash === pathToFileURL(deps.rendererFile).href;
    if (!trusted) invalid();
    bindWindow(win);
    return states.get(win);
  }
  function current(state, generation) {
    if (disposed || state.generation !== generation || state.win.isDestroyed() || deps.getWindow() !== state.win) {
      throw new TransferError('PREVIEW_EXPIRED');
    }
  }
  function endpoint() {
    const access = deps.backend.access;
    if (!access || !access.ready || !access.instanceId || !Number.isSafeInteger(access.generation)) {
      throw new TransferError('TRANSFER_UNAVAILABLE', 'recovery', { retryable: true });
    }
    return { ...access, url: addressOf({ profile: deps.profile }).url };
  }
  function sameBackend(access) {
    const latest = deps.backend.access;
    return latest?.ready && latest.instanceId === access.instanceId && latest.generation === access.generation;
  }
  function bindPreview(state, data, access) {
    if (!previewShape(data)) throw new TransferError('TRANSFER_UNAVAILABLE', 'body_read');
    state.preview = { preview_id: data.preview_id, operation_id: data.operation_id, file_sha256: data.file_sha256,
      plan_sha256: data.plan_sha256, instanceId: access.instanceId, backendGeneration: access.generation };
  }
  async function request(state, route, { method = 'GET', json, parts, contentType, maxBytes = LIMITS.result, timeoutMs = 5000, read = false, deadline } = {}) {
    const generation = state.generation;
    const access = endpoint();
    const body = json === undefined ? (parts || []) : [Buffer.from(JSON.stringify(json))];
    const headers = { 'X-Portunus-Instance': access.instanceId };
    if (json !== undefined || parts) {
      headers['Content-Type'] = contentType || 'application/json';
      headers['Content-Length'] = String(body.reduce((sum, part) => sum + part.length, 0));
    }
    const controller = new AbortController();
    const pending = { state, controller };
    requests.add(pending);
    try {
      let response;
      for (let attempt = 0; attempt < (read ? 3 : 1); attempt++) {
        current(state, generation);
        if (deadline !== undefined && now() >= deadline) throw new TransferError('RESULT_UNCONFIRMED', 'recovery', { retryable: true });
        if (!sameBackend(access)) throw new TransferError('BACKEND_CHANGED', 'recovery', { retryable: true });
        try {
          response = await transport({ url: `${access.url}/api/config-transfer${route}`, method, headers, parts: body, maxBytes,
            timeoutMs: deadline === undefined ? timeoutMs : Math.min(timeoutMs, Math.max(1, deadline - now())), signal: controller.signal });
          break;
        } catch (error) {
          if (error instanceof TransferError || controller.signal.aborted || attempt === (read ? 2 : 0)) throw error;
          await sleep(deadline === undefined ? 500 : Math.min(500, Math.max(0, deadline - now())));
        }
      }
      current(state, generation);
      if (!sameBackend(access) || header(response.headers, 'x-portunus-instance') !== access.instanceId) {
        throw new TransferError('BACKEND_CHANGED', 'recovery', { retryable: true });
      }
      if (!Buffer.isBuffer(response.body) || response.body.length > maxBytes) throw new TransferError('LIMIT_EXCEEDED', 'body_read');
      if (response.status >= 200 && response.status < 300) return { ...response, access };
      let value;
      try { value = JSON.parse(response.body.toString('utf8')); } catch { throw new TransferError('TRANSFER_UNAVAILABLE', 'body_read'); }
      const options = { retryable: value.retryable, operation_id: value.operation_id, field: value.field };
      if (value.code === 'PREVIEW_CHANGED' && previewShape(value.replacement_preview)) {
        options.replacement_preview = value.replacement_preview;
        bindPreview(state, value.replacement_preview, access);
      }
      throw new TransferError(value.code, value.stage, options);
    } finally {
      requests.delete(pending);
      if (json && Object.hasOwn(json, 'password')) for (const part of body) part.fill(0);
    }
  }
  function parse(response) {
    try { return JSON.parse(response.body.toString('utf8')); }
    catch { throw new TransferError('TRANSFER_UNAVAILABLE', 'body_read'); }
  }
  async function jsonRequest(state, route, options) { return parse(await request(state, route, options)); }
  async function heavy(work) {
    if (busy) throw new TransferError('TRANSFER_BUSY', 'input', { retryable: true });
    busy = true;
    try { return await work(); } finally { busy = false; }
  }
  async function discardPreview(state) {
    if (state.preview) {
      const access = deps.backend.access;
      if (access?.ready && access.instanceId === state.preview.instanceId && access.generation === state.preview.backendGeneration) {
        try { await request(state, `/previews/${state.preview.preview_id}`, { method: 'DELETE', maxBytes: 8192 }); }
        catch (error) {
          // Go 在提交、到期或关闭时已经释放预览；410即已完成清理。
          if (!(error instanceof TransferError) || error.code !== 'PREVIEW_EXPIRED') throw error;
        }
      }
      state.preview = null;
    }
    clearToken(state);
  }
  async function readSelected(filePath) {
    let fd;
    try {
      const stat = await files.lstat(filePath);
      if (!stat.isFile() || stat.isSymbolicLink() || stat.size > LIMITS.file) throw new TransferError(stat.size > LIMITS.file ? 'LIMIT_EXCEEDED' : 'FILE_READ_FAILED', 'file_read');
      fd = await files.open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW || 0) | (constants.O_NONBLOCK || 0));
      const opened = await fd.stat();
      if (!opened.isFile() || opened.size > LIMITS.file || opened.dev !== stat.dev || opened.ino !== stat.ino) {
        throw new TransferError(opened.size > LIMITS.file ? 'LIMIT_EXCEEDED' : 'FILE_READ_FAILED', 'file_read');
      }
      // 在同一个描述符上按块读取；文件增长时也无法突破上限。
      const chunks = [];
      let bytes = 0;
      while (true) {
        const chunk = Buffer.allocUnsafe(Math.min(65536, LIMITS.file + 1 - bytes));
        const result = await fd.read(chunk, 0, chunk.length, null);
        if (!result.bytesRead) break;
        bytes += result.bytesRead;
        if (bytes > LIMITS.file) throw new TransferError('LIMIT_EXCEEDED', 'file_read');
        chunks.push(chunk.subarray(0, result.bytesRead));
      }
      const final = await fd.stat();
      if (!final.isFile() || final.size !== bytes || opened.size !== bytes || final.mtimeMs !== opened.mtimeMs || final.ctimeMs !== opened.ctimeMs) throw new TransferError('FILE_READ_FAILED', 'file_read');
      return Buffer.concat(chunks, bytes);
    } catch (error) {
      if (error instanceof TransferError) throw error;
      throw new TransferError('FILE_READ_FAILED', 'file_read');
    } finally { if (fd) await fd.close().catch(() => {}); }
  }
  async function safeSave(filePath, bytes, validate) {
    let temporary;
    let fd;
    async function target() {
      try {
        const stat = await files.lstat(filePath);
        if (!stat.isFile() || stat.isSymbolicLink()) throw new TransferError('FILE_SAVE_FAILED', 'file_save');
      } catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
    try {
      await target();
      temporary = path.join(path.dirname(filePath), `.portunus-transfer-${random(24).toString('hex')}.tmp`);
      fd = await files.open(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
      // 只有 open 成功后，这个随机临时路径才属于本次作业。
      const owned = temporary;
      temporary = null;
      try {
        await fd.writeFile(bytes);
        await fd.sync();
        await fd.close();
        fd = null;
        validate();
        await target();
        await files.rename(owned, filePath);
      } catch (error) { temporary = owned; throw error; }
      try {
        const directory = await files.open(path.dirname(filePath), constants.O_RDONLY);
        try { await directory.sync(); } finally { await directory.close(); }
      } catch { /* 平台可选的目录同步；文件已经成功原子替换。 */ }
    } catch (error) {
      if (fd) await fd.close().catch(() => {});
      if (temporary && fd !== undefined) await files.unlink(temporary).catch(() => {});
      if (error instanceof TransferError && ['PREVIEW_EXPIRED', 'BACKEND_CHANGED'].includes(error.code)) throw error;
      throw new TransferError('FILE_SAVE_FAILED', 'file_save');
    }
  }
  async function terminalAfterCommit(state, op) {
    const deadline = now() + (deps.pollBudgetMs ?? 120000);
    let delay = 1000;
    do {
      try {
        const data = await jsonRequest(state, `/operations/${op}`, { read: true, timeoutMs: 5000, deadline });
        if (data.state === 'committed' || data.state === 'aborted') return data;
      } catch (error) {
        if (error instanceof TransferError && error.code === 'PREVIEW_EXPIRED') throw error;
      }
      const remaining = deadline - now();
      if (remaining <= 0) break;
      await sleep(Math.min(delay, remaining));
      delay = Math.min(2000, delay + 250);
    } while (now() < deadline);
    throw new TransferError('RESULT_UNCONFIRMED', 'recovery', { retryable: true, operation_id: op });
  }

  const methods = {
    async summary(state, ...args) {
      noArgs(args);
      const data = await jsonRequest(state, '/summary', { read: true, maxBytes: 8192 });
      return { counts: data.counts, can_export: data.can_export, format_version: data.format_version, limits: data.limits, ready: data.ready };
    },
    async exportConfig(state, input, ...extra) {
      if (extra.length) invalid();
      object(input, ['password']);
      const pw = password(input.password);
      try { return await heavy(async () => {
        const generation = state.generation;
        const chosen = await deps.dialog.showSaveDialog(state.win, { title: '导出配置', defaultPath: 'Portunus.portunus', filters: [{ name: 'Portunus 加密配置', extensions: ['portunus'] }] });
        current(state, generation);
        if (chosen.canceled || !chosen.filePath) return { cancelled: true };
        const response = await request(state, '/exports', { method: 'POST', json: { password: pw.toString('utf8'), app_version: deps.getVersion() }, maxBytes: LIMITS.file, timeoutMs: 120000 });
        if (header(response.headers, 'content-type')?.split(';')[0] !== 'application/octet-stream'
          || header(response.headers, 'content-length') !== String(response.body.length)) throw new TransferError('TRANSFER_UNAVAILABLE', 'body_read');
        const counts = {};
        for (const [key, max] of [['channels', 1000], ['models', 50000], ['groups', 5000]]) {
          const value = header(response.headers, `x-portunus-${key}`);
          if (typeof value !== 'string' || !/^\d+$/.test(value) || Number(value) > max) throw new TransferError('TRANSFER_UNAVAILABLE', 'body_read');
          counts[key] = Number(value);
        }
        await safeSave(chosen.filePath, response.body, () => {
          current(state, generation);
          if (!sameBackend(response.access)) throw new TransferError('BACKEND_CHANGED', 'file_save');
        });
        return { file_name: path.basename(chosen.filePath), bytes: response.body.length, counts, format_version: 1 };
      }); } finally { pw.fill(0); }
    },
    async chooseImportFile(state, ...args) {
      noArgs(args);
      return heavy(async () => {
        const generation = state.generation;
        await discardPreview(state);
        const access = endpoint();
        const chosen = await deps.dialog.showOpenDialog(state.win, { title: '选择配置文件', properties: ['openFile'], filters: [{ name: 'Portunus 加密配置', extensions: ['portunus'] }] });
        current(state, generation);
        if (chosen.canceled || !chosen.filePaths?.length) return { cancelled: true };
        const selected = chosen.filePaths[0];
        const bytes = await readSelected(selected);
        current(state, generation);
        if (!sameBackend(access)) throw new TransferError('BACKEND_CHANGED', 'file_read', { retryable: true });
        const id = random(32).toString('hex');
        state.token = { id, bytes, expires: now() + LIMITS.ttl, generation, profile: deps.profile,
          instanceId: access.instanceId, backendGeneration: access.generation };
        state.tokenTimer = setTimeout(() => clearToken(state), LIMITS.ttl);
        state.tokenTimer.unref?.();
        return { file_token: id, file_name: path.basename(selected), bytes: bytes.length };
      });
    },
    async previewImport(state, input, ...extra) {
      if (extra.length) invalid();
      object(input, ['file_token', 'password']);
      if (typeof input.file_token !== 'string' || !HASH.test(input.file_token)) invalid();
      const pw = password(input.password);
      try { return await heavy(async () => {
        const token = state.token;
        if (!token || token.id !== input.file_token || token.expires <= now() || token.generation !== state.generation || token.profile !== deps.profile) {
          clearToken(state); throw new TransferError('PREVIEW_EXPIRED');
        }
        const access = endpoint();
        if (token.instanceId !== access.instanceId || token.backendGeneration !== access.generation) {
          clearToken(state); throw new TransferError('BACKEND_CHANGED', 'input', { retryable: true });
        }
        if (state.preview) {
          await request(state, `/previews/${state.preview.preview_id}`, { method: 'DELETE', maxBytes: 8192 });
          state.preview = null;
        }
        const boundary = `portunus-${random(24).toString('hex')}`;
        const parts = [Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="password"\r\n\r\n`), pw,
          Buffer.from(`\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="config.portunus"\r\nContent-Type: application/octet-stream\r\n\r\n`),
          token.bytes, Buffer.from(`\r\n--${boundary}--\r\n`)];
        if (parts.reduce((sum, part) => sum + part.length, 0) > 32 * 1024 * 1024 + 16384) throw new TransferError('LIMIT_EXCEEDED', 'body_read');
        const response = await request(state, '/previews', { method: 'POST', parts, contentType: `multipart/form-data; boundary=${boundary}`, maxBytes: LIMITS.preview, timeoutMs: 120000 });
        const data = parse(response);
        bindPreview(state, data, response.access);
        return data;
      }); } finally { pw.fill(0); }
    },
    async commitImport(state, input, ...extra) {
      if (extra.length) invalid();
      object(input, ['preview_id', 'operation_id', 'file_sha256', 'plan_sha256']);
      if (!previewShape(input)) invalid();
      return heavy(async () => {
        const preview = state.preview;
        if (!preview || ['preview_id', 'operation_id', 'file_sha256', 'plan_sha256'].some((key) => input[key] !== preview[key])) throw new TransferError('OPERATION_MISMATCH', 'commit');
        const access = endpoint();
        if (access.instanceId !== preview.instanceId || access.generation !== preview.backendGeneration) throw new TransferError('BACKEND_CHANGED', 'commit', { operation_id: input.operation_id, retryable: true });
        try {
          const data = await jsonRequest(state, `/operations/${input.operation_id}/commit`, { method: 'POST', json: { preview_id: input.preview_id, file_sha256: input.file_sha256, plan_sha256: input.plan_sha256 }, timeoutMs: 120000 });
          if (data.state === 'committed' || data.state === 'aborted') return data;
        } catch (error) {
          if (error instanceof TransferError && !['RESULT_UNCONFIRMED', 'OPERATION_RUNNING', 'TRANSFER_BUSY', 'BACKEND_CHANGED', 'TRANSFER_UNAVAILABLE', 'DB_BUSY', 'LIMIT_EXCEEDED'].includes(error.code)) throw error;
        }
        return terminalAfterCommit(state, input.operation_id);
      });
    },
    async operationResult(state, input, ...extra) {
      if (extra.length) invalid(); operation(input);
      return heavy(() => jsonRequest(state, `/operations/${input.operation_id}`, { read: true }));
    },
    async recoverOperation(state, input, ...extra) {
      if (extra.length) invalid(); operation(input);
      return heavy(() => jsonRequest(state, `/operations/${input.operation_id}/recover`, { method: 'POST', timeoutMs: 5000 }));
    },
    async recoveryList(state, input = {}, ...extra) {
      if (extra.length) invalid(); object(input, ['cursor', 'limit'], []);
      if (input.cursor !== undefined && (typeof input.cursor !== 'string' || Buffer.byteLength(input.cursor) > 256 || !/^[A-Za-z0-9_-]*$/.test(input.cursor))) invalid();
      if (input.limit !== undefined && (!Number.isInteger(input.limit) || input.limit < 1 || input.limit > 100)) invalid();
      const query = new URLSearchParams({ unacknowledged: '1', limit: String(input.limit ?? 20) });
      if (input.cursor) query.set('cursor', input.cursor);
      return jsonRequest(state, `/operations?${query}`, { read: true, maxBytes: 256 * 1024 });
    },
    async acknowledge(state, input, ...extra) {
      if (extra.length) invalid(); operation(input);
      await request(state, `/operations/${input.operation_id}/ack`, { method: 'POST', maxBytes: 8192 });
      return {};
    },
    async discard(state, input = {}, ...extra) {
      if (extra.length) invalid(); object(input, ['preview_id'], []);
      if (input.preview_id !== undefined && (typeof input.preview_id !== 'string' || !OPAQUE.test(input.preview_id))) invalid();
      if (state.preview && state.preview.preview_id !== input.preview_id) throw new TransferError('OPERATION_MISMATCH');
      if (input.preview_id && !state.preview) throw new TransferError('PREVIEW_EXPIRED');
      return heavy(async () => { await discardPreview(state); return {}; });
    },
  };
  function register(ipcMain) {
    for (const [method, channel] of Object.entries(CHANNELS)) ipcMain.handle(channel, async (event, ...args) => {
      try { return { ok: true, data: await methods[method](source(event), ...args) }; }
      catch (error) {
        const safe = error instanceof TransferError ? error : new TransferError('TRANSFER_UNAVAILABLE', 'recovery', { retryable: true });
        return { ok: false, error: { code: safe.code, message: safe.message, stage: safe.stage, retryable: safe.retryable,
          ...(safe.operation_id ? { operation_id: safe.operation_id } : {}), ...(safe.field ? { field: safe.field } : {}),
          ...(safe.replacement_preview ? { replacement_preview: safe.replacement_preview } : {}) } };
      }
    });
  }
  function dispose() {
    disposed = true;
    for (const state of states.values()) invalidate(state);
    states.clear();
  }
  return { register, bindWindow, dispose };
}
function registerConfigTransfer(ipcMain, deps) {
  const transfer = createConfigTransfer(deps);
  transfer.register(ipcMain);
  return transfer;
}
module.exports = { createConfigTransfer, registerConfigTransfer, CHANNELS, LIMITS, requestHTTP };
