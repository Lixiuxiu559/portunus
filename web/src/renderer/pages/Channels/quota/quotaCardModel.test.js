import test from 'node:test';
import assert from 'node:assert/strict';
import { quotaCardPresentation } from './quotaCardModel.js';

const unit = { kind: 'money', code: 'CNY' };
const item = (remaining, extra = {}) => ({ id: 'balance', label: '余额', kind: 'balance', scope: 'account', remaining, unit, ...extra });
const newAPI = (remaining) => ({ items: [item('999')], newapi: {
  wallet: { status: 'ok' }, subscriptions: { status: 'ok' }, account_summary: item(remaining),
} });

test('不同厂商适配成相同的金额与单位字段，统一精度且单位不混入金额文本', () => {
  for (const [value, expected] of [['58.22', '58.22'], ['500', '500.00'], ['0', '0.00'], ['0.000002', '<0.01'], ['9007199254740993.995', '9,007,199,254,740,994.00']]) {
    for (const provider of ['deepseek.balance', 'siliconflow.balance', 'openrouter.credits', 'custom.balance', 'newapi.account']) {
      const snapshot = provider === 'newapi.account' ? newAPI(value) : { items: [item(value)] };
      const card = quotaCardPresentation(provider, snapshot);
      assert.equal(card.heading, '账户余额');
      assert.equal(card.rows.length, 1);
      assert.equal(card.rows[0].value, expected);
      assert.equal(card.rows[0].unit, 'CNY');
    }
  }
});

test('通用适配保留不同币种和非金额单位，不在卡片求和', () => {
  const snapshot = { items: [item('10'), item('20', { id: 'usd', unit: { kind: 'money', code: 'USD' } }), item('1', { id: 'quota', unit: { kind: 'count', code: 'QUOTA' } })] };
  const card = quotaCardPresentation('custom.balance', snapshot);
  assert.deepEqual(card.rows.map(({ value, unit }) => ({ value, unit })), [{ value: '10.00', unit: 'CNY' }, { value: '20.00', unit: 'USD' }]);
  assert.equal(card.moreCount, 3);
  assert.equal(quotaCardPresentation('custom.balance', { items: [snapshot.items[2]] }).rows[0].value, '1');
});

test('未知、不限额及NewAPI局部失败由适配器明确标注', () => {
  assert.equal(quotaCardPresentation('deepseek.balance', null).rows.length, 0);
  const unknown = quotaCardPresentation('custom.balance', { items: [item(undefined)] }).rows[0];
  assert.equal(unknown.value, '未知'); assert.equal(unknown.unit, undefined);
  const unlimited = quotaCardPresentation('custom.balance', { items: [item('0', { limit_state: 'unlimited' })] }).rows[0];
  assert.equal(unlimited.value, '不限总额度'); assert.equal(unlimited.unit, undefined);
  const snapshot = newAPI('58.22');
  snapshot.newapi.wallet.status = 'error';
  assert.equal(quotaCardPresentation('newapi.account', snapshot).notice, '部分额度未获取，合计不完整');
  delete snapshot.newapi.account_summary;
  assert.equal(quotaCardPresentation('newapi.account', snapshot).rows[0].value, '待刷新');
});
