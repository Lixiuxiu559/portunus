import test from 'node:test';
import assert from 'node:assert/strict';
import { logChannelLabel, logErrorSummary, copyLogText } from './logDetails.js';

test('渠道展示只用名称，删除及历史缺失渠道不伪造名称', () => {
  const channels = [{ id: 1, name: '个人-deepseek' }, { id: 2, name: '乐聚-newapi' }];
  assert.equal(logChannelLabel(1, channels), '个人-deepseek');
  assert.equal(logChannelLabel(2, channels), '乐聚-newapi');
  assert.equal(logChannelLabel(9, channels), '渠道 #9（已删除或未加载）');
  assert.equal(logChannelLabel(0, channels), '未记录渠道');
});

test('错误摘要保留多行原文和未知分类，缺失错误不伪造成功或失败原因', () => {
  assert.equal(logErrorSummary({ err_kind: 'network', err_msg: '连接失败\n第二行' }), '网络错误\n连接失败\n第二行');
  assert.equal(logErrorSummary({ err_kind: 'future_kind' }), 'future_kind');
  assert.equal(logErrorSummary({}), '');
});

test('复制必须等待实际写入，拒绝权限和缺失剪贴板向上传递', async () => {
  let actual;
  await copyLogText('req_%&=x', { async writeText(text) { actual = text; } });
  assert.equal(actual, 'req_%&=x');
  await assert.rejects(copyLogText('req', { async writeText() { throw new Error('denied'); } }), /denied/);
  await assert.rejects(copyLogText('req', undefined), /剪贴板不可用/);
});
