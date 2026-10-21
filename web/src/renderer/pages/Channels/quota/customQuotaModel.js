// 只处理配置草稿；金额计算和响应解释由后端适配器完成。
export const customItem = (index = 1) => ({ id: `balance_${index}`, label: '余额', kind: 'balance', scope: 'account', unit: { kind: 'money', code: '' }, remaining_path: '/data/balance' });
export const defaultCustom = () => ({ request: { method: 'GET', path: '/user/balance', headers: { Authorization: 'Bearer {{token}}' }, body: '' }, items: [customItem()] });
export const cloneConfig = (value) => JSON.parse(JSON.stringify(value));
export function customEditors(config) {
  return {
    headers: JSON.stringify(config.request?.headers || {}, null, 2),
    success: config.success?.equals_json ?? 'true',
    unlimited: Object.fromEntries((config.items || []).map((item) => [item.id, item.unlimited?.equals_json ?? 'true'])),
  };
}
function parseJSON(text, label) {
  try { return JSON.parse(text); } catch { throw new Error(`${label}不是有效 JSON，请先修正配置。`); }
}
function scalar(text, label) {
  const value = parseJSON(text, label);
  if (value !== null && typeof value === 'object') throw new Error(`${label}必须是 JSON 字符串、数字、布尔值或 null。`);
  return text.trim();
}
export function customPayload(config, editors) {
  const result = cloneConfig(config);
  result.request.headers = parseJSON(editors.headers, '请求头');
  if (!result.request.headers || typeof result.request.headers !== 'object' || Array.isArray(result.request.headers) || Object.values(result.request.headers).some((value) => typeof value !== 'string')) throw new Error('请求头必须是 JSON 对象，且每个值均为字符串。');
  if (result.request.method === 'GET') result.request.body = '';
  else if (result.request.body?.trim()) parseJSON(result.request.body, 'POST 请求体');
  if (result.success) result.success.equals_json = scalar(editors.success, '成功条件比较值');
  for (const item of result.items) {
    if (!item.unit?.code?.trim()) throw new Error('请为每条额度填写单位代码，不会自动猜测币种。');
    item.unit.code = item.unit.code.trim();
    if (item.divisor) {
      if (!/^(?:\d+(?:\.\d+)?|\.\d+)$/.test(item.divisor) || !/[1-9]/.test(item.divisor)) throw new Error('换算除数必须是大于 0 的十进制字符串。');
      if (!item.original_unit?.code?.trim()) throw new Error('启用换算时必须填写原始单位代码。');
    } else delete item.original_unit;
    if (item.unlimited) item.unlimited.equals_json = scalar(editors.unlimited[item.id] ?? 'true', '无限条件比较值');
  }
  return result;
}

// 无鉴权切换只去掉 Token 占位头；其他请求头及未完成的 JSON 编辑保留。
export function headersWithoutToken(text) {
  try {
    const headers = JSON.parse(text);
    if (headers && typeof headers === 'object' && !Array.isArray(headers)) return JSON.stringify(Object.fromEntries(Object.entries(headers).filter(([, value]) => typeof value !== 'string' || !value.includes('{{token}}'))), null, 2);
  } catch { /* 无效草稿由提交校验显示，不丢失输入。 */ }
  return text;
}
