// JSON / TOML 语法高亮与校验（复刻 client-config-editor.html 的实现）。
// 高亮输出带 <span class="tk-*"> 的 HTML；对应配色见 index.css 的 .tk-* 规则。

import { parse as parseToml, stringify as stringifyToml } from 'smol-toml';

function esc(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function highlightJSON(src) {
  let out = '';
  let i = 0;
  const n = src.length;
  while (i < n) {
    const ch = src[i];
    if (ch === '"') {
      let j = i + 1;
      while (j < n && src[j] !== '"') {
        if (src[j] === '\\') j++;
        j++;
      }
      if (src[j] === '"') j++;
      out += '<span class="tk-str">' + esc(src.slice(i, j)) + '</span>';
      i = j;
      continue;
    }
    if (/[0-9-]/.test(ch)) {
      const m = src.slice(i).match(/^-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/);
      if (m) {
        out += '<span class="tk-num">' + esc(m[0]) + '</span>';
        i += m[0].length;
        continue;
      }
    }
    const kw = src.slice(i).match(/^(true|false|null)/);
    if (kw) {
      out += '<span class="tk-kw">' + kw[0] + '</span>';
      i += kw[0].length;
      continue;
    }
    out += esc(ch);
    i++;
  }
  return out;
}

function tokenizeValue(v) {
  let out = '';
  let i = 0;
  const n = v.length;
  while (i < n) {
    const ch = v[i];
    if (ch === '"' || ch === "'") {
      const q = ch;
      let j = i + 1;
      while (j < n && v[j] !== q) {
        if (v[j] === '\\') j++;
        j++;
      }
      if (v[j] === q) j++;
      out += '<span class="tk-str">' + esc(v.slice(i, j)) + '</span>';
      i = j;
      continue;
    }
    if (v[i] === '#') {
      out += '<span class="tk-com">' + esc(v.slice(i)) + '</span>';
      break;
    }
    if (/[0-9]/.test(ch)) {
      const m = v.slice(i).match(/^\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/);
      if (m) {
        out += '<span class="tk-num">' + esc(m[0]) + '</span>';
        i += m[0].length;
        continue;
      }
    }
    const kw = v.slice(i).match(/^(true|false)/);
    if (kw) {
      out += '<span class="tk-kw">' + kw[0] + '</span>';
      i += kw[0].length;
      continue;
    }
    out += esc(ch);
    i++;
  }
  return out;
}

function highlightTOML(src) {
  return src
    .split('\n')
    .map((line) => {
      const comment = line.match(/^\s*(#.*)$/);
      if (comment) return '<span class="tk-com">' + esc(line) + '</span>';
      const sec = line.match(/^(\s*)(\[\[?[^\]]+\]\]?)(\s*)$/);
      if (sec) return esc(sec[1]) + '<span class="tk-sec">' + esc(sec[2]) + '</span>' + esc(sec[3]);
      const kv = line.match(/^(\s*)([A-Za-z0-9_.-]+)(\s*=\s*)(.*)$/);
      if (kv) {
        return (
          esc(kv[1]) +
          '<span class="tk-key">' + esc(kv[2]) + '</span>' +
          esc(kv[3]) +
          tokenizeValue(kv[4])
        );
      }
      return esc(line);
    })
    .join('\n');
}

/** 按语言高亮：lang 为 'json' | 'toml'。 */
export function highlight(text, lang) {
  return lang === 'json' ? highlightJSON(text) : highlightTOML(text);
}

/** 校验：返回 { ok, msg, line? }。JSON 用内建解析，TOML 用 smol-toml 精确解析。 */
export function validate(text, lang) {
  if (!text.trim()) return { ok: true, msg: '文件为空' };
  if (lang === 'json') {
    try {
      JSON.parse(text);
      return { ok: true, msg: 'JSON 语法有效' };
    } catch (e) {
      const m = e.message.match(/position\s*(\d+)/i);
      const line = m ? text.slice(0, parseInt(m[1], 10)).split('\n').length : null;
      return {
        ok: false,
        line,
        msg: (line ? '第 ' + line + ' 行 · ' : '') + e.message.split(' at ')[0],
      };
    }
  }
  try {
    parseToml(text);
    return { ok: true, msg: 'TOML 语法有效' };
  } catch (e) {
    return { ok: false, msg: 'TOML 语法错误：' + (e.message || String(e)) };
  }
}

/** 格式化：JSON 缩进 2 空格；TOML 用 smol-toml 重新序列化。解析失败抛错。 */
export function format(text, lang) {
  if (lang === 'json') {
    return JSON.stringify(JSON.parse(text), null, 2) + '\n';
  }
  return stringifyToml(parseToml(text));
}