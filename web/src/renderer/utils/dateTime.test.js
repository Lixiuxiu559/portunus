import test from 'node:test';
import assert from 'node:assert/strict';
import { formatDate, formatDateTime, formatTime, formatRelativeTime } from './dateTime.js';

test('日期与完整时间使用同一用户本地时区，时间固定24小时并保留秒', () => {
  const date = new Date(2026, 9, 27, 23, 59, 58);
  assert.equal(formatDate(date), '2026/10/27');
  assert.equal(formatTime(date), '23:59:58');
  assert.equal(formatDateTime(date), '2026/10/27 23:59:58');
  assert.equal(formatDateTime(date.toISOString()), formatDateTime(date));
  assert.equal(formatDateTime(date.getTime()), formatDateTime(date));
  assert.equal(formatTime(new Date(2026, 9, 28)), '00:00:00');
});

test('空值和非法时间不显示epoch，调用方可保留自身的缺失文案', () => {
  for (const value of [undefined, null, '', '  ', false, {}, 'invalid', '1790000000', NaN, Infinity, new Date(NaN)]) {
    assert.equal(formatDate(value), '未记录');
    assert.equal(formatTime(value), '未记录');
    assert.equal(formatDateTime(value, '尚无记录'), '尚无记录');
    assert.equal(formatRelativeTime(value, Date.now(), '尚未查询'), '尚未查询');
  }
  assert.equal(formatDateTime(0), formatDateTime(new Date(0)));
});

test('相对时间统一分钟小时天阈值，未来时间钳制刚刚，30天起显示日期', () => {
  const now = new Date(2026, 9, 28, 0, 1, 0).getTime();
  for (const [elapsed, expected] of [
    [-60_000, '刚刚'], [0, '刚刚'], [59_999, '刚刚'], [60_000, '1 分钟前'],
    [120_000, '2 分钟前'], [60 * 60_000, '1 小时前'], [24 * 60 * 60_000, '1 天前'],
    [29 * 24 * 60 * 60_000, '29 天前'],
  ]) assert.equal(formatRelativeTime(new Date(now - elapsed), now), expected);
  const thirtyDaysAgo = new Date(now - 30 * 24 * 60 * 60_000);
  assert.equal(formatRelativeTime(thirtyDaysAgo, now), formatDate(thirtyDaysAgo));
  assert.equal(formatRelativeTime(new Date(now), NaN, '未知'), '未知');
});
