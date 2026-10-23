import assert from 'node:assert/strict';
import test from 'node:test';
import { toggleGroupModel, moveGroupModel, groupCreatePayload, createModelPageLoader } from './groupModelSelection.js';

const first = { id: 1001, name: 'same-name', channel_id: 1 };
const second = { id: 2002, name: 'same-name', channel_id: 2 };

test('跨页跨渠道同名模型按 ID 选择，去重并保留名称快照', () => {
  let selected = toggleGroupModel([], first);
  selected = toggleGroupModel(selected, second);
  assert.deepEqual(selected, [first, second]);
  assert.deepEqual(toggleGroupModel(selected, { ...first }), [second]);
});

test('故障转移顺序影响请求优先级，手动成员以模型 ID 映射且不受重排影响', () => {
  const selected = moveGroupModel([first, second], second.id, -1);
  assert.deepEqual(selected, [second, first]);
  assert.deepEqual(groupCreatePayload({ name: ' coding ', strategy: 'failover', selected }), {
    name: 'coding', strategy: 'failover', items: [{ model_id: 2002, priority: 0 }, { model_id: 1001, priority: 1 }],
  });
  assert.equal(groupCreatePayload({ name: 'x', strategy: 'manual', selected, activeModelId: first.id }).active_model_id, 1001);
  assert.equal(moveGroupModel(selected, second.id, -1), selected);
  assert.equal(moveGroupModel(selected, 9999, 1), selected);
});

test('不能提交空成员、重复成员、空名称或已移除的手动成员', () => {
  for (const input of [
    { name: 'x', strategy: 'manual', selected: [] },
    { name: 'x', strategy: 'failover', selected: [first, first] },
    { name: ' ', strategy: 'failover', selected: [first] },
    { name: 'x', strategy: 'manual', selected: [second], activeModelId: first.id },
  ]) assert.throws(() => groupCreatePayload(input));
});

test('服务端分页参数原样传递，晚到的旧渠道或旧搜索结果不能覆盖新结果', async () => {
  const pending = [];
  const loader = createModelPageLoader((params) => new Promise((resolve, reject) => pending.push({ params, resolve, reject })));
  const old = loader.load({ channel_id: 1, name: 'old', page: 2, page_size: 20 });
  const newest = loader.load({ channel_id: 2, name: 'same', page: 1, page_size: 20 });
  assert.deepEqual(pending[1].params, { channel_id: 2, name: 'same', page: 1, page_size: 20 });
  pending[1].resolve({ total: 1, data: [second] });
  assert.deepEqual(await newest, { total: 1, models: [second] });
  pending[0].resolve({ total: 1, data: [first] });
  assert.equal(await old, null);
});

test('关闭失效请求，过期错误静默失效；当前错误仍可重试', async () => {
  const pending = [];
  const loader = createModelPageLoader(() => new Promise((resolve, reject) => pending.push({ resolve, reject })));
  const old = loader.load({ page: 1 });
  loader.invalidate();
  pending[0].reject(new Error('old failure'));
  assert.equal(await old, null);
  const current = loader.load({ page: 1 });
  pending[1].reject(new Error('current failure'));
  await assert.rejects(current, /current failure/);
  const retry = loader.load({ page: 1 });
  pending[2].resolve({ total: 0, data: [] });
  assert.deepEqual(await retry, { models: [], total: 0 });
});

test('已废弃的轮询和未知策略不能提交，有效名称及成员不能绕过策略校验', () => {
  for (const strategy of ['round_robin', 'unknown', '', undefined]) {
    assert.throws(() => groupCreatePayload({ name: 'coding', strategy, selected: [first], activeModelId: first.id }));
  }
});
