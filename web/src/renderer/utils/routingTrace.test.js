import test from 'node:test';
import assert from 'node:assert/strict';
import { routingCoverage, routingEventDetails, routingFinalCandidate, routingLabel } from './routingTrace.js';
import { createAttemptLoader } from './requestAttempts.js';

test('区分未取得诊断、完整保留和截断，不把缺失解读为没有跳过', () => {
  assert.match(routingCoverage(null), /未采集、尚未完成或已清理/);
  assert.match(routingCoverage(null), /无法据此判断是否跳过/);
  assert.match(routingCoverage({ dropped_events: 0 }), /不构成 HTTP 尝试/);
  assert.match(routingCoverage({ dropped_events: 17 }), /另有 17 条事件未保留/);
});

test('候选跳过、半开名额和备用选择使用独立事件语义', () => {
  assert.equal(routingLabel('event', 'candidate_skipped'), '跳过候选');
  assert.equal(routingLabel('reason', 'probe_busy'), '半开探测名额已占用');
  assert.equal(routingLabel('reason', 'backup'), '选择备用目标');
  assert.match(routingLabel('reason', 'ignored'), /不计恢复成功/);
  assert.equal(routingLabel('strategy', 'manual'), '手动');
  assert.equal(routingLabel('strategy', 'failover'), '故障转移');
  assert.match(routingLabel('stop', 'committed_failure'), /禁止重放/);
  assert.match(routingLabel('stop', 'attempt_budget_exhausted'), /总尝试预算用尽/);
  assert.equal(routingLabel('outcome', 'relay_succeeded'), '网关调用处理成功');
  assert.equal(routingLabel('stop', 'client_write'), '客户端响应写入失败');
  assert.equal(routingLabel('stop', 'conversion_safety_stop'), '转换或流式校验触发安全停止');
});

test('最后选中候选独立于截断事件保留，不从事件猜测最终目标', () => {
  const trace = { final_candidate: 3, final_channel_id: 7, final_model_id: 42, dropped_events: 99,
    events: [{ candidate: 1, channel_id: 1, model_id: 1, event: 'candidate_selected' }] };
  assert.equal(routingFinalCandidate(trace, [{ id: 7, name: '备用渠道' }]), '候选 #3 · 备用渠道 · 模型 ID 42');
  assert.equal(routingFinalCandidate(trace), '候选 #3 · 渠道 #7（已删除或未加载） · 模型 ID 42');
  assert.equal(routingFinalCandidate({ events: trace.events }), '未记录选中候选');
});

test('未知枚举保留原值，不误报新事件或停止原因', () => {
  for (const kind of ['event', 'reason', 'scope', 'stop', 'strategy', 'outcome']) {
    assert.equal(routingLabel(kind, 'new_value'), 'new_value');
    assert.equal(routingLabel(kind, '__proto__'), '__proto__');
    assert.equal(routingLabel(kind, ''), '未记录');
  }
});

test('冷却作用域、重试建议、relay 和探测状态保留；0 秒不是未记录', () => {
  assert.equal(routingEventDetails({ relay_attempt: 2, scope: 'channel', retry_after: 0, probe: true }),
    'relay #2 · 渠道作用域 · 建议 0 秒后重试 · 半开探测');
  assert.match(routingEventDetails({ scope: 'model', retry_after: 23, err_kind: 'rate_limit_cooldown' }),
    /模型作用域 · 建议 23 秒后重试 · 错误分类：限流冷却中（rate_limit_cooldown）/);
  assert.equal(routingEventDetails({ probe: false }), '');
});

test('渠道准入诊断展示取得与释放名额和占用上限，零表示不限', () => {
  assert.equal(routingLabel('event', 'capacity_acquired'), '取得渠道并发名额');
  assert.equal(routingLabel('event', 'capacity_released'), '释放渠道并发名额');
  assert.equal(routingLabel('reason', 'channel_capacity'), '渠道并发已满');
  assert.equal(routingLabel('stop', 'no_available_target'), '没有可用目标');
  assert.equal(routingEventDetails({ capacity_limit: 3, in_flight: 3 }), '渠道并发：3 / 3');
  assert.equal(routingEventDetails({ capacity_limit: 0, in_flight: 4 }), '渠道并发：4 / 不限');
  assert.equal(routingEventDetails({ capacity_limit: 2, in_flight: 0 }), '渠道并发：0 / 2');
  assert.equal(routingEventDetails({}), '');
  assert.equal(routingEventDetails({ capacity_limit: 2 }), '');
  assert.equal(routingEventDetails({ capacity_limit: -1, in_flight: 1 }), '');
});

test('诊断精确传递请求 ID，旧响应和关闭后的失败不污染当前详情', async () => {
  const pending = new Map();
  const loader = createAttemptLoader((params) => new Promise((resolve, reject) => pending.set(params.request_id, { resolve, reject })));
  const old = loader.load({ request_id: 'old' });
  const current = loader.load({ request_id: 'exact_%+&' });
  const trace = { data: { events: [{ event: 'candidate_skipped', reason: 'probe_busy' }] } };
  pending.get('exact_%+&').resolve(trace);
  assert.deepEqual(await current, trace);
  pending.get('old').resolve({ data: null });
  assert.equal(await old, null);
  const closed = loader.load({ request_id: 'closed' });
  loader.invalidate();
  pending.get('closed').reject(new Error('late failure'));
  assert.equal(await closed, null);
});

test('当前 API 失败必须报错；成功 null 保留为未取得诊断', async () => {
  const failure = createAttemptLoader(async () => { throw new Error('API unavailable'); });
  await assert.rejects(failure.load({ request_id: 'r' }), /API unavailable/);
  const missing = createAttemptLoader(async () => ({ data: null }));
  assert.deepEqual(await missing.load({ request_id: 'r' }), { data: null });
});
