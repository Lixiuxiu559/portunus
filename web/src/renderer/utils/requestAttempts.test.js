import test from 'node:test';
import assert from 'node:assert/strict';
import { attemptCoverage, createAttemptLoader } from './requestAttempts.js';

test('未采集和连续序号不能证明调用次数或请求完成；分页不误报序号缺口', () => {
  assert.match(attemptCoverage([], 1, 20), /无法确认调用次数/);
  assert.match(attemptCoverage([{ sequence: 1 }, { sequence: 2 }], 1, 20), /完整性未确认/);
  assert.match(attemptCoverage([{ sequence: 21 }], 2, 20), /当前页序号连续/);
  assert.match(attemptCoverage([{ sequence: 1 }, { sequence: 3 }], 1, 20), /序号有缺口/);
  assert.match(attemptCoverage([{ sequence: 2 }], 1, 20), /序号有缺口/);
});

test('旧请求或旧页晚到不覆盖新结果，传递准确请求 ID 和分页', async () => {
  let resolveOld;
  const calls = [];
  const loader = createAttemptLoader((params) => {
    calls.push(params);
    return params.request_id === 'old' ? new Promise((resolve) => { resolveOld = resolve; }) : Promise.resolve({ total: 21, data: [{ sequence: 21 }] });
  });
  const old = loader.load({ request_id: 'old', page: 1, page_size: 20 });
  assert.deepEqual(await loader.load({ request_id: 'new_%+&', page: 2, page_size: 20 }), { total: 21, data: [{ sequence: 21 }] });
  resolveOld({ total: 1, data: [{ sequence: 1 }] });
  assert.equal(await old, null);
  assert.deepEqual(calls[1], { request_id: 'new_%+&', page: 2, page_size: 20 });
});

test('关闭后旧错误静默，当前查询失败仍可重试', async () => {
  let rejectOld;
  const loader = createAttemptLoader(() => new Promise((_, reject) => { rejectOld = reject; }));
  const old = loader.load({ request_id: 'r' });
  loader.invalidate(); rejectOld(new Error('late error'));
  assert.equal(await old, null);
  const retry = loader.load({ request_id: 'r' });
  rejectOld(new Error('current error'));
  await assert.rejects(retry, /current error/);
});
