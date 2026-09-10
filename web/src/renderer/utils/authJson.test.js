import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readAuthApiKey, applyAuthApiKey } from './authJson.js';

// auth.json 源码的 OPENAI_API_KEY 读取与点改回归测试（node --test，运行：cd web && pnpm test）。
// 语义：API Key 框与 auth.json 源码共享同一份文本——框里改 = 点改源码里的值，保存整写落盘；
// tokens / last_refresh 等登录材料只随源码原样保留，绝不因点改而丢失。

test('readAuthApiKey：读 OPENAI_API_KEY，缺失/非法返回空串', () => {
  assert.equal(readAuthApiKey('{\n  "OPENAI_API_KEY": "sk-abc"\n}\n'), 'sk-abc');
  assert.equal(readAuthApiKey('{\n  "tokens": {}}\n'), '');
  assert.equal(readAuthApiKey('not json'), '');
  assert.equal(readAuthApiKey('[]'), '');
});

test('applyAuthApiKey：已有键原位替换，其余内容逐字节保留', () => {
  const t = '{\n  "OPENAI_API_KEY": "sk-old",\n  "tokens": {"a": 1}\n}\n';
  const r = applyAuthApiKey(t, 'sk-new');
  assert.equal(r, t.replace('sk-old', 'sk-new'));
});

test('applyAuthApiKey：缺键插入（保留其他键与原有缩进风格之外的内容）', () => {
  const t = '{\n  "tokens": {"a": 1}\n}\n';
  const r = applyAuthApiKey(t, 'sk-new');
  assert.equal(JSON.parse(r).OPENAI_API_KEY, 'sk-new');
  assert.deepEqual(JSON.parse(r).tokens, { a: 1 });
});

test('applyAuthApiKey：空串显式置空（键保留、值为空，可见可改）', () => {
  const r = applyAuthApiKey('{\n  "OPENAI_API_KEY": "sk-old"\n}\n', '');
  assert.match(r, /"OPENAI_API_KEY": ""/);
});

test('applyAuthApiKey：非法 JSON / 顶层非对象 → 原样返回不动', () => {
  assert.equal(applyAuthApiKey('not json', 'sk-new'), 'not json');
  assert.equal(applyAuthApiKey('[1,2]', 'sk-new'), '[1,2]');
});
