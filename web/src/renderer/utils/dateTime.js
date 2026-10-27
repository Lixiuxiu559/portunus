// 所有页面使用用户本地时区；只统一格式，不改变查询时间或事件时间的含义。
const dateFormatter = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' });
const timeFormatter = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' });

function validDate(value) {
  if (value == null || (typeof value !== 'string' && typeof value !== 'number' && !(value instanceof Date))) return null;
  if (typeof value === 'string' && (!value.trim() || /^\d+$/.test(value.trim()))) return null;
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date : null;
}

export function formatDate(value, fallback = '未记录') {
  const date = validDate(value);
  return date ? dateFormatter.format(date) : fallback;
}

export function formatTime(value, fallback = '未记录') {
  const date = validDate(value);
  return date ? timeFormatter.format(date) : fallback;
}

export function formatDateTime(value, fallback = '未记录') {
  const date = validDate(value);
  return date ? `${dateFormatter.format(date)} ${timeFormatter.format(date)}` : fallback;
}

export function formatRelativeTime(value, now = Date.now(), fallback = '未记录') {
  const date = validDate(value);
  if (!date || !Number.isFinite(now)) return fallback;
  const minutes = Math.max(0, Math.floor((now - date.getTime()) / 60_000));
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)} 小时前`;
  const days = Math.floor(minutes / 1440);
  return days < 30 ? `${days} 天前` : formatDate(date, fallback);
}
