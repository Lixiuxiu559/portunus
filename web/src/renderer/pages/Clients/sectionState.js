// 服务接入的树形选中态（纯函数，可单测）。
// URL 是唯一真相：?section=examples|config&tool=<id>；默认 examples/api 不写进 URL。

export const SECTION_TOOLS = {
  examples: ['api', 'claude', 'codex', 'opencode', 'openclaw'],
  config: ['claude'],
};

export const DEFAULT_SECTION = 'examples';
export const DEFAULT_TOOL = 'api';

/** 解析 URL 选中态；非法 section/tool 回退到该分组的默认项。 */
export function parseSelection(params) {
  const section = params.get('section');
  if (!section || !SECTION_TOOLS[section]) {
    return { section: DEFAULT_SECTION, tool: DEFAULT_TOOL };
  }
  const tools = SECTION_TOOLS[section];
  const tool = params.get('tool');
  return { section, tool: tools.includes(tool) ? tool : tools[0] };
}

/** 把选中态写回 URLSearchParams：保留 group 等其余参数，默认值从 URL 省略。 */
export function selectionSearch(params, selection) {
  const tools = SECTION_TOOLS[selection.section];
  if (!tools) throw new Error(`未知的 section：${selection.section}`);
  const tool = tools.includes(selection.tool) ? selection.tool : tools[0];

  const next = new URLSearchParams(params);
  // 只有「示例的默认工具」才完全省略 section；否则必须保留 section，
  // 避免 ?tool=claude 被解析回 examples/api。
  if (selection.section === DEFAULT_SECTION && tool === tools[0]) next.delete('section');
  else next.set('section', selection.section);

  if (tool === tools[0]) next.delete('tool');
  else next.set('tool', tool);
  return next;
}

/** 规范化当前 URL：非法值修正、默认值省略；与 selectionSearch 组合后幂等。 */
export function normalizeSearch(params) {
  return selectionSearch(params, parseSelection(params));
}

/** 目标地址（HashRouter 用）：/clients?section=...&tool=... */
export function selectionHref(params, selection) {
  const search = selectionSearch(params, selection).toString();
  return search ? `/clients?${search}` : '/clients';
}
