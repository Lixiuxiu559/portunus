/** 接入只选择未禁用、未删除的 Key。 */
export function selectAPIKey(keys, preferredID) {
  const active = keys.filter((key) => !key.revoked && !key.deleted_at);
  return active.find((key) => String(key.id) === String(preferredID)) || active[0] || null;
}

/** 筛选显示当前名称，选项值仍使用稳定 ID。 */
export function apiKeyLabel(id, keys) {
  if (!(Number(id) > 0)) return '未记录 API Key';
  const key = keys.find((item) => String(item.id) === String(id));
  return key?.name || '未记录名称';
}

/** 日志只使用调用时名称快照，不能用当前名称推测历史身份。 */
export function logAPIKeyLabel(record) {
  if (record.api_key_name) return record.api_key_name;
  if (Number(record.api_key_id) > 0) return '未记录调用时名称';
  return '未记录 API Key';
}
