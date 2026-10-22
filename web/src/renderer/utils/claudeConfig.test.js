import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyClaudeBaseUrl,
  applyClaudeModels,
  applyClaudeToken,
  isPortunusBase,
  parseModel,
  readClaudeBase,
  readClaudeEnv,
  readClaudeModels,
  readClaudeToken,
  setClaudeEnv,
  validateClaudeSettings,
} from './claudeConfig.js';

const SETTINGS = JSON.stringify({
  env: { ANTHROPIC_BASE_URL: 'http://127.0.0.1:13061', ANTHROPIC_AUTH_TOKEN: 'sk-test' },
  permissions: { allow: ['Bash'] },
  mcpServers: { demo: { command: 'node' } },
}, null, 2);

test('读取连接字段，JSON 非法时正则仍能显示', () => {
  assert.equal(readClaudeBase(SETTINGS), 'http://127.0.0.1:13061');
  assert.equal(readClaudeToken(SETTINGS), 'sk-test');
  assert.equal(readClaudeBase('{"env":{"ANTHROPIC_BASE_URL":"http://x"'), 'http://x');
  assert.equal(readClaudeToken('not json'), '');
});

test('parseModel 识别 [1M] 后缀', () => {
  assert.deepEqual(parseModel('claude-x[1M]'), { base: 'claude-x', onem: true });
  assert.deepEqual(parseModel('claude-x'), { base: 'claude-x', onem: false });
  assert.deepEqual(parseModel(undefined), { base: '', onem: false });
});

test('setClaudeEnv 只改 env 并保留其他顶层字段', () => {
  const next = setClaudeEnv(SETTINGS, 'ANTHROPIC_MODEL', 'coding');
  const obj = JSON.parse(next);
  assert.equal(obj.env.ANTHROPIC_MODEL, 'coding');
  assert.equal(obj.env.ANTHROPIC_AUTH_TOKEN, 'sk-test');
  assert.deepEqual(obj.permissions, { allow: ['Bash'] });
  assert.deepEqual(obj.mcpServers, { demo: { command: 'node' } });
});

test('空值删除 env 键', () => {
  const next = setClaudeEnv(SETTINGS, 'ANTHROPIC_AUTH_TOKEN', '');
  assert.equal('ANTHROPIC_AUTH_TOKEN' in JSON.parse(next).env, false);
});

test('applyClaudeBaseUrl / Token 返回 ok', () => {
  const base = applyClaudeBaseUrl(SETTINGS, 'http://127.0.0.1:13060');
  assert.equal(base.ok, true);
  assert.equal(JSON.parse(base.text).env.ANTHROPIC_BASE_URL, 'http://127.0.0.1:13060');

  const token = applyClaudeToken(SETTINGS, 'sk-new');
  assert.equal(JSON.parse(token.text).env.ANTHROPIC_AUTH_TOKEN, 'sk-new');
});

test('非法 JSON 的写回返回 ok:false 且原文不变', () => {
  const broken = '{"env": {';
  const results = [
    applyClaudeBaseUrl(broken, 'http://x'),
    applyClaudeToken(broken, 'sk'),
    applyClaudeModels(broken, {}),
  ];
  for (const r of results) {
    assert.equal(r.ok, false);
    assert.equal(r.text, broken);
  }
  assert.equal(setClaudeEnv(broken, 'K', 'v'), null);
});

test('applyClaudeModels 写入请求模型、[1M] 与显示名，空值删除', () => {
  const models = {
    sonnet: { base: 'coding', onem: true, name: 'Coding 1M' },
    haiku: { base: '', onem: false, name: '' },
    default: { base: 'daily', onem: false, name: '' },
  };
  const env = JSON.parse(applyClaudeModels('{"env":{}}', models).text).env;
  assert.equal(env.ANTHROPIC_DEFAULT_SONNET_MODEL, 'coding[1M]');
  assert.equal(env.ANTHROPIC_DEFAULT_SONNET_MODEL_NAME, 'Coding 1M');
  assert.equal('ANTHROPIC_DEFAULT_HAIKU_MODEL' in env, false);
  assert.equal(env.ANTHROPIC_MODEL, 'daily');
});

test('readClaudeModels 反向解析六个槽位', () => {
  const text = '{"env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"coding[1M]","ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":"Coding"}}';
  const models = readClaudeModels(text);
  assert.deepEqual(models.opus, { base: 'coding', onem: true, name: 'Coding' });
  assert.deepEqual(models.sonnet, { base: '', onem: false, name: '' });
});

test('validateClaudeSettings 报告语法错误与顶层类型', () => {
  assert.equal(validateClaudeSettings('').ok, true);
  assert.equal(validateClaudeSettings('{"env":{}}').ok, true);
  const bad = validateClaudeSettings('{\n  "env": ,\n}');
  assert.equal(bad.ok, false);
  assert.equal(typeof bad.message, 'string');
  // 错误信息不能回显配置内容（可能含令牌）
  const secret = validateClaudeSettings('{"ANTHROPIC_AUTH_TOKEN":"sk-secret" oops}');
  assert.equal(secret.ok, false);
  assert.equal(secret.message.includes('sk-secret'), false);
  assert.equal(validateClaudeSettings('[1]').message, 'settings.json 顶层必须是 JSON 对象');
});

test('isPortunusBase 只认当前实例地址', () => {
  assert.equal(isPortunusBase('http://127.0.0.1:13061', 'http://127.0.0.1:13061'), true);
  assert.equal(isPortunusBase('http://127.0.0.1:13061/', 'http://127.0.0.1:13061'), true);
  assert.equal(isPortunusBase('http://127.0.0.1:13061/v1', 'http://127.0.0.1:13061'), true);
  assert.equal(isPortunusBase('http://127.0.0.1:13060', 'http://127.0.0.1:13061'), false);
  assert.equal(isPortunusBase('', 'http://127.0.0.1:13061'), false);
  assert.equal(isPortunusBase('http://127.0.0.1:13061', ''), false);
});
