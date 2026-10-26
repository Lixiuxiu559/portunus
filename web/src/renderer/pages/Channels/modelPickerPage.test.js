import { test } from 'node:test';
import assert from 'node:assert/strict';
import { modelPickerPage } from './modelPickerPage.js';

test('大量候选有界展示，最后一页完整可访问且不改变候选顺序', () => {
  const names = Array.from({ length: 123 }, (_, index) => `model-${index}`);
  const original = [...names];
  const first = modelPickerPage(names, '', 1);
  const second = modelPickerPage(names, '', 2);
  const last = modelPickerPage(names, '', 3);
  assert.equal(first.names.length, 50);
  assert.equal(second.names.length, 50);
  assert.equal(last.names.length, 23);
  assert.deepEqual([...first.names, ...second.names, ...last.names], original);
  assert.deepEqual(names, original);
});

test('搜索覆盖所有页面，忽略大小写；筛选后越界页回退到有效页', () => {
  const names = ['GPT-4', 'claude-1', 'gpt-5', 'claude-2', 'gpt-6'];
  assert.deepEqual(modelPickerPage(names, 'GPT', 2, 2), {
    names: ['gpt-6'], total: 3, totalPages: 2, page: 2,
  });
  assert.deepEqual(modelPickerPage(names, 'claude-2', 99, 2), {
    names: ['claude-2'], total: 1, totalPages: 1, page: 1,
  });
  assert.equal(modelPickerPage(names, '', 0, 2).page, 1);
});

test('空候选和无匹配搜索保留可用的第一页，不生成无效分页', () => {
  for (const names of [[], ['one']]) {
    assert.deepEqual(modelPickerPage(names, 'missing', 9), {
      names: [], total: 0, totalPages: 1, page: 1,
    });
  }
});
