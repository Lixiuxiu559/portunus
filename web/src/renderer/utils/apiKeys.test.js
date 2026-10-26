import { test } from 'node:test';
import assert from 'node:assert/strict';
import { selectAPIKey, apiKeyLabel, logAPIKeyLabel } from './apiKeys.js';

const keys = [
  { id: 1, name: '旧工具', revoked: true, deleted_at: null },
  { id: 2, name: 'Claude Code', revoked: false, deleted_at: null },
  { id: 3, name: 'Codex', revoked: false, deleted_at: null },
  { id: 4, name: 'Codex', revoked: false, deleted_at: '2026-10-26T00:00:00Z' },
];

test('接入保留选中 Key，禁用或删除后回退有效 Key，启用后可再次选中', () => {
  assert.equal(selectAPIKey(keys, '3').id, 3);
  assert.equal(selectAPIKey(keys, 1).id, 2);
  assert.equal(selectAPIKey(keys, 4).id, 2);
  assert.equal(selectAPIKey(keys, 99).id, 2);
  assert.equal(selectAPIKey(keys.map((key) => ({ ...key, revoked: true })), 2), null);
  assert.equal(selectAPIKey(keys.map((key) => key.id === 1 ? { ...key, revoked: false } : key), 1).id, 1);
  assert.equal(selectAPIKey([], 1), null);
});

test('筛选只显示当前名称，同名 Key 仍按 ID 独立选择', () => {
  assert.equal(apiKeyLabel('3', keys), 'Codex');
  assert.equal(apiKeyLabel(1, keys), '旧工具');
  assert.equal(apiKeyLabel(4, keys), 'Codex');
  assert.equal(apiKeyLabel(20, keys), '未记录名称');
  assert.equal(apiKeyLabel(0, keys), '未记录 API Key');
});

test('日志名称只用调用时快照，重命名、禁用、删除不替换快照；旧日志明确未知', () => {
  const historical = { api_key_id: 3, api_key_name: '旧名称' };
  assert.equal(logAPIKeyLabel(historical), '旧名称');
  assert.equal(logAPIKeyLabel({ api_key_id: 3 }), '未记录调用时名称');
  assert.equal(logAPIKeyLabel({ api_key_id: 3, api_key_name: '' }), '未记录调用时名称');
  assert.equal(logAPIKeyLabel({ api_key_id: 0 }), '未记录 API Key');
});
