// Codex config.toml「激活 provider 段」的定位与点改（纯函数，node --test 覆盖）。
// 语义对齐 Codex CLI 0.145.0：顶层 model_provider 指向的 [model_providers.<id>] 是激活段，
// base_url / experimental_bearer_token 只在激活段内生效，顶层没有 bearer token 键；
// 键语义依据 docs/research/cc-switch-codex-config.md §3。
// 只做正则点改、保留用户原有格式与注释（smol-toml round-trip 会重排全文，故不用）。

// TOML 基础字符串值的转义（反斜杠 + 双引号；URL/令牌不含控制字符，够用）
function escToml(s) {
  return s.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

// escToml 的逆：只还原它写得出的两种转义，\n \t 等手写转义保持原样
function unescToml(s) {
  return s.replace(/\\(["\\])/g, '$1');
}

/** 顶层 model_provider 的值；未设置返回 null（Codex 默认 "openai"，此处无从得知）。 */
export function readActiveProviderId(t) {
  const m = t.match(/^model_provider\s*=\s*"([^"]*)"/m);
  return m ? m[1] : null;
}

/** 激活段行范围：段头行之后，到下一个段头（含子表 [x.y] 与数组表 [[x]]）或文件尾。
 * 已知边界：段内多行字符串/数组的续行以 [ 开头时会被误判为段尾（正则点改方案的局限）。 */
function activeProviderSection(t) {
  const id = readActiveProviderId(t);
  if (id == null) return null;
  const esc = id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const hm = t.match(new RegExp(`^\\[model_providers\\.${esc}\\][ \\t]*(?:#.*)?$`, 'm'));
  if (!hm) return null;
  const headerEnd = hm.index + hm[0].length;
  const next = t.slice(headerEnd).search(/^\[/m);
  const end = next === -1 ? t.length : headerEnd + next;
  return { headerEnd, end };
}

/** 读激活段内 key 的双引号字符串值；无激活段或无该键返回 ''。 */
export function readCodexKey(t, key) {
  const sec = activeProviderSection(t);
  if (!sec) return '';
  const m = t
    .slice(sec.headerEnd, sec.end)
    .match(new RegExp(`^[ \\t]*${key}[ \\t]*=[ \\t]*"((?:[^"\\\\]|\\\\.)*)"`, 'm'));
  return m ? unescToml(m[1]) : '';
}

/**
 * 激活段内点改 key：已有则原位替换，缺失则插到段头之后；无激活段原样返回。
 * 返回 { text, ok } —— ok=false 表示文件里没有可锚定的激活段，调用方须明确警告，不可假成功。
 */
export function upsertCodexKey(t, key, value) {
  const sec = activeProviderSection(t);
  if (!sec) return { text: t, ok: false };
  const seg = t.slice(sec.headerEnd, sec.end);
  const re = new RegExp(`^([ \\t]*${key}[ \\t]*=[ \\t]*)"[^"]*"`, 'gm');
  if (re.test(seg)) {
    // 用函数替换，避免 value 里的 $ 序列被当成捕获组引用
    const newSeg = seg.replace(re, (_, p1) => `${p1}"${escToml(value)}"`);
    return { text: t.slice(0, sec.headerEnd) + newSeg + t.slice(sec.end), ok: true };
  }
  const line = `${key} = "${escToml(value)}"\n`;
  // 插到段头行尾换行之后；段头是末行且无换行时补一个
  const nl = t.slice(sec.headerEnd, sec.headerEnd + 2);
  const at = sec.headerEnd + (nl === '\r\n' ? 2 : nl[0] === '\n' ? 1 : 0);
  const prefix = at === sec.headerEnd ? '\n' : '';
  return { text: t.slice(0, at) + prefix + line + t.slice(at), ok: true };
}
