import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildServiceExamples, selectServiceGroup } from './serviceAccess.js';

test('显式分组链接严格匹配，缺失或空名称不会悄悄选择其他分组', () => {
  const groups = [{ name: 'coding' }, { name: 'daily' }];
  assert.equal(selectServiceGroup(groups, 'daily', 'coding'), 'daily');
  assert.equal(selectServiceGroup(groups, 'deleted', 'coding'), '');
  assert.equal(selectServiceGroup(groups, '', 'coding'), '');
  assert.equal(selectServiceGroup(groups, null, 'daily'), 'daily');
  assert.equal(selectServiceGroup(groups, null, 'deleted'), 'coding');
  assert.equal(selectServiceGroup([], null), '');
});

// 提取命令的 shell 单引号参数，确认特殊字符未破坏可复制的请求体。
function unquote(value) { return value.slice(1, -1).replaceAll("'\\''", "'"); }
for (const stream of [false, true]) {
  test(`三协议示例使用实际地址、分组、低预算和正确字段，stream=${stream}`, () => {
    const apiKey = "pk-'key";
    const model = "研发'\\\n组";
    const examples = buildServiceExamples({ serverUrl: 'http://127.0.0.1:13060///', apiKey, model, stream });
    assert.deepEqual(examples.map((e) => e.path), ['/v1/chat/completions', '/v1/responses', '/v1/messages']);
    for (const example of examples) {
      assert.ok(example.code.includes(`http://127.0.0.1:13060${example.path}`));
      assert.ok(example.code.includes("Authorization: Bearer pk-'\\''key"));
      const body = JSON.parse(unquote(example.code.split('  -d ')[1]));
      assert.equal(body.model, model);
      assert.equal(body.stream, stream);
      assert.equal(body[example.id === 'responses' ? 'max_output_tokens' : 'max_tokens'], 128);
      assert.equal(body.max_completion_tokens, undefined);
      assert.equal(example.code.startsWith('curl -N'), stream);
    }
    assert.ok(examples[2].code.includes('anthropic-version: 2023-06-01'));
  });
}

test('缺少令牌或分组仅生成占位符，调用示例不会包含 undefined', () => {
  const examples = buildServiceExamples({ serverUrl: 'http://127.0.0.1:3060' });
  for (const example of examples) {
    assert.ok(example.code.includes('<PORTUNUS_API_KEY>'));
    assert.ok(example.code.includes('<分组名称>'));
    assert.ok(!example.code.includes('undefined'));
  }
});
