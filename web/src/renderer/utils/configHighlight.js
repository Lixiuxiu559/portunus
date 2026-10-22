// JSON 语法高亮与校验（沿用 v0.4.1 client-config-editor 的实现，只保留 JSON）。
// 高亮输出带 <span class="tk-*"> 的 HTML；对应配色见 index.css 的 .tk-* 规则，
// 组件通过 dangerouslySetInnerHTML 渲染，因此这里必须先转义 & < >。

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

/** 高亮配置文本（当前只支持 JSON）。 */
export function highlight(text) {
  return highlightJSON(String(text ?? ''));
}

/** 校验：返回 { ok, msg, line? }。 */
export function validate(text) {
  const src = String(text ?? '');
  if (!src.trim()) return { ok: true, msg: '文件为空' };
  try {
    JSON.parse(src);
    return { ok: true, msg: 'JSON 语法有效' };
  } catch (e) {
    const m = e.message.match(/position\s*(\d+)/i);
    const line = m ? src.slice(0, parseInt(m[1], 10)).split('\n').length : null;
    // Node 的错误信息可能带整段原文，只保留原因，避免把配置内容（含令牌）带进页面提示
    const reason = String(e.message || 'JSON 语法错误').split('"')[0].split(' at ')[0].replace(/[,:\s]+$/, '');
    return {
      ok: false,
      line,
      msg: (line ? '第 ' + line + ' 行 · ' : '') + (reason || 'JSON 语法错误'),
    };
  }
}

/** 格式化：JSON 缩进 2 空格；解析失败抛错。 */
export function format(text) {
  return JSON.stringify(JSON.parse(text), null, 2) + '\n';
}
