import test from 'node:test';
import assert from 'node:assert/strict';
import { getNavBlock, setNavBlock } from './navGuard.js';

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
