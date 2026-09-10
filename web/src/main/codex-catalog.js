// Codex 外部模型目录（config.toml 顶层 model_catalog_json 指向的 models.json）生成。
// 机制对齐 cc-switch（codex_config.rs codex_catalog_model_entry）：克隆官方 ModelInfo
// 模板再覆写——Codex 的 catalog 解析是严格 schema，缺 base_instructions /
// supports_reasoning_summaries 会整个文件拒载；自由格式 apply_patch 相关键必须剥除
// （网关不支持 freeform 工具，靠 shell_type="shell_command" 完成编辑）。
// 模板来源：cc-switch 仓库 resources/gpt5_5_template.json（Codex 官方 ModelInfo 形状快照，
// 含 GPT-5.5 的 base_instructions 全文——它是 parser 必需字段，不是 portunus 生成的提示词）。

const TEMPLATE = require('./resources/gpt5_5_template.json');

// 第三方网关的保守 reasoning 档：声明太多档位，上游不支持时 Codex 发过去就报错
const CONSERVATIVE_REASONING_LEVELS = [
  { effort: 'high', description: 'High reasoning effort' },
  { effort: 'none', description: 'Disable reasoning' },
];

/** 模板克隆 + 覆写出一个目录条目（纯函数）。models 为空返回 null，调用方不写文件。
 * 条目可以是模型名字符串，或 cc-switch 表格同形的行对象 {model, displayName?, contextWindow?}。 */
function buildModelCatalog(models, template = TEMPLATE) {
  const seen = new Set();
  const entries = [];
  for (const raw of models) {
    const model = typeof raw === 'string' ? raw.trim() : typeof raw?.model === 'string' ? raw.model.trim() : '';
    if (!model || seen.has(model)) continue;
    seen.add(model);
    const displayName = typeof raw?.displayName === 'string' ? raw.displayName.trim() : '';
    const entry = {
      ...template,
      ...overrides(model, displayName, parsePositiveInt(raw?.contextWindow) ?? DEFAULT_CONTEXT_WINDOW, entries.length),
    };
    for (const key of STRIP_KEYS) delete entry[key];
    entries.push(entry);
  }
  return entries.length ? { models: entries } : null;
}

const DEFAULT_CONTEXT_WINDOW = 128000;

function parsePositiveInt(v) {
  const n = Number(v);
  return Number.isInteger(n) && n > 0 ? n : null;
}

// 自由格式 apply_patch 相关键：原生网关不支持，剥掉后靠 shell_command 完成编辑
const STRIP_KEYS = ['apply_patch_tool_type', 'web_search_tool_type', 'tools', 'model_messages'];

function overrides(model, displayName, contextWindow, index) {
  return {
    slug: model,
    display_name: displayName || model,
    description: displayName || model,
    context_window: contextWindow,
    max_context_window: contextWindow,
    priority: 1000 + index,
    additional_speed_tiers: [],
    service_tiers: [],
    availability_nux: null,
    upgrade: null,
    input_modalities: ['text', 'image'],
    supported_reasoning_levels: CONSERVATIVE_REASONING_LEVELS,
    default_reasoning_level: 'high',
    shell_type: 'shell_command',
  };
}

/**
 * catalog 文件 → 表格行（cc-switch read_codex_model_catalog_simplified 同语义）：
 * display_name 等于 slug、context_window 等于默认值 128k 时折叠为空，保留「用户留空」意图。
 * 无 slug 的条目剔除；非法输入返回空数组。
 */
function catalogToRows(catalog) {
  let models = catalog;
  if (typeof catalog === 'string') {
    try {
      models = JSON.parse(catalog);
    } catch {
      return [];
    }
  }
  if (!models || typeof models !== 'object' || !Array.isArray(models.models)) return [];
  const rows = [];
  for (const m of models.models) {
    const model = typeof m?.slug === 'string' ? m.slug.trim() : '';
    if (!model) continue;
    const displayName = typeof m?.display_name === 'string' && m.display_name !== model ? m.display_name : '';
    const contextWindow = parsePositiveInt(m?.context_window);
    rows.push({
      model,
      displayName,
      contextWindow: contextWindow && contextWindow !== DEFAULT_CONTEXT_WINDOW ? contextWindow : null,
    });
  }
  return rows;
}

module.exports = { buildModelCatalog, catalogToRows };
