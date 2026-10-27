import test from 'node:test';
import assert from 'node:assert/strict';
import { activeNewAPISubscriptions, formatNewAPIAmount, formatNewAPICompactAmount, newAPIAccountOverview, newAPIBillingLabels, newAPIHasPartialFailure,
  newAPINextReset, newAPISectionMessage, newAPISubscriptionOverview, newAPIUsagePercent, newAPIValue } from './newAPIQuotaModel.js';
const now = Date.parse('2026-10-27T00:00:00Z');

test('卡片只展示后端账户总额，局部失败和旧缓存不伪装成完整余额', () => {
  const snapshot = { newapi: { wallet: { status: 'ok' }, subscriptions: { status: 'ok' },
    account_summary: { remaining: '1091.962858', unit: { kind: 'money', code: 'CNY' }, limit_state: 'limited' } } };
  assert.deepEqual(newAPIAccountOverview(snapshot, now), { value: '1,091.96', unit: 'CNY', notice: undefined });
  snapshot.newapi.wallet.status = 'error';
  assert.equal(newAPIAccountOverview(snapshot, now).notice, '部分额度未获取，合计不完整');
  snapshot.newapi.account_summary.remaining = '0.000002';
  assert.equal(newAPIAccountOverview(snapshot, now).value, '<0.01');
  snapshot.newapi.account_summary.limit_state = 'unlimited';
  assert.equal(newAPIAccountOverview(snapshot, now).value, '含不限额订阅');
  snapshot.newapi.account_summary.expires_at = '2026-10-27T00:00:00Z';
  assert.equal(newAPIAccountOverview(snapshot, now).value, '需刷新');
  delete snapshot.newapi.account_summary;
  assert.equal(newAPIAccountOverview(snapshot, now).value, '待刷新');
});
const plan = (id, extra = {}) => ({ id: `subscription:${id}`, subscription_id: String(id), plan_id: '9',
  label: '同名套餐', scope: 'plan', account_status: 'active', expires_at: '2027-01-01T00:00:00Z', ...extra });
test('精确额度、零和极小余额只添加千分位，不转浮点', () => {
  assert.equal(formatNewAPIAmount('9007199254740993.000002'), '9,007,199,254,740,993.000002');
  assert.equal(formatNewAPIAmount('0'), '0');
  assert.equal(newAPIValue({ remaining: '1' }), '1');
  assert.equal(newAPIValue({ remaining: '0', limit_state: 'unlimited' }), '不限总额度');
  assert.equal(newAPIValue({}), '未知');assert.equal(newAPIValue(null), '未获取');
});

test('卡片金额保留两位小数，非零极小值不显示成零，详情保持完整精度', () => {
  const unit = { kind: 'money', code: 'CNY' };
  for (const [input, expected] of [
    ['0', '0.00'], ['-0.000', '0.00'], ['500', '500.00'], ['1091.962858', '1,091.96'],
    ['1.005', '1.01'], ['999.999', '1,000.00'], ['0.01', '0.01'],
    ['0.000002', '<0.01'], ['0.009999', '<0.01'], ['-0.000002', '>-0.01'],
    ['-1.005', '-1.01'], ['9007199254740993.995', '9,007,199,254,740,994.00'],
  ]) {
    const item = { remaining: input, unit };
    assert.equal(newAPIValue(item, true), expected);
    assert.equal(newAPIValue(item), formatNewAPIAmount(input));
    assert.equal(item.remaining, input);
  }
  assert.equal(formatNewAPICompactAmount('0.000002', { kind: 'count', code: 'QUOTA' }), '0.000002');
  assert.equal(newAPIValue({ limit_state: 'unlimited' }, true), '不限总额度');
  assert.equal(newAPIValue({}, true), '未知');
  assert.equal(newAPIValue(null, true), '未获取');
});
test('同计划两份订阅分别保留，过期、取消及未知到期状态不算生效订阅', () => {
  const items = [plan(345), plan(346), plan(347, { expires_at: '2026-10-26T00:00:00Z' }),
    plan(348, { account_status: 'cancelled' }), plan(349, { expires_at: undefined }), plan(350, { expires_at: 'not-a-date' })];
  assert.deepEqual(activeNewAPISubscriptions({ items }, now).map((item) => item.subscription_id), ['345', '346']);
});
test('钱包零不影响订阅；局部查询失败明确提示，不假装没有订阅', () => {
  const snapshot = { items: [{ id: 'account_quota', remaining: '0' }, plan(345)], newapi: { wallet: { status: 'ok' }, subscriptions: { status: 'ok' } } };
  assert.equal(activeNewAPISubscriptions(snapshot, now).length, 1);assert.equal(newAPIHasPartialFailure(snapshot), false);
  snapshot.newapi.subscriptions = { status: 'error', failure: { category: 'permission' } };
  assert.equal(newAPIHasPartialFailure(snapshot), true);
  assert.equal(newAPISectionMessage(snapshot.newapi.subscriptions, '订阅'), '订阅查询权限不足');
  assert.equal(newAPISectionMessage({ status: 'unsupported' }, '订阅'), '站点暂不支持订阅查询');
  assert.equal(newAPISectionMessage({ status: 'ok' }, '订阅'), '');
});
test('重置与到期时间分开，过去的重置时间不当作未来重置', () => {
  const items = [plan(1, { resets_at: '2026-11-01T00:00:00Z' }), plan(2, { resets_at: '2026-10-28T00:00:00Z' }), plan(3, { resets_at: '2026-10-26T00:00:00Z' })];
  assert.equal(newAPINextReset(items, now), '2026-10-28T00:00:00Z');assert.equal(newAPINextReset([items[2]], now), null);
});
test('接近耗尽不舍入为100%，未知计费偏好不猜默认值', () => {
  assert.equal(newAPIUsagePercent(99.999998), '>99.99%');assert.equal(newAPIUsagePercent(100), '100%');assert.equal(newAPIUsagePercent(undefined), null);
  assert.equal(newAPIBillingLabels.subscription_first, '订阅优先');assert.equal(newAPIBillingLabels.unknown, undefined);
});

const overviewSnapshot = (summary, section = { status: 'ok' }) => ({
  items: [plan(345, { remaining: '100' }), plan(346, { remaining: '200' })],
  newapi: { wallet: { status: 'ok' }, subscriptions: section, subscription_summary: summary },
});
const summary = (extra = {}) => ({ count: 2, unlimited_count: 0, remaining: '300',
  unit: { kind: 'count', code: 'QUOTA' }, expires_at: '2027-01-01T00:00:00Z', ...extra });

test('订阅合计和钱包使用同一金额展示规则，不修改后端合计', () => {
  for (const [remaining, expected] of [['1091.962858', '1,091.96'], ['0.000002', '<0.01'], ['0', '0.00']]) {
    const snapshot = overviewSnapshot(summary({ remaining, unit: { kind: 'money', code: 'CNY' } }));
    assert.deepEqual(newAPISubscriptionOverview(snapshot, now), { value: expected, unit: 'CNY', count: 2 });
    assert.equal(snapshot.newapi.subscription_summary.remaining, remaining);
  }
});

test('渠道卡片使用后端订阅合计，保留大数、零和极小值，不相加每份额度', () => {
  assert.deepEqual(newAPISubscriptionOverview(overviewSnapshot(summary({ remaining: '9007199254740993.000002' })), now),
    { value: '9,007,199,254,740,993.000002', unit: 'QUOTA', count: 2 });
  for (const remaining of ['0', '0.0000000001']) {
    assert.equal(newAPISubscriptionOverview(overviewSnapshot(summary({ remaining })), now).value, remaining);
  }
  const empty = overviewSnapshot(summary({ count: 0, remaining: '0', expires_at: undefined }));
  empty.items = [];
  assert.deepEqual(newAPISubscriptionOverview(empty, now), { value: '0', unit: 'QUOTA', count: 0 });
});

test('不限额订阅不显示为有限金额合计，混合时明确标注', () => {
  assert.deepEqual(newAPISubscriptionOverview(overviewSnapshot(summary({ unlimited_count: 1 })), now),
    { value: '含不限额订阅', count: 2 });
  assert.deepEqual(newAPISubscriptionOverview(overviewSnapshot(summary({ unlimited_count: 2 })), now),
    { value: '不限总额度', count: 2 });
});

test('旧缓存和到期后的汇总要求刷新，不沿用过期合计', () => {
  assert.deepEqual(newAPISubscriptionOverview(overviewSnapshot(undefined), now),
    { value: '待刷新', count: 2, notice: '刷新后获取订阅汇总' });
  const expired = overviewSnapshot(summary({ expires_at: '2026-10-27T00:00:00Z' }));
  expired.items[0].expires_at = '2026-10-27T00:00:00Z';
  assert.deepEqual(newAPISubscriptionOverview(expired, now),
    { value: '需刷新', count: 1, notice: '部分订阅已到期，请刷新查询' });
});

test('订阅失败不显示旧合计或0份，钱包和套餐名称失败不会抹去订阅合计', () => {
  assert.deepEqual(newAPISubscriptionOverview(overviewSnapshot(summary(), { status: 'error' }), now),
    { value: '未获取', count: null });
  const snapshot = overviewSnapshot(summary());
  snapshot.newapi.wallet = { status: 'error', failure: { category: 'rate_limited' } };
  assert.equal(newAPIHasPartialFailure(snapshot), true);
  assert.equal(newAPISubscriptionOverview(snapshot, now).value, '300');
  assert.equal(newAPISectionMessage(snapshot.newapi.wallet, '钱包'), '钱包查询被限流');
  snapshot.newapi.wallet = { status: 'ok' };
  snapshot.newapi.plans = { status: 'error' };
  assert.equal(newAPIHasPartialFailure(snapshot), true);
  assert.equal(newAPISubscriptionOverview(snapshot, now).value, '300');
});
