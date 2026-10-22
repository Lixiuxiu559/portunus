import test from 'node:test';
import assert from 'node:assert/strict';
import { format, highlight, validate } from './configHighlight.js';

test('高亮转义 HTML 特殊字符，避免注入', () => {
  const html = highlight('{"a":"<b>&"}');
  assert.ok(!html.includes('<b>'));
  assert.ok(html.includes('&lt;b&gt;&amp;'));
  assert.ok(html.includes('class="tk-str"'));
});

test('高亮数字、关键字与普通文本', () => {
  const html = highlight('{"n": -1.5, "ok": true, "nil": null}');
  assert.ok(html.includes('class="tk-num"'));
  assert.ok(html.includes('class="tk-kw"'));
});

test('校验空文件与合法 JSON', () => {
  assert.deepEqual(validate(''), { ok: true, msg: '文件为空' });
  assert.equal(validate('{"env":{}}').ok, true);
});

test('校验非法 JSON 返回可读信息', () => {
  const r = validate('{"env": }');
  assert.equal(r.ok, false);
  assert.match(r.msg, /Unexpected|JSON/);
  // 错误信息不能回显配置内容（可能含令牌）
  const secret = validate('{"ANTHROPIC_AUTH_TOKEN":"sk-secret" oops}');
  assert.equal(secret.msg.includes('sk-secret'), false);
});

test('格式化输出 2 空格缩进并以换行结尾', () => {
  assert.equal(format('{"env":{"A":"1"}}'), '{\n  "env": {\n    "A": "1"\n  }\n}\n');
  assert.throws(() => format('{'));
});
