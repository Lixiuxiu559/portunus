// ~/.codex/auth.json 源码的 OPENAI_API_KEY 读取与点改（纯函数，node --test 覆盖）。
// API Key 框与 auth.json 源码编辑器共享同一份文本：框里改 = 点改源码里的值，
// 保存时整写落盘；tokens 等登录材料只存在于源码里，点改绝不触碰。

/** 读 OPENAI_API_KEY 的字符串值；缺失、非字符串、非法 JSON、顶层非对象返回 ''。 */
export function readAuthApiKey(t) {
  let obj;
  try {
    obj = JSON.parse(t);
  } catch {
    return '';
  }
  return obj && typeof obj === 'object' && !Array.isArray(obj) && typeof obj.OPENAI_API_KEY === 'string'
    ? obj.OPENAI_API_KEY
    : '';
}

/**
 * 点改 OPENAI_API_KEY 的值：已有键原位替换（保留格式），缺失则 JSON round-trip 插入。
 * 空串显式置空（键保留、值为空，可见可改）。非法 JSON / 顶层非对象原样返回不动。
 */
export function applyAuthApiKey(t, key) {
  const re = /("OPENAI_API_KEY"\s*:\s*)"[^"]*"/;
  if (re.test(t)) {
    const escaped = key.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
    return t.replace(re, `$1"${escaped}"`);
  }
  let obj;
  try {
    obj = JSON.parse(t);
  } catch {
    return t;
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return t;
  obj.OPENAI_API_KEY = key;
  return JSON.stringify(obj, null, 2) + '\n';
}
