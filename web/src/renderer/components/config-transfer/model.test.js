import test from 'node:test';
import assert from 'node:assert/strict';
import { filterImportedGroups, importedGroupIds, isTerminal, isTransferBusy, pageOf, passwordError, previewRelations, renamedItems, ZERO_COUNTS } from './model.js';

test('密码保留空格及Unicode原值，按字节限制且拒绝未配对代理项', () => {
  assert.equal(passwordError(''), '请输入文件密码');
  assert.equal(passwordError(' '), '');
  assert.equal(passwordError('e\u0301'), '');
  assert.equal(passwordError('密'.repeat(342)), '密码不能超过 1024 字节');
  assert.equal(passwordError('\ud800'), '密码包含无效字符');
  assert.equal(passwordError('\udc00'), '密码包含无效字符');
  assert.equal(passwordError('🔒'), '');
});

test('详情、改名及成员本地20项分页，末页及空列表边界正确', () => {
  const items = Array.from({ length: 41 }, (_, id) => id);
  assert.deepEqual(pageOf(items, 2).items, items.slice(20, 40));
  assert.deepEqual(pageOf(items, 99), { items: [40], page: 3, pages: 3, total: 41 });
  assert.deepEqual(pageOf([], 0), { items: [], page: 1, pages: 1, total: 0 });
});

test('同名模型通过ref区分渠道，当前项按active ref，备用顺序沿DTO原顺序', () => {
  const data = { channels: [{ ref: 'c1', name: 'A（导入）', original_name: 'A' }, { ref: 'c2', name: 'B', original_name: 'B' }],
    models: [{ ref: 'm1', name: 'same', channel_ref: 'c1' }, { ref: 'm2', name: 'same', channel_ref: 'c2' }],
    groups: [{ ref: 'g1', name: 'group（导入）', original_name: 'group', active_item_ref: 'i2', items: [
      { ref: 'i1', model_ref: 'm1', position: 1, priority: '9223372036854775807' },
      { ref: 'i2', model_ref: 'm2', position: 2, priority: '9223372036854775807' }] }] };
  const relations = previewRelations(data);
  const members = data.groups[0].items.map((item) => relations.member(item, data.groups[0]));
  assert.deepEqual(members.map((item) => item.channelName), ['A（导入）', 'B']);
  assert.deepEqual(members.map((item) => item.active), [false, true]);
  assert.equal(members[0].priority, '9223372036854775807');
  assert.deepEqual(renamedItems(data).map((item) => item.kind), ['渠道', '分组']);
});

test('导入分组上下文只用导航state字符串ID，已删除项不会重建', () => {
  const groups = [{ id: 1, name: 'one' }, { id: 2, name: 'two' }];
  const ids = importedGroupIds({ importedGroupIds: ['1', '3', '1', 2, 'x', '0'] });
  assert.equal(ids.size, 2);
  assert.deepEqual(filterImportedGroups(groups, ids), [groups[0]]);
  assert.equal(importedGroupIds(null), null);
  assert.deepEqual(filterImportedGroups(groups, null), groups);
});

test('提交和整次失败均是终态，忙碌阶段逐个保护，其他值不能误判', () => {
  assert.deepEqual(ZERO_COUNTS, { channels: 0, models: 0, groups: 0 });
  assert.equal(Object.isFrozen(ZERO_COUNTS), true);
  for (const state of ['committed', 'aborted']) assert.equal(isTerminal({ state }), true);
  for (const value of [undefined, null, {}, { state: 'prepared' }, { state: 'committed ' }, { state: '' }]) assert.equal(isTerminal(value), false);
  for (const stage of ['selecting', 'exporting', 'decrypting', 'committing', 'checking']) assert.equal(isTransferBusy(stage), true);
  for (const stage of ['idle', 'import', 'export', 'preview', 'result', 'unconfirmed', 'exported', undefined]) assert.equal(isTransferBusy(stage), false);
});

test('密码1024字节精确边界及全部Unicode代理区间端点', () => {
  for (const password of ['a'.repeat(1024), '密'.repeat(341) + 'a', '😀'.repeat(256), '\ud800\udc00', '\udbff\udfff', 'x\ud800\udfffz']) {
    assert.equal(passwordError(password), '');
  }
  for (const password of ['a'.repeat(1025), '密'.repeat(341) + 'ab', '😀'.repeat(256) + ' ']) {
    assert.equal(passwordError(password), '密码不能超过 1024 字节');
  }
  for (const password of ['\ud800', '\udbff', '\udc00', '\udfff', '\ud800a', '\udbff\udbff', '\udc00\ud800', '\ud800\udc00\udfff']) {
    assert.equal(passwordError(password), '密码包含无效字符');
  }
  for (const password of ['\ud7ff', '\ue000', '\uffff', '  e\u0301  ']) assert.equal(passwordError(password), '');
  assert.equal(passwordError('a', 1), '');
  assert.equal(passwordError('é', 1), '密码不能超过 1 字节');
});

test('导航ID必须是完整的正十进制字符串，保持大整数原值', () => {
  const valid = ['1', '10', '123', '9223372036854775807'];
  const invalid = ['0', '01', '-1', '+1', 'x1', '1x', '1.5', '1 ', ' 1', '10x', '1a', '', 1, null];
  assert.deepEqual([...importedGroupIds({ importedGroupIds: [...valid, ...invalid, '10'] })], valid);
  for (const state of [{}, { importedGroupIds: '1' }, { importedGroupIds: null }]) assert.equal(importedGroupIds(state), null);
  assert.deepEqual(filterImportedGroups([{ id: '9223372036854775807' }, { id: 1 }, { id: 2 }], new Set(valid)),
    [{ id: '9223372036854775807' }, { id: 1 }]);
  assert.deepEqual(filterImportedGroups([{ id: 1 }], new Set()), []);
});

test('默认分页和自定义页长使用正确切片，不能重复或丢项', () => {
  assert.deepEqual(pageOf(), { items: [], page: 1, pages: 1, total: 0 });
  const items = Array.from({ length: 21 }, (_, id) => id);
  assert.deepEqual(pageOf(items), { items: items.slice(0, 20), page: 1, pages: 2, total: 21 });
  assert.deepEqual(pageOf(items, -1, 7), { items: items.slice(0, 7), page: 1, pages: 3, total: 21 });
  assert.deepEqual(pageOf(items, 2, 7), { items: items.slice(7, 14), page: 2, pages: 3, total: 21 });
  assert.deepEqual(pageOf(items, 9, 7), { items: items.slice(14), page: 3, pages: 3, total: 21 });
});

test('空预览和缺失引用仍展示明确占位，原对象字段和当前项状态保留', () => {
  for (const input of [undefined, null, {}]) {
    assert.deepEqual(renamedItems(input), []);
    const relations = previewRelations(input);
    assert.equal(relations.channelName('missing'), '渠道引用缺失');
    assert.deepEqual(relations.member({ ref: 'i1', model_ref: 'm0', priority: '99' }, { active_item_ref: 'i2' }),
      { ref: 'i1', model_ref: 'm0', priority: '99', modelName: '模型引用缺失', channelName: '渠道引用缺失', active: false });
  }
  const relations = previewRelations({ channels: [{ ref: 'c1', name: '已知渠道' }], models: [{ ref: 'm1', name: 'M', channel_ref: 'missing' }] });
  assert.equal(relations.channelName('c1'), '已知渠道');
  assert.equal(relations.member({ ref: 'i', model_ref: 'm1' }, { active_item_ref: 'i' }).channelName, '渠道引用缺失');
  assert.deepEqual(renamedItems({ channels: [{ ref: 'c', name: 'new', original_name: 'old' }], groups: [{ ref: 'g', name: 'same', original_name: 'same' }] }),
    [{ ref: 'c', name: 'new', original_name: 'old', kind: '渠道' }]);
});
