// Claude Code 配置（~/.claude/settings.json）的纯文本读写工具。
// 页面只持有原始文本；这里负责把文本解析成表单投影、把表单写回文本。
// 所有写回都返回 { text, ok, reason }：解析失败时 ok=false，调用方必须提示而不是假装成功。

// Claude Code 模型映射槽位（沿用 v0.4.1 的 six-slot 结构）。
export const MODEL_SLOTS = [
  { id: 'sonnet', label: 'Sonnet', key: 'ANTHROPIC_DEFAULT_SONNET_MODEL', keyName: 'ANTHROPIC_DEFAULT_SONNET_MODEL_NAME', has1m: true },
  { id: 'opus', label: 'Opus', key: 'ANTHROPIC_DEFAULT_OPUS_MODEL', keyName: 'ANTHROPIC_DEFAULT_OPUS_MODEL_NAME', has1m: true },
  { id: 'fable', label: 'Fable', key: 'ANTHROPIC_DEFAULT_FABLE_MODEL', keyName: 'ANTHROPIC_DEFAULT_FABLE_MODEL_NAME', has1m: true },
  { id: 'haiku', label: 'Haiku', key: 'ANTHROPIC_DEFAULT_HAIKU_MODEL', keyName: 'ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME', has1m: false },
  { id: 'subagent', label: 'Subagent', key: 'CLAUDE_CODE_SUBAGENT_MODEL', nameDash: true, has1m: true },
  { id: 'default', label: '默认兜底模型', key: 'ANTHROPIC_MODEL', has1m: true, solo: true },
];

/** 解析槽位值：末尾 [1M] 表示声明 1M 上下文能力。 */
export function parseModel(value) {
  if (typeof value === 'string' && value.slice(-4) === '[1M]') {
    return { base: value.slice(0, -4), onem: true };
  }
  return { base: value || '', onem: false };
}

// 正则读取用于「编辑到一半 JSON 不合法」时仍能显示连接字段。
export function readClaudeBase(text) {
  const m = String(text).match(/"ANTHROPIC_BASE_URL"\s*:\s*"([^"]*)"/);
  return m ? m[1] : '';
}

export function readClaudeToken(text) {
  const m = String(text).match(/"ANTHROPIC_AUTH_TOKEN"\s*:\s*"([^"]*)"/);
  return m ? m[1] : '';
}

export function readClaudeEnv(text) {
  try {
    const obj = JSON.parse(text);
    return obj && obj.env && typeof obj.env === 'object' && !Array.isArray(obj.env) ? obj.env : {};
  } catch {
    return {};
  }
}

/** 把 env 反向解析成六个槽位的表单值。 */
export function readClaudeModels(text) {
  const env = readClaudeEnv(text);
  const models = {};
  MODEL_SLOTS.forEach((slot) => {
    const parsed = parseModel(env[slot.key]);
    models[slot.id] = {
      base: parsed.base,
      onem: parsed.onem,
      name: slot.keyName ? env[slot.keyName] || '' : '',
    };
  });
  return models;
}

function parseObject(text) {
  const obj = JSON.parse(text);
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) {
    throw new Error('settings.json 顶层必须是 JSON 对象');
  }
  return obj;
}

/**
 * 写回单个 env 键（空值删除该键）。解析失败返回 null 交给上层报告。
 * 只改 env，其他顶层字段（MCP、权限等）原样保留。
 */
export function setClaudeEnv(text, key, value) {
  let obj;
  try {
    obj = parseObject(text);
  } catch {
    return null;
  }
  const env = obj.env && typeof obj.env === 'object' && !Array.isArray(obj.env) ? obj.env : {};
  if (value) env[key] = value;
  else delete env[key];
  obj.env = env;
  return JSON.stringify(obj, null, 2) + '\n';
}

function applyClaudeKey(text, key, value) {
  const next = setClaudeEnv(text, key, value);
  return next === null
    ? { text, ok: false, reason: 'settings.json 不是合法 JSON，未能写入' }
    : { text: next, ok: true, reason: '' };
}

export function applyClaudeBaseUrl(text, baseUrl) {
  return applyClaudeKey(text, 'ANTHROPIC_BASE_URL', baseUrl);
}

export function applyClaudeToken(text, token) {
  return applyClaudeKey(text, 'ANTHROPIC_AUTH_TOKEN', token);
}

/** 把六个槽位写回 env：请求模型 + [1M]、显示名；空值删除对应键。 */
export function applyClaudeModels(text, models) {
  let obj;
  try {
    obj = parseObject(text);
  } catch {
    return { text, ok: false, reason: 'settings.json 不是合法 JSON，未能写入' };
  }
  const env = obj.env && typeof obj.env === 'object' && !Array.isArray(obj.env) ? obj.env : {};
  MODEL_SLOTS.forEach((slot) => {
    const m = models[slot.id] || {};
    const value = m.base ? m.base + (m.onem ? '[1M]' : '') : '';
    if (value) env[slot.key] = value;
    else delete env[slot.key];
    if (slot.keyName) {
      if (m.name) env[slot.keyName] = m.name;
      else delete env[slot.keyName];
    }
  });
  obj.env = env;
  return { text: JSON.stringify(obj, null, 2) + '\n', ok: true, reason: '' };
}

/** 校验编辑器内容：返回 { ok, message, line? }。 */
export function validateClaudeSettings(text) {
  if (!String(text).trim()) return { ok: true, message: '文件为空' };
  try {
    parseObject(text);
    return { ok: true, message: 'JSON 语法有效' };
  } catch (e) {
    if (e.message === 'settings.json 顶层必须是 JSON 对象') {
      return { ok: false, message: e.message };
    }
    const m = e.message.match(/position\s*(\d+)/i);
    const line = m ? String(text).slice(0, parseInt(m[1], 10)).split('\n').length : null;
    // Node 的错误信息可能带整段原文，只保留原因，避免把配置内容（含令牌）带进页面提示
    const reason = String(e.message || 'JSON 语法错误').split('"')[0].split(' at ')[0].replace(/[,:\s]+$/, '');
    return { ok: false, line, message: (line ? `第 ${line} 行 · ` : '') + (reason || 'JSON 语法错误') };
  }
}

/** base_url 是否指向当前 Portunus 实例（避免沿用 3060/13060 的旧正则）。 */
export function isPortunusBase(base, serverUrl) {
  const normalized = String(base || '').trim().replace(/\/+$/, '');
  const target = String(serverUrl || '').trim().replace(/\/+$/, '');
  if (!normalized || !target) return false;
  return normalized === target || normalized === `${target}/v1`;
}
