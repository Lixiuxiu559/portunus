import test from 'node:test';
import { headersWithoutToken } from './customQuotaModel.js';
import assert from 'node:assert/strict';
import { quotaDraft, quotaPayload, quotaValue, visibleSnapshot, createRequestGate, createCachePoller, canKeepQuotaToken, formatQuotaRelativeTime } from './quotaModel.js';

const adapter = { id: 'fixture.balance', name: '测试余额', default_base_url: 'https://example.test', credential_modes: ['inherit_channel_key', 'dedicated'] };

test('新来源关闭查询和定时，不默认设置币种或换算比例', () => {
  const draft = quotaDraft(null, adapter);
  assert.equal(draft.enabled, false);
  assert.equal(draft.auto_refresh_minutes, 0);
  assert.deepEqual(draft.options, {});
  assert.equal(quotaPayload(draft).credentials, undefined);
});

test('读取来源仅初始化脱敏草稿，Token 留空不清除，清除与替换独立', () => {
  const source = { ...quotaPayload(quotaDraft(null, adapter)), id: 7, config_revision: 3, credential_mode: 'dedicated', has_token: true };
  const draft = quotaDraft(source, adapter);
  assert.equal(draft.token, '');
  assert.equal(quotaPayload(draft, source).credentials, undefined);
  assert.equal(quotaPayload({ ...draft, token: '  ' }, source).credentials, undefined);
  assert.deepEqual(quotaPayload({ ...draft, token: 'new-token' }, source).credentials, { token: 'new-token' });
  assert.deepEqual(quotaPayload({ ...draft, token: 'ignored', clearToken: true }, source).credentials, { clear: true });
  assert.equal(quotaPayload({ ...draft, credential_mode: 'inherit_channel_key', token: 'ignored' }, source).credentials, undefined);
  assert.equal(quotaPayload(draft, source).expected_revision, 3);
});

test('保存并查询保留表单间隔，只额外启用并立即查询一次', () => {
  const draft = { ...quotaDraft(null, adapter), auto_refresh_minutes: '5' };
  assert.equal(quotaPayload(draft).auto_refresh_minutes, 5);
  const query = quotaPayload(draft, null, true);
  assert.equal(query.enabled, true);
  assert.equal(query.auto_refresh_minutes, 5);
  assert.equal(quotaPayload({ ...draft, auto_refresh_minutes: '0' }, null, true).auto_refresh_minutes, 0);
});

test('站点原始额度与显式换算配置均按原值传输，无浮点计算或币种猜测', () => {
  const draft = { ...quotaDraft(null, adapter), options: { user_id: '1', quota_per_unit: '', display_unit: '' } };
  assert.deepEqual(quotaPayload(draft).options, { user_id: '1' });
  const options = { user_id: '1', quota_per_unit: '500000', display_unit: 'credits' };
  assert.deepEqual(quotaPayload({ ...draft, options }).options, options);
});

test('未知、零、无限与负数分别显示，并保留大数/高精度 decimal string', () => {
  assert.equal(quotaValue({}), '未知');
  assert.equal(quotaValue({ remaining: '0', unit: { code: 'CNY' } }), '0 CNY');
  assert.equal(quotaValue({ remaining: '-0.01', unit: { code: 'USD' } }), '-0.01 USD');
  assert.equal(quotaValue({ remaining: '9007199254740993.123456789', unit: { code: 'quota' } }), '9007199254740993.123456789 quota');
  assert.equal(quotaValue({ limit_state: 'unlimited', total: '0' }), '无限');
  assert.equal(quotaValue({ used_percent: 132 }), '未知');
});

test('仅 fresh/stale 展示主摘要，失效身份和过期数据不能因保留旧对象而复活', () => {
  const snapshot = { items: [{ id: 'wallet', remaining: '12' }] };
  for (const status of ['disabled', 'never', 'unavailable', 'expired', 'invalidated']) {
    assert.equal(visibleSnapshot({ status, snapshot, last_snapshot: snapshot }), null);
  }
  for (const status of ['fresh', 'stale']) assert.equal(visibleSnapshot({ status, snapshot }), snapshot);
});

test('重复请求、草稿变更和关闭同时取消请求并隔离迟到结果', () => {
  const gate = createRequestGate();
  const first = gate.start();
  assert.equal(first.current(), true);
  const second = gate.start();
  assert.equal(first.signal.aborted, true);
  assert.equal(first.current(), false);
  assert.equal(second.current(), true);
  gate.cancel();
  assert.equal(second.signal.aborted, true);
  assert.equal(second.current(), false);
  const third = gate.start();
  assert.equal(third.current(), true);
  assert.equal(first.current(), false);
});


test('缓存读取失败后仍调度；隐藏停止，重新可见读取，卸载隔离迟到响应', async () => {
  let queued; let calls = 0; let hidden = false; let cancelled = 0; let finish;
  const timers = { setTimeout(fn) { queued = fn; return fn; }, clearTimeout(id) { if (queued === id) queued = null; } };
  const poller = createCachePoller({
    visible: () => !hidden, interval: () => 1500, timers, cancel: () => cancelled++,
    load: async () => { calls++; if (calls === 1) throw new Error('offline'); await new Promise((resolve) => { finish = resolve; }); },
  });
  poller.start();
  await queued();
  assert.equal(calls, 1);
  assert.equal(typeof queued, 'function');
  hidden = true; poller.visibilityChanged();
  assert.equal(queued, null); assert.equal(cancelled, 1);
  hidden = false; poller.visibilityChanged();
  assert.equal(calls, 2);
  poller.stop(); finish();
  await Promise.resolve(); await Promise.resolve();
  assert.equal(queued, null);
});


test('切换适配器后不能承诺保留旧 Token', () => {
  const source = { id: 1, adapter_id: 'old', query_base_url: 'https://example.test/', has_token: true, name: '已有来源' };
  assert.equal(canKeepQuotaToken(source, { adapter_id: 'old' }), true);
  assert.equal(canKeepQuotaToken(source, { adapter_id: 'new' }), false);
});

test('自定义结构化配置深拷贝，按字符串传输单位和金额路径', () => {
  const customAdapter = { id: 'custom.balance', credential_modes: ['dedicated'] };
  const draft = quotaDraft(null, customAdapter);
  assert.equal(draft.options.custom.items[0].unit.code, '');
  assert.throws(() => quotaPayload(draft), /单位代码/);
  draft.options.custom.items[0].unit.code = 'credits';
  const source = { ...quotaPayload(draft), has_token: true };
  const edited = quotaDraft(source, customAdapter);
  edited.options.custom.items[0].unit.code = 'USD';
  assert.equal(source.options.custom.items[0].unit.code, 'credits');
  const sent = quotaPayload(edited);
  assert.equal(sent.options.custom.items[0].remaining_path, '/data/balance');
  assert.deepEqual(sent.options.custom.request.headers, { Authorization: 'Bearer {{token}}' });
});

test('自定义 JSON 草稿语法与条件标量先验证，不发送编辑文本对象', () => {
  const draft = quotaDraft(null, { id: 'custom.balance' });
  draft.options.custom.items[0].unit.code = 'CNY';
  draft.customEditors.headers = '{';
  assert.throws(() => quotaPayload(draft), /请求头不是有效 JSON/);
  draft.customEditors.headers = '{"X-Test": 4}';
  assert.throws(() => quotaPayload(draft), /每个值均为字符串/);
  draft.customEditors.headers = '{}';
  draft.options.custom.request = { ...draft.options.custom.request, method: 'POST', body: '{' };
  assert.throws(() => quotaPayload(draft), /POST 请求体不是有效 JSON/);
  draft.options.custom.request.body = '{"account":"example"}';
  draft.options.custom.success = { path: '/success', equals_json: 'true' };
  draft.customEditors.success = '{}';
  assert.throws(() => quotaPayload(draft), /JSON 字符串/);
  draft.customEditors.success = 'true';
  const item = draft.options.custom.items[0];
  item.divisor = '500000';
  assert.throws(() => quotaPayload(draft), /原始单位/);
  item.original_unit = { kind: 'count', code: 'QUOTA' };
  item.unlimited = { path: '/unlimited', equals_json: 'true' };
  draft.customEditors.unlimited[item.id] = 'false';
  assert.equal(quotaPayload(draft).options.custom.items[0].divisor, '500000');
  assert.equal(quotaPayload(draft).options.custom.items[0].unlimited.equals_json, 'false');
});

test('自定义地址变更不保留 Token，重建适配器草稿不残留 custom', () => {
  const source = { adapter_id: 'custom.balance', query_base_url: 'https://a.test/', has_token: true };
  assert.equal(canKeepQuotaToken(source, { ...source, query_base_url: 'https://a.test' }), true);
  assert.equal(canKeepQuotaToken(source, { ...source, query_base_url: 'https://b.test' }), false);
  assert.equal(quotaDraft(null, adapter).options.custom, undefined);
});


test('null 条件值经编辑保存不会被替换为 true', () => {
  const draft = quotaDraft(null, { id: 'custom.balance' });
  draft.options.custom.items[0].unit.code = 'credits';
  draft.options.custom.success = { path: '/success', equals_json: 'null' };
  draft.options.custom.items[0].unlimited = { path: '/limit', equals_json: 'null' };
  const reloaded = quotaDraft(quotaPayload({ ...draft, customEditors: { ...draft.customEditors, success: 'null', unlimited: { balance_1: 'null' } } }));
  assert.equal(reloaded.customEditors.success, 'null');
  assert.equal(reloaded.customEditors.unlimited.balance_1, 'null');
  assert.equal(quotaPayload(reloaded).options.custom.success.equals_json, 'null');
});

test('大数、高精度比较值及 POST 请求体保留 JSON 原文', () => {
  const value = '9007199254740993.123456789012345678';
  const draft = quotaDraft(null, { id: 'custom.balance' });
  draft.options.custom.items[0].unit.code = 'credits';
  draft.options.custom.request.method = 'POST';
  draft.options.custom.request.body = '{"account":9007199254740993,"fraction":0.123456789012345678}';
  draft.options.custom.success = { path: '/code', equals_json: value };
  draft.customEditors.success = value;
  const payload = quotaPayload(draft);
  assert.equal(payload.options.custom.success.equals_json, value);
  assert.equal(payload.options.custom.request.body, draft.options.custom.request.body);
  const reloaded = quotaDraft(payload);
  assert.equal(reloaded.customEditors.success, value);
  assert.equal(quotaPayload(reloaded).options.custom.success.equals_json, value);
});

test('无鉴权去除所有 Token 占位请求头，保留普通请求头和无效草稿', () => {
  assert.deepEqual(JSON.parse(headersWithoutToken('{"Authorization":"Bearer {{token}}","X-Key":"{{token}}","Accept":"application/json"}')), { Accept: 'application/json' });
  assert.equal(headersWithoutToken('{'), '{');
  assert.equal(headersWithoutToken('[]'), '[]');
  const draft = quotaDraft(null, { id: 'custom.balance', credential_modes: ['none'] });
  draft.customEditors.headers = headersWithoutToken(draft.customEditors.headers);
  draft.options.custom.items[0].unit.code = 'credits';
  assert.deepEqual(quotaPayload(draft).options.custom.request.headers, {});
  assert.equal(quotaPayload(draft).credentials, undefined);
});


test('查询时间以相对时间展示，正常旧记录不被称作账户过期', () => {
 const now=Date.parse('2026-10-21T08:00:00Z');
 assert.equal(formatQuotaRelativeTime('2026-10-21T07:59:40Z',now),'刚刚');
 assert.equal(formatQuotaRelativeTime('2026-10-21T07:48:00Z',now),'12 分钟前');
 assert.equal(formatQuotaRelativeTime('2026-10-21T05:00:00Z',now),'3 小时前');
 assert.equal(formatQuotaRelativeTime('2026-10-20T08:00:00Z',now),'1 天前');
 assert.equal(formatQuotaRelativeTime('2026-10-22T08:00:00Z',now),'刚刚');
 assert.equal(formatQuotaRelativeTime(undefined,now),'尚未查询');
 assert.equal(formatQuotaRelativeTime('invalid',now),'尚未查询');
});
