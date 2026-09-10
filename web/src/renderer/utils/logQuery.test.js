import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CalendarDateTime } from '@internationalized/date';
import { buildLogParams } from './logQuery.js';

// 日志查询参数构建回归测试（node --test，运行：cd web && pnpm test）。
// 契约：start_time/end_time 必须是带时区偏移的 RFC3339——后端 time.Parse(RFC3339)
// 对无偏移的 "2026-09-10T00:00:00" 解析失败且静默丢弃条件（时间范围失效的根因），
// 所以这里逐字符断言偏移存在，Date.parse 能过还不够（它把无偏移串当本地时间）。

const RANGE = {
  start: new CalendarDateTime(2026, 9, 10, 8, 30, 0),
  end: new CalendarDateTime(2026, 9, 10, 23, 59, 0),
};

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

test('buildLogParams：时间值带时区偏移（RFC3339），Go time.Parse 可解析', () => {
  const p = buildLogParams({ model_name: '', success: 'all', range: RANGE }, 1, 20);
  assert.match(p.start_time, RFC3339);
  assert.match(p.end_time, RFC3339);
  // 同一刻换算为 UTC 后 Instant 一致（本地时区语义不丢）
  assert.equal(new Date(p.start_time).getTime(), new Date('2026-09-10T08:30:00+08:00').getTime());
});

test('buildLogParams：其余筛选与分页映射，空值省略', () => {
  const p = buildLogParams({ model_name: 'glm', success: 'false', range: RANGE }, 3, 20);
  assert.equal(p.model_name, 'glm');
  assert.equal(p.success, 'false');
  assert.equal(p.page, 3);
  assert.equal(p.page_size, 20);
  assert.ok(p.start_time && p.end_time);

  const empty = buildLogParams({ model_name: '', success: 'all', range: null }, 1, 20);
  assert.deepEqual(
    Object.keys(empty).sort(),
    ['page', 'page_size'],
    '无筛选时只应有分页参数',
  );

  const half = buildLogParams({ model_name: '', success: 'all', range: { start: RANGE.start, end: null } }, 1, 20);
  assert.ok(half.start_time);
  assert.equal('end_time' in half, false);
});

test('buildLogParams：两端时间单调（start <= end）', () => {
  const p = buildLogParams({ model_name: '', success: 'all', range: RANGE }, 1, 20);
  assert.ok(new Date(p.start_time) <= new Date(p.end_time));
});
