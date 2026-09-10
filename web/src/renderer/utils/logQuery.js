// 日志页查询参数构建（纯函数，node --test 覆盖）。
// 契约：start_time/end_time 必须是带时区偏移的 RFC3339。heroui DateRangePicker 给出的是
// CalendarDateTime（本地墙上时间），其 toString() 无偏移，后端 time.Parse(RFC3339) 会解析
// 失败并静默丢弃条件——必须先经 toDate(本地时区) 转成真实时刻再 toISOString()。

/** 把筛选状态折叠成 /api/logs 的 query 参数；空筛选不出现在结果里。 */
export function buildLogParams(filters, page, pageSize) {
  const params = { page, page_size: pageSize };
  if (filters.model_name) params.model_name = filters.model_name;
  if (filters.success !== 'all') params.success = filters.success;
  if (filters.range?.start) params.start_time = toRFC3339(filters.range.start);
  if (filters.range?.end) params.end_time = toRFC3339(filters.range.end);
  return params;
}

// CalendarDateTime（或带时区的 ZonedDateTime）→ 带偏移的 RFC3339 字符串
function toRFC3339(value) {
  const d = typeof value.toDate === 'function' ? value.toDate(getLocalTimeZone()) : new Date(value.toString());
  return d.toISOString();
}

function getLocalTimeZone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}
