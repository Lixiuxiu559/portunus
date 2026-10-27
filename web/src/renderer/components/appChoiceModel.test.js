import test from 'node:test';
import assert from 'node:assert/strict';
import { choiceKey, comboOptions, selectedChoice, selectOptions } from './appChoiceModel.js';

test('缺失已存选项只追加禁用展示，不丢失未知值、不变更原列表', () => {
  const options = [{ id: 'current', name: '当前分组' }];
  const result = selectOptions(options, 'deleted-model');
  assert.deepEqual(result[0], { id: 'deleted-model', name: 'deleted-model（当前值不可用）', disabled: true });
  assert.equal(selectedChoice(result, choiceKey('deleted-model')), undefined);
  assert.deepEqual(options, [{ id: 'current', name: '当前分组' }]);
});

test('明确清除映射的空串选项与未选择区分，禁用项不能触发变更', () => {
  const options = selectOptions([{ id: '', name: '不映射' }, { id: 'blocked', name: '不可用', disabled: true }], '');
  assert.equal(selectedChoice(options, choiceKey('')).id, '');
  assert.equal(selectedChoice(options, null), undefined);
  assert.equal(selectedChoice(options, choiceKey('blocked')), undefined);
  assert.equal(selectedChoice(options, choiceKey('unknown')), undefined);
});

test('空选项、数字 ID 与长标签正常处理', () => {
  assert.deepEqual(selectOptions([], ''), []);
  const name = '很长的模型分组名称'.repeat(30);
  const items = selectOptions([{ id: 42, name }], '42');
  assert.deepEqual(selectedChoice(items, choiceKey(42)), { id: '42', name });
});

test('候选模型去重和清理空值，同时保留任意自定义模型名', () => {
  const options = ['gpt-custom:model/v1', '', 'gpt-custom:model/v1', '模型-β', null];
  assert.deepEqual(comboOptions(options), ['gpt-custom:model/v1', '模型-β']);
  assert.equal(options.length, 5);
});
