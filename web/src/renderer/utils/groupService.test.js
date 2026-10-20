import test from 'node:test';
import assert from 'node:assert/strict';
import { groupConfiguration, groupServicePath } from './groupService.js';

test('分组配置完整不等于在线；手动策略必须选择有效成员', () => {
  assert.equal(groupConfiguration({ strategy: 'manual', items: [] }).complete, false);
  const items = [{ id: 4, model_id: 20 }];
  assert.equal(groupConfiguration({ strategy: 'manual', items, active_item_id: 99 }).complete, false);
  assert.equal(groupConfiguration({ strategy: 'manual', items, active_item_id: 4 }).complete, true);
  for (const strategy of ['round_robin', 'failover']) {
    const state = groupConfiguration({ strategy, items });
    assert.equal(state.complete, true);
    assert.match(state.label, /未验证/);
  }
});

test('接入和记录跳转保留完整分组标识', () => {
  const name = '编码 A&B/快?=#';
  for (const path of ['/clients', '/logs']) {
    const url = new URL(groupServicePath(path, name), 'http://localhost');
    assert.equal(url.pathname, path);
    assert.equal(url.searchParams.get('group'), name);
    assert.equal(url.hash, '');
  }
});
