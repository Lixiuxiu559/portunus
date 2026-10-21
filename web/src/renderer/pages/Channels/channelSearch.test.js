import test from 'node:test';
import assert from 'node:assert/strict';
import { filterChannels } from './channelSearch.js';

const channels = [
  { id: 1, name: '工作 DeepSeek', provider_id: 'deepseek', type: 'openai', base_url: 'https://api.deepseek.com/v1', key: 'secret-hidden' },
  { id: 2, name: '备用网关', provider_id: 'generic', type: 'anthropic', base_url: 'https://relay.test/api' },
];
test('空搜索保留渠道与顺序，多词跨字段匹配且忽略大小写', () => {
  assert.equal(filterChannels(channels, ' \t '), channels);
  assert.deepEqual(filterChannels(channels, '工作 API.DEEPSEEK'), [channels[0]]);
  assert.deepEqual(filterChannels(channels, 'anthropic relay'), [channels[1]]);
  assert.deepEqual(filterChannels(channels, 'deepseek 备用'), []);
});
test('搜索不索引凭据，缺少元信息的渠道可正常筛选', () => {
  assert.deepEqual(filterChannels(channels, 'secret-hidden'), []);
  assert.deepEqual(filterChannels([{ id: 3, name: '本地' }], '本地'), [{ id: 3, name: '本地' }]);
  assert.deepEqual(filterChannels([], 'test'), []);
});
