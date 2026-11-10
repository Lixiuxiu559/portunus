import test from 'node:test';
import assert from 'node:assert/strict';
import { getNavBlock, isNavLocked, setNavBlock } from './navGuard.js';

test('多个 key 的拦截原因互不覆盖，全部注销后放行', () => {
  assert.equal(getNavBlock(), null);
  setNavBlock('client-claude', 'Claude Code 配置有未保存改动');
  setNavBlock('other', '另一个面板有未保存改动');
  assert.equal(getNavBlock(), 'Claude Code 配置有未保存改动');
  setNavBlock('client-claude', null);
  assert.equal(getNavBlock(), '另一个面板有未保存改动');
  setNavBlock('other', null);
  assert.equal(getNavBlock(), null);
});

test('进行中硬拦截优先于可放弃草稿，解除后恢复普通导航确认', () => {
  setNavBlock('draft', '未保存草稿');
  assert.equal(isNavLocked(), false);
  setNavBlock('transfer', '导入处理中', { locked: true });
  assert.equal(isNavLocked(), true);
  assert.equal(getNavBlock(), '导入处理中');
  setNavBlock('transfer', '预览待确认');
  assert.equal(isNavLocked(), false);
  assert.equal(getNavBlock(), '未保存草稿');
  setNavBlock('draft', null); setNavBlock('transfer', null);
  assert.equal(getNavBlock(), null);
});
