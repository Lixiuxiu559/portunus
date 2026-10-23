import test from 'node:test';
import assert from 'node:assert/strict';
import { createResponseCache } from './responseCache.js';

test('首帧缓存区分尚未加载和成功空结果，读写都隔离页面修改', () => {
  const cache = createResponseCache();
  assert.equal(cache.read('/channels'), undefined);
  const channels = [];
  cache.write('/channels', undefined, channels, cache.generation());
  channels.push({ id: 1 });
  assert.deepEqual(cache.read('/channels'), []);
  cache.read('/channels').push({ id: 2 });
  assert.deepEqual(cache.read('/channels'), []);
});

test('查询条件和页码隔离，参数顺序不影响命中', () => {
  const cache = createResponseCache();
  cache.write('/models', { page: 1, page_size: 24, name: 'claude' }, { data: [{ id: 1 }] }, cache.generation());
  assert.deepEqual(cache.read('/models', { name: 'claude', page_size: 24, page: 1 }), { data: [{ id: 1 }] });
  assert.equal(cache.read('/models', { page: 2, page_size: 24, name: 'claude' }), undefined);
  assert.equal(cache.read('/models', { page: 1, page_size: 24, name: 'gpt' }), undefined);
  assert.equal(cache.read('/logs', { page: 1, page_size: 24, name: 'claude' }), undefined);
});

test('写操作失效后，保存前发出的慢请求不能复活旧快照', () => {
  const cache = createResponseCache();
  const oldGeneration = cache.generation();
  cache.write('/groups', undefined, [{ id: 1 }], oldGeneration);
  cache.write('/apikey', undefined, { key: 'old' }, oldGeneration);
  cache.invalidate();
  assert.equal(cache.read('/groups'), undefined);
  assert.equal(cache.read('/apikey'), undefined);
  cache.write('/groups', undefined, [{ id: 1 }], oldGeneration);
  assert.equal(cache.read('/groups'), undefined);
  cache.write('/groups', undefined, [{ id: 2 }], cache.generation());
  assert.deepEqual(cache.read('/groups'), [{ id: 2 }]);
});

test('缓存过期和容量限制会释放快照', () => {
  let time = 100;
  const cache = createResponseCache({ maxEntries: 2, maxAge: 500, now: () => time });
  cache.write('/channels', undefined, [], cache.generation());
  time = 599;
  assert.deepEqual(cache.read('/channels'), []);
  time = 600;
  assert.equal(cache.read('/channels'), undefined);
  for (const page of [1, 2, 3]) cache.write('/models', { page }, { data: [page] }, cache.generation());
  assert.equal(cache.read('/models', { page: 1 }), undefined);
  assert.deepEqual(cache.read('/models', { page: 2 }), { data: [2] });
  assert.deepEqual(cache.read('/models', { page: 3 }), { data: [3] });
});

test('仅缓存页面入口白名单，不保留渠道明文详情或诊断响应', () => {
  const cache = createResponseCache();
  for (const url of ['/channels/1', '/logs/attempts', '/channels/test']) {
    cache.write(url, undefined, { secret: 'private' }, cache.generation());
    assert.equal(cache.read(url), undefined);
  }
});
