import test from 'node:test';
import assert from 'node:assert/strict';
import { applyNewAPIDetection, createNewAPIDetector, newAPIDetectionTarget } from './newAPIDetection.js';
import { quotaDraft, switchQuotaAdapter } from './quotaModel.js';

const old = { id: 'deepseek.balance', name: 'DeepSeek 账户余额' };
const adapter = { id: 'newapi.account', name: 'New API 钱包与订阅' };
const options = { quota_per_unit: '500000/7', display_unit: 'CNY' };
const draft = () => ({ ...quotaDraft(null, adapter), query_base_url: 'https://example.test/prefix/', options: { user_id: '7' } });
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };

function setup(detect, value = draft()) {
  let current = value;
  const states = [];
  const detector = createNewAPIDetector({ readDraft: () => current, detect,
    applyOptions: (updated) => { current = { ...current, options: updated }; }, onChange: (state) => states.push(state) });
  return { detector, states, get: () => current, set: (value) => { current = value; } };
}

test('类型切换时默认名、空白名跟随；自定义名保留，切回原类型仍跟随', () => {
  for (const name of ['', '  ', old.name]) {
    const switched = switchQuotaAdapter({ ...quotaDraft(null, old), name }, old, adapter);
    assert.equal(switched.name, adapter.name);
    assert.equal(switchQuotaAdapter(switched, adapter, old).name, old.name);
  }
  assert.equal(switchQuotaAdapter({ ...quotaDraft(null, old), name: '我的钱包' }, old, adapter).name, '我的钱包');
});

test('只识别有效NewAPI根地址，不使用凭据、查询参数、片段或其它协议地址', () => {
  assert.equal(newAPIDetectionTarget(draft()), 'https://example.test/prefix');
  for (const root of ['', 'not a url', 'ftp://example.test', 'https://u:p@example.test', 'https://example.test?a=1', 'https://example.test#hash']) {
    assert.equal(newAPIDetectionTarget({ ...draft(), query_base_url: root }), '');
  }
  assert.equal(newAPIDetectionTarget({ ...draft(), adapter_id: old.id }), '');
});

test('已有任意手动换算字段不自动覆盖，显式应用保留用户ID等其它参数', () => {
  const source = { ...draft(), options: { user_id: '42', quota_per_unit: '9', display_unit: '', extra: 'kept' } };
  assert.equal(applyNewAPIDetection(source, options), null);
  assert.deepEqual(applyNewAPIDetection(source, options, true), { user_id: '42', ...options, extra: 'kept' });
  assert.equal(applyNewAPIDetection({ ...source, options: { display_unit: 'USD' } }, options), null);
});

test('空配置识别成功填入精确倍率与单位，同地址自动尝试一次，手动重试可再次识别', async () => {
  const calls = [];
  const app = setup(async (...args) => { calls.push(args); return { options }; });
  await app.detector.run();
  assert.deepEqual(app.get().options, { user_id: '7', ...options });
  assert.deepEqual(calls[0][0], { query_base_url: 'https://example.test/prefix' });
  assert.equal(calls[0][1].signal.aborted, false);
  assert.equal(app.states.at(-1).applied, true);
  await app.detector.run(); assert.equal(calls.length, 1);
  await app.detector.run(true); assert.equal(calls.length, 2);
});

test('失败不清空任何原配置，不自动重复请求；重试成功仍需显式应用手动值', async () => {
  let failed = true;
  const original = { ...draft(), options: { user_id: '8', quota_per_unit: '100', display_unit: 'credits' } };
  const app = setup(async () => { if (failed) throw new Error('unsupported'); return { options }; }, original);
  await app.detector.run();
  assert.deepEqual(app.get(), original); assert.equal(app.states.at(-1).status, 'error');
  failed = false; await app.detector.run(true);
  assert.equal(app.states.at(-1).applied, false); assert.deepEqual(app.get(), original);
  app.set({ ...app.get(), options: { ...app.get().options, user_id: '99' } });
  app.detector.apply();
  assert.deepEqual(app.get().options, { user_id: '99', ...options });
});

test('地址、类型和手动设置改变及关闭后，迟到成功或失败都不能更新草稿或提示', async () => {
  for (const fail of [false, true]) {
    for (const change of ['address', 'adapter', 'manual', 'close']) {
      const delayed = deferred();
      const app = setup(() => delayed.promise);
      const pending = app.detector.run();
      if (change === 'address') app.set({ ...app.get(), query_base_url: 'https://other.test' });
      if (change === 'adapter') app.set({ ...app.get(), adapter_id: old.id });
      if (change === 'manual') app.set({ ...app.get(), options: { user_id: '7', display_unit: 'credits' } });
      if (change === 'close') app.detector.cancel(); else app.detector.reset();
      const before = app.get(); const count = app.states.length;
      if (fail) delayed.reject(new Error('late')); else delayed.resolve({ options });
      await pending;
      assert.deepEqual(app.get(), before); assert.equal(app.states.length, count);
    }
  }
});

test('新请求成功后旧请求迟到不会覆盖；取消中的自动尝试允许重新挂载后重试', async () => {
  const first = deferred(); const second = deferred(); const requests = [first, second];
  const app = setup(() => requests.shift().promise);
  const oldRequest = app.detector.run(); app.detector.cancel();
  const newRequest = app.detector.run(); second.resolve({ options }); await newRequest;
  first.resolve({ options: { quota_per_unit: '1', display_unit: 'USD' } }); await oldRequest;
  assert.deepEqual(app.get().options, { user_id: '7', ...options });
});

test('缺少识别字段视为失败，不应用不完整的配对参数', async () => {
  const app = setup(async () => ({ options: { display_unit: 'USD' } }));
  const before = app.get(); await app.detector.run();
  assert.deepEqual(app.get(), before); assert.equal(app.states.at(-1).status, 'error');
});
