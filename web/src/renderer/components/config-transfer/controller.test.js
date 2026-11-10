import test from 'node:test';
import assert from 'node:assert/strict';
import { createTransferController as createRealController } from './controller.js';
import { createResponseCache } from '../../api/responseCache.js';

const counts = { channels: 1, models: 2, groups: 1 };
const preview = { preview_id: 'p1', operation_id: 'op1', file_sha256: 'f1', plan_sha256: 'h1', counts,
  channels: [], models: [], groups: [], file_info: { format_version: 1, app_version: '0.5.3' } };
const result = { operation_id: 'op1', state: 'committed', counts, groups: [], channels: [], replayed: false };
const deferred = () => { let resolve; let reject; const promise = new Promise((done, fail) => { resolve = done; reject = fail; }); return { promise, resolve, reject }; };
const tick = () => new Promise((resolve) => setImmediate(resolve));
// 所有恢复轮询使用可推进的时钟，不在失败路径等待真实120秒。
function createTransferController(api, options = {}) {
  let clock = 0;
  return createRealController(api, { now: () => clock, wait: async (ms) => { clock += ms; }, ...options });
}
function mock(overrides = {}) {
  const calls = [];
  const defaults = { summary: { counts, can_export: true }, recoveryList: { data: [], next_cursor: null },
    discard: {}, acknowledge: {}, chooseImportFile: { file_token: 'token', file_name: 'a.portunus', bytes: 123 },
    previewImport: preview, commitImport: result, operationResult: result, recoverOperation: result, exportConfig: { file_name: 'out.portunus', bytes: 123, counts } };
  const api = Object.fromEntries(Object.entries(defaults).map(([method, value]) => [method, async (input) => {
    calls.push({ method, input });
    return typeof overrides[method] === 'function' ? overrides[method](input) : overrides[method] ?? value;
  }]));
  return { api, calls };
}
async function ready(controller) { controller.open('import'); await controller.chooseFile(); await controller.preview(' raw 密码 '); }

test('摘要失败不冒充空库，重试读取服务端值', async () => {
  let failure = true;
  const { api } = mock({ summary: () => { if (failure) throw new Error('摘要不可用'); return { counts, can_export: false }; } });
  const c = createTransferController(api);
  assert.equal(c.getSnapshot().summaryStatus, 'loading');
  assert.equal(c.getSnapshot().summary, null);
  await c.loadSummary();
  assert.equal(c.getSnapshot().summaryStatus, 'error');
  assert.equal(c.getSnapshot().summary, null);
  failure = false; await c.loadSummary();
  assert.equal(c.getSnapshot().summary.can_export, false);
});

test('系统取消保存保留表单态；导出结果使用实际快照数量', async () => {
  let cancelled = true;
  const { api, calls } = mock({ exportConfig: () => cancelled ? { cancelled: true } : { counts: { ...counts, models: 9 }, file_name: 'new.portunus' } });
  const c = createTransferController(api); c.open('export');
  await c.exportConfig(' 密码 ');
  assert.equal(c.getSnapshot().stage, 'export');
  assert.equal(c.getSnapshot().exported, null);
  assert.equal(calls.find((call) => call.method === 'exportConfig').input.password, ' 密码 ');
  cancelled = false; await c.exportConfig(' 密码 ');
  assert.equal(c.getSnapshot().exported.counts.models, 9);
});

test('解密错误保留文件并允许重输密码，字段路径单独呈现', async () => {
  let fails = true;
  const { api, calls } = mock({ previewImport: () => { if (fails) throw { message: '无法解密', field: 'channels[0].name', stage: 'decrypt' }; return preview; } });
  const c = createTransferController(api); c.open('import'); await c.chooseFile(); await c.preview('bad');
  assert.equal(c.getSnapshot().stage, 'import');
  assert.equal(c.getSnapshot().file.file_token, 'token');
  assert.equal(c.getSnapshot().preview, null);
  assert.equal(c.getSnapshot().errorField, 'channels[0].name');
  fails = false; await c.preview('  raw  ');
  assert.equal(c.getSnapshot().stage, 'preview');
  assert.equal(calls.filter((call) => call.method === 'previewImport')[1].input.password, '  raw  ');
});

test('处理时阻止关闭与重复请求，卸载后旧预览不更新页面且被释放', async () => {
  const pending = deferred();
  const { api, calls } = mock({ previewImport: () => pending.promise });
  const c = createTransferController(api); c.open('import'); await c.chooseFile();
  const request = c.preview('password');
  assert.equal(c.close(), false);
  await c.preview('again');
  assert.equal(calls.filter((call) => call.method === 'previewImport').length, 1);
  c.dispose(); pending.resolve(preview); await request;
  assert.notEqual(c.getSnapshot().stage, 'preview');
  assert.ok(calls.some((call) => call.method === 'discard' && call.input.preview_id === 'p1'));
});

test('换文件与返回使旧计划失效，关闭选文件阶段释放无ID令牌', async () => {
  const { api, calls } = mock();
  const c = createTransferController(api); await ready(c);
  await c.backToFile();
  assert.equal(c.getSnapshot().preview, null);
  assert.equal(c.getSnapshot().file, null);
  assert.ok(calls.some((call) => call.method === 'discard' && call.input.preview_id === 'p1'));
  await c.chooseFile(); c.close();
  assert.deepEqual(calls.filter((call) => call.method === 'discard').at(-1).input, {});
});

test('提交断线只核实同op，重放成功清缓存，结果关闭才ACK', async () => {
  const cache = createResponseCache();
  cache.write('/groups', {}, [{ id: 1 }], cache.generation());
  const oldGeneration = cache.generation();
  let refreshes = 0;
  const { api, calls } = mock({ commitImport: () => { throw new Error('lost response'); },
    operationResult: { ...result, replayed: true } });
  const c = createTransferController(api, { onCommitted: () => { refreshes += 1; cache.invalidate(); throw new Error('list refresh failed'); } });
  await ready(c); await c.commit();
  assert.equal(c.getSnapshot().result.state, 'committed');
  assert.equal(c.getSnapshot().result.replayed, true);
  assert.equal(calls.filter((call) => call.method === 'commitImport').length, 1);
  assert.deepEqual(calls.find((call) => call.method === 'operationResult').input, { operation_id: 'op1' });
  assert.equal(refreshes, 1);
  assert.equal(cache.read('/groups'), undefined);
  cache.write('/groups', {}, [{ id: 1 }], oldGeneration);
  assert.equal(cache.read('/groups'), undefined);
  assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 0);
  c.close(); await tick();
  assert.deepEqual(calls.find((call) => call.method === 'acknowledge').input, { operation_id: 'op1' });
});

test('120秒仍未知保留原op，继续查询也不重新提交，未知态关闭不ACK或discard', async () => {
  let clock = 0;
  let known = false;
  const { api, calls } = mock({ commitImport: { operation_id: 'op1', state: 'prepared' }, operationResult: () => {
    if (!known) throw new Error('not found'); return result;
  } });
  const c = createTransferController(api, { now: () => clock, wait: async (ms) => { clock += ms; }, queryBudget: 120_000 });
  await ready(c); await c.commit();
  assert.equal(clock, 120_000);
  assert.equal(c.getSnapshot().stage, 'unconfirmed');
  assert.equal(c.getSnapshot().result, null);
  assert.equal(c.getSnapshot().operationId, 'op1');
  const before = calls.length;
  c.close(); await tick();
  assert.ok(!calls.slice(before).some((call) => ['discard', 'acknowledge'].includes(call.method)));
  known = true;
  await c.openRecovery({ operation_id: 'op1', confirmed_at: '2026-11-09T00:00:00Z' });
  assert.equal(c.getSnapshot().result.state, 'committed');
  assert.equal(calls.filter((call) => call.method === 'commitImport').length, 1);
  assert.ok(calls.filter((call) => call.method === 'operationResult').every((call) => call.input.operation_id === 'op1'));
});

test('prepared由recover核实为aborted后才展示整次失败', async () => {
  const aborted = { operation_id: 'op1', state: 'aborted', failure_code: 'IMPORT_FAILED', stage: 'commit', counts: { channels: 0, models: 0, groups: 0 } };
  const { api, calls } = mock({ commitImport: { state: 'prepared' }, operationResult: { state: 'prepared' }, recoverOperation: aborted });
  const c = createTransferController(api); await ready(c); await c.commit();
  assert.deepEqual(c.getSnapshot().result, aborted);
  assert.ok(calls.some((call) => call.method === 'recoverOperation' && call.input.operation_id === 'op1'));
});

test('Main已用尽核实预算时立即进入待核实，不额外叠加120秒', async () => {
  let clock = 0;
  const { api, calls } = mock({ commitImport: () => { clock = 120_000; throw { code: 'RESULT_UNCONFIRMED' }; } });
  const c = createTransferController(api, { now: () => clock, wait: async (ms) => { clock += ms; } });
  await ready(c); await c.commit();
  assert.equal(c.getSnapshot().stage, 'unconfirmed');
  assert.equal(c.getSnapshot().operationId, 'op1');
  assert.ok(!calls.some((call) => ['operationResult', 'recoverOperation'].includes(call.method)));
});

test('新名称计划必须再次点击确认，提交绑定更新后的全部标识', async () => {
  const replacement = { ...preview, preview_id: 'p2', operation_id: 'op2', plan_sha256: 'h2' };
  const { api, calls } = mock({ commitImport: ({ operation_id }) => {
    if (operation_id === 'op1') throw { code: 'PREVIEW_CHANGED', replacement_preview: replacement };
    return { ...result, operation_id: 'op2' };
  } });
  const c = createTransferController(api); await ready(c); await c.commit();
  assert.equal(c.getSnapshot().stage, 'preview');
  assert.equal(c.getSnapshot().changed, true);
  assert.equal(calls.filter((call) => call.method === 'commitImport').length, 1);
  await c.commit();
  assert.deepEqual(calls.filter((call) => call.method === 'commitImport')[1].input,
    { preview_id: 'p2', operation_id: 'op2', file_sha256: 'f1', plan_sha256: 'h2' });
});

test('重复确认只产生一个提交，终态成功后不再提交', async () => {
  const pending = deferred();
  const { api, calls } = mock({ commitImport: () => pending.promise });
  const c = createTransferController(api); await ready(c);
  const commit = c.commit(); await c.commit();
  assert.equal(c.close(), false);
  pending.resolve(result); await commit; await c.commit();
  assert.equal(calls.filter((call) => call.method === 'commitImport').length, 1);
});

test('恢复列表支持游标，填新表单期间不强行打开旧结果', async () => {
  const pending = deferred();
  const { api, calls } = mock({ recoveryList: ({ cursor }) => cursor ? { data: [{ operation_id: 'op2' }], next_cursor: null } : pending.promise });
  const c = createTransferController(api);
  const list = c.loadRecoveries({ autoOpen: true }); c.open('export');
  pending.resolve({ data: [{ operation_id: 'op1' }], next_cursor: 'next' }); await list;
  assert.equal(c.getSnapshot().mode, 'export');
  assert.ok(!calls.some((call) => call.method === 'operationResult'));
  await c.loadRecoveries({ more: true });
  assert.equal(c.getSnapshot().recoveries.length, 2);
  assert.deepEqual(calls.filter((call) => call.method === 'recoveryList')[1].input, { cursor: 'next', limit: 20 });
});

test('空闲时读取旧回执复用结果，卸载不能自动ACK', async () => {
  const { api, calls } = mock({ recoveryList: { data: [{ operation_id: 'op1', confirmed_at: '2026-11-09T00:00:00Z' }] } });
  const c = createTransferController(api); await c.loadRecoveries({ autoOpen: true }); await tick();
  assert.equal(c.getSnapshot().stage, 'result');
  assert.equal(c.getSnapshot().recoveredAt, '2026-11-09T00:00:00Z');
  c.dispose(); assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 0);
});

test('摘要只接受最新请求，旧成功与旧失败不能覆盖新结果', async () => {
  const pending = [deferred(), deferred(), deferred(), deferred()];
  let index = 0;
  const { api } = mock({ summary: () => pending[index++].promise });
  const c = createTransferController(api);
  const oldSuccess = c.loadSummary();
  const latest = c.loadSummary();
  pending[1].resolve({ counts, can_export: false }); await latest;
  pending[0].resolve({ counts: { channels: 999 }, can_export: true }); await oldSuccess;
  assert.equal(c.getSnapshot().summary.can_export, false);
  assert.deepEqual(c.getSnapshot().summary.counts, counts);
  const oldFailure = c.loadSummary();
  const newest = c.loadSummary();
  pending[3].resolve({ counts, can_export: true }); await newest;
  pending[2].reject(new Error('stale failure')); await oldFailure;
  assert.equal(c.getSnapshot().summaryStatus, 'ready');
  assert.equal(c.getSnapshot().summary.can_export, true);
  assert.equal(c.getSnapshot().summaryError, '');
});

test('恢复列表过期成功与失败不污染新游标，默认读取不会自动打开回执', async () => {
  const pending = [deferred(), deferred(), deferred(), deferred()];
  let index = 0;
  const { api, calls } = mock({ recoveryList: () => pending[index++].promise });
  const c = createTransferController(api);
  const oldSuccess = c.loadRecoveries(); const latest = c.loadRecoveries();
  pending[1].resolve({ data: [{ operation_id: 'new' }], next_cursor: 'new-cursor' }); await latest;
  pending[0].resolve({ data: [{ operation_id: 'old' }], next_cursor: 'old-cursor' }); await oldSuccess;
  assert.deepEqual(c.getSnapshot().recoveries, [{ operation_id: 'new' }]);
  assert.equal(c.getSnapshot().recoveryCursor, 'new-cursor');
  assert.ok(!calls.some((call) => call.method === 'operationResult'));
  const oldFailure = c.loadRecoveries(); const newest = c.loadRecoveries();
  pending[3].resolve({}); await newest;
  pending[2].reject(new Error('stale recovery failure')); await oldFailure;
  assert.deepEqual(c.getSnapshot().recoveries, []);
  assert.equal(c.getSnapshot().recoveryCursor, null);
  assert.equal(c.getSnapshot().recoveryStatus, 'ready');
  assert.equal(c.getSnapshot().recoveryError, '');
});

test('摘要和恢复失败显示错误，重试清除旧错误，空错误使用可读fallback', async () => {
  let fails = true;
  const { api } = mock({ summary: () => { if (fails) throw undefined; return { counts }; },
    recoveryList: () => { if (fails) throw new Error('列表暂不可用'); return { data: [] }; } });
  const c = createTransferController(api);
  await c.loadSummary(); await c.loadRecoveries();
  assert.equal(c.getSnapshot().summaryStatus, 'error');
  assert.equal(c.getSnapshot().summaryError, '配置迁移暂时不可用，请重试');
  assert.equal(c.getSnapshot().recoveryStatus, 'error');
  assert.equal(c.getSnapshot().recoveryError, '列表暂不可用');
  fails = false; await c.loadSummary(); await c.loadRecoveries();
  assert.equal(c.getSnapshot().summaryStatus, 'ready');
  assert.equal(c.getSnapshot().summaryError, '');
  assert.equal(c.getSnapshot().recoveryStatus, 'ready');
  assert.equal(c.getSnapshot().recoveryError, '');
});

test('订阅者观察状态变化，取消订阅和卸载后不再通知，迟到摘要不能写状态', async () => {
  const pending = deferred();
  const recoveries = deferred();
  const { api } = mock({ summary: () => pending.promise, recoveryList: () => recoveries.promise });
  const c = createTransferController(api);
  let notifications = 0;
  const unsubscribe = c.subscribe(() => { notifications++; });
  c.open('import'); assert.equal(notifications, 1);
  unsubscribe(); c.close(); assert.equal(notifications, 1);
  await tick();
  const request = c.loadSummary();
  const list = c.loadRecoveries();
  c.subscribe(() => { notifications++; });
  c.dispose(); const before = c.getSnapshot();
  pending.resolve({ counts }); recoveries.resolve({ data: [{ operation_id: 'late' }] });
  await request; await list; await tick();
  assert.equal(c.getSnapshot(), before);
  assert.equal(notifications, 1);
  assert.equal(c.open('export'), false); assert.equal(c.close(), false);
  await c.chooseFile(); await c.preview('x'); await c.exportConfig('x'); await c.commit(); await c.backToFile();
  await c.openRecovery({ operation_id: 'late' });
  assert.equal(c.getSnapshot(), before);
});

test('选文件错误回到表单，系统取消和迟到选择释放令牌且不制造预览', async () => {
  let mode = 'error';
  const pending = deferred();
  const { api, calls } = mock({ chooseImportFile: () => {
    if (mode === 'error') throw { message: '文件不可读', stage: 'file_read', field: 'channels[0].name' };
    return mode === 'cancel' ? { cancelled: true } : pending.promise;
  } });
  const c = createTransferController(api); c.open('import');
  await c.preview('no-file');
  assert.ok(!calls.some((call) => call.method === 'previewImport'));
  await c.chooseFile();
  assert.equal(c.getSnapshot().stage, 'import');
  assert.equal(c.getSnapshot().error, '文件不可读');
  assert.equal(c.getSnapshot().errorStage, 'file_read');
  assert.equal(c.getSnapshot().errorField, 'channels[0].name');
  mode = 'cancel'; await c.chooseFile();
  assert.equal(c.getSnapshot().file, null);
  assert.equal(c.getSnapshot().error, '');
  assert.equal(c.getSnapshot().errorStage, '');
  assert.equal(c.getSnapshot().errorField, '');
  mode = 'pending'; const selection = c.chooseFile(); await tick();
  assert.equal(c.getSnapshot().stage, 'selecting');
  c.dispose(); const before = c.getSnapshot();
  const discards = calls.filter((call) => call.method === 'discard').length;
  pending.resolve({ file_token: 'late' }); await selection; await tick();
  assert.equal(c.getSnapshot(), before);
  assert.equal(calls.filter((call) => call.method === 'discard').length, discards + 1);
  assert.deepEqual(calls.filter((call) => call.method === 'discard').at(-1).input, {});
});

test('导出失败保留表单和安全字段；卸载后的迟到成功不能更新页面', async () => {
  let fail = true;
  const pending = deferred();
  const { api, calls } = mock({ exportConfig: () => { if (fail) throw { field: 'groups[0].name', stage: 'encrypt' }; return pending.promise; } });
  const c = createTransferController(api); c.open('export');
  await c.exportConfig(' password ');
  assert.equal(c.getSnapshot().stage, 'export');
  assert.equal(c.getSnapshot().error, '配置迁移暂时不可用，请重试');
  assert.equal(c.getSnapshot().errorStage, 'encrypt');
  assert.equal(c.getSnapshot().errorField, 'groups[0].name');
  fail = false; const exporting = c.exportConfig('again');
  assert.equal(c.getSnapshot().stage, 'exporting');
  assert.equal(c.getSnapshot().error, '');
  const count = calls.length;
  assert.equal(c.open('import'), false); assert.equal(c.close(), false);
  await c.exportConfig('duplicate'); await c.chooseFile(); await c.backToFile();
  assert.equal(calls.length, count);
  c.dispose(); const before = c.getSnapshot();
  pending.resolve({ file_name: 'late' }); await exporting;
  assert.equal(c.getSnapshot(), before);
});

test('忙碌时禁止换文件和返回；预览失败后的新表单没有遗留changed标记', async () => {
  const pending = deferred();
  const { api, calls } = mock({ previewImport: () => pending.promise });
  const c = createTransferController(api); c.open('import'); await c.chooseFile();
  const request = c.preview('x'); const count = calls.length;
  assert.equal(c.open('export'), false);
  await c.chooseFile(); await c.backToFile();
  assert.equal(calls.length, count);
  assert.equal(c.getSnapshot().stage, 'decrypting');
  pending.resolve(preview); await request;
  assert.equal(c.getSnapshot().changed, false);
  assert.equal(c.getSnapshot().error, '');
  await c.backToFile();
  assert.equal(c.getSnapshot().operationId, null);
  assert.equal(c.getSnapshot().changed, false);
});

test('同op多次查看只刷新和通知一次；关闭终态才ACK，重复关闭不再次ACK', async () => {
  const { api, calls } = mock();
  const notifications = [];
  const c = createTransferController(api, { onCommitted: (value) => notifications.push(value.operation_id) });
  await c.openRecovery({ operation_id: 'op1', created_at: 'created' }); await tick();
  assert.equal(c.getSnapshot().recoveredAt, 'created');
  assert.equal(c.getSnapshot().stage, 'result');
  assert.deepEqual(notifications, ['op1']);
  assert.equal(calls.filter((call) => call.method === 'summary').length, 1);
  assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 0);
  assert.equal(c.close(), true); await tick();
  assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 1);
  assert.equal(c.close(), true); await tick();
  assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 1);
  await c.openRecovery({ operation_id: 'op1', confirmed_at: 'confirmed' }); await tick();
  assert.equal(c.getSnapshot().recoveredAt, 'confirmed');
  assert.deepEqual(notifications, ['op1']);
  assert.equal(calls.filter((call) => call.method === 'summary').length, 1);
  c.dispose(); await tick();
  assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 1);
});

test('aborted不触发已提交刷新，实际关闭仍确认读取回执', async () => {
  const aborted = { operation_id: 'op1', state: 'aborted', counts };
  const { api, calls } = mock({ operationResult: aborted });
  let committed = 0;
  const c = createTransferController(api, { onCommitted: () => { committed++; } });
  await c.openRecovery({ operation_id: 'op1' });
  assert.deepEqual(c.getSnapshot().result, aborted);
  assert.equal(committed, 0);
  assert.equal(calls.filter((call) => call.method === 'summary').length, 0);
  assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 0);
  c.close(); await tick();
  assert.deepEqual(calls.find((call) => call.method === 'acknowledge').input, { operation_id: 'op1' });
});

test('查询预算到点进入未知态，继续查询原op能收敛，不重发commit', async () => {
  let clock = 0;
  let known = false;
  const delays = [];
  const { api, calls } = mock({ commitImport: { state: 'prepared' },
    operationResult: () => { if (!known) throw new Error('offline'); return result; } });
  const c = createTransferController(api, { now: () => clock, wait: async (ms) => { delays.push(ms); clock += ms; }, queryBudget: 3000 });
  await ready(c); await c.commit();
  assert.equal(c.getSnapshot().stage, 'unconfirmed');
  assert.equal(c.getSnapshot().error, '结果待核实。请继续查询本次操作，避免重复导入。');
  assert.deepEqual(delays, [1000, 2000]);
  assert.equal(clock, 3000);
  known = true; await c.continueQuery();
  assert.equal(c.getSnapshot().stage, 'result');
  assert.equal(c.getSnapshot().operationId, 'op1');
  assert.ok(calls.filter((call) => call.method === 'operationResult').every((call) => call.input.operation_id === 'op1'));
  assert.equal(calls.filter((call) => call.method === 'commitImport').length, 1);
  const queried = calls.filter((call) => call.method === 'operationResult').length;
  await c.continueQuery();
  assert.equal(calls.filter((call) => call.method === 'operationResult').length, queried);
});

test('只有PREVIEW_CHANGED且携带替换计划才展示重确认，其他错误只核实原op', async () => {
  for (const error of [{ code: 'PREVIEW_CHANGED' }, { code: 'DB_BUSY', replacement_preview: { ...preview, operation_id: 'foreign' } }]) {
    const { api, calls } = mock({ commitImport: () => { throw error; } });
    const c = createTransferController(api); await ready(c); await c.commit();
    assert.equal(c.getSnapshot().stage, 'result');
    assert.equal(c.getSnapshot().operationId, 'op1');
    assert.equal(calls.filter((call) => call.method === 'commitImport').length, 1);
    assert.deepEqual(calls.find((call) => call.method === 'operationResult').input, { operation_id: 'op1' });
  }
});

test('提交或核实进行中卸载，迟到终态不通知、不ACK、不释放不确定操作', async () => {
  for (const phase of ['commitImport', 'operationResult', 'recoverOperation']) {
    const pending = deferred();
    const { api, calls } = mock({
      ...(phase !== 'commitImport' ? { commitImport: { state: 'prepared' } } : {}),
      ...(phase === 'recoverOperation' ? { operationResult: { state: 'prepared' } } : {}),
      [phase]: () => pending.promise,
    });
    let committed = 0;
    const c = createTransferController(api, { onCommitted: () => { committed++; } });
    await ready(c); const committing = c.commit(); await tick();
    assert.ok(calls.some((call) => call.method === phase));
    const discards = calls.filter((call) => call.method === 'discard').length;
    c.dispose(); const before = c.getSnapshot();
    pending.resolve(result); await committing; await tick();
    assert.equal(c.getSnapshot(), before);
    assert.equal(committed, 0);
    assert.equal(calls.filter((call) => call.method === 'discard').length, discards);
    assert.equal(calls.filter((call) => call.method === 'acknowledge').length, 0);
  }
});

test('卸载非忙碌预览释放计划，关闭导出不清理导入令牌，不自动打开第二个旧回执', async () => {
  const { api, calls } = mock({ recoveryList: { data: [{ operation_id: 'op1' }] } });
  const c = createTransferController(api); await ready(c); c.dispose(); await tick();
  assert.deepEqual(calls.filter((call) => call.method === 'discard').at(-1).input, { preview_id: 'p1' });
  const other = mock(); const exported = createTransferController(other.api); exported.open('export');
  exported.close(); await tick();
  assert.equal(other.calls.filter((call) => call.method === 'discard').length, 0);
  const recovery = mock({ recoveryList: { data: [{ operation_id: 'op1' }] } });
  const recoveryController = createTransferController(recovery.api);
  await recoveryController.loadRecoveries({ autoOpen: true }); await tick();
  assert.equal(recoveryController.getSnapshot().stage, 'result');
  recoveryController.close(); await tick();
  const queries = recovery.calls.filter((call) => call.method === 'operationResult').length;
  await recoveryController.loadRecoveries({ autoOpen: true }); await tick();
  assert.equal(recoveryController.getSnapshot().mode, null);
  assert.equal(recovery.calls.filter((call) => call.method === 'operationResult').length, queries);
});
