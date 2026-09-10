import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildModelCatalog, catalogToRows } from './codex-catalog.js';

// buildModelCatalog：Codex 外部模型目录生成回归测试（node --test，运行：cd web && pnpm test）。
// 机制对齐 cc-switch（codex_config.rs codex_catalog_model_entry）：克隆官方 ModelInfo
// 模板再覆写——catalog 解析是严格 schema，base_instructions 等必需字段必须保留，
// 缺了整个文件被 Codex 拒载；自由格式 apply_patch 相关键必须剥掉（网关不支持）。

const template = {
  slug: 'gpt-5.5',
  display_name: 'GPT-5.5',
  description: 'GPT-5.5',
  context_window: 272000,
  max_context_window: 272000,
  input_modalities: ['text', 'image'],
  supported_reasoning_levels: [{ effort: 'low', description: 'x' }, { effort: 'medium', description: 'y' }],
  default_reasoning_level: 'medium',
  base_instructions: 'You are Codex…',
  shell_type: 'shell_command',
  apply_patch_tool_type: 'freeform',
  web_search_tool_type: 'web_search',
  tools: [{ name: 'apply_patch' }],
  model_messages: [],
  supports_reasoning_summaries: true,
};

test('buildModelCatalog：每个模型一个条目，slug/展示名/描述取模型名', () => {
  const cat = buildModelCatalog(['g1', 'g2'], template);
  assert.equal(cat.models.length, 2);
  assert.deepEqual(
    cat.models.map((m) => m.slug),
    ['g1', 'g2'],
  );
  assert.equal(cat.models[0].display_name, 'g1');
  assert.equal(cat.models[0].description, 'g1');
});

test('buildModelCatalog：必需字段保留，自由格式 apply_patch 相关键剥除', () => {
  const e = buildModelCatalog(['g1'], template).models[0];
  assert.equal(e.base_instructions, 'You are Codex…');
  assert.equal(e.supports_reasoning_summaries, true);
  assert.equal(e.shell_type, 'shell_command');
  for (const key of ['apply_patch_tool_type', 'web_search_tool_type', 'tools', 'model_messages']) {
    assert.equal(key in e, false, `${key} 应被剥除`);
  }
});

test('buildModelCatalog：上下文窗口归一 128k，priority 递增，空档位与空 nux', () => {
  const [a, b] = buildModelCatalog(['g1', 'g2'], template).models;
  for (const e of [a, b]) {
    assert.equal(e.context_window, 128000);
    assert.equal(e.max_context_window, 128000);
    assert.deepEqual(e.additional_speed_tiers, []);
    assert.deepEqual(e.service_tiers, []);
    assert.equal(e.availability_nux, null);
    assert.equal(e.upgrade, null);
    assert.deepEqual(e.input_modalities, ['text', 'image']);
  }
  assert.equal(a.priority, 1000);
  assert.equal(b.priority, 1001);
});

test('buildModelCatalog：reasoning 保守档 none/high，默认 high', () => {
  const e = buildModelCatalog(['g1'], template).models[0];
  assert.deepEqual(
    e.supported_reasoning_levels.map((l) => l.effort),
    ['high', 'none'],
  );
  assert.equal(e.default_reasoning_level, 'high');
});

test('buildModelCatalog：空模型列表 → null（调用方不写目录文件）', () => {
  assert.equal(buildModelCatalog([], template), null);
  assert.equal(buildModelCatalog(['', '  '], template), null);
});

test('buildModelCatalog：重名模型去重，不改入参模板', () => {
  const cat = buildModelCatalog(['g1', 'g1', 'g2'], template);
  assert.deepEqual(
    cat.models.map((m) => m.slug),
    ['g1', 'g2'],
  );
  assert.equal(template.slug, 'gpt-5.5');
  assert.deepEqual(template.tools, [{ name: 'apply_patch' }]);
});

// ─── 行对象形态（cc-switch 模型映射表格：model + displayName? + contextWindow?）───

test('buildModelCatalog：行对象的 displayName / contextWindow 生效，空值回落默认', () => {
  const cat = buildModelCatalog(
    [
      { model: 'g1', displayName: 'G1 快速档', contextWindow: 400000 },
      { model: 'g2', displayName: '  ', contextWindow: 0 },
      'g3',
    ],
    template,
  );
  assert.deepEqual(
    cat.models.map((m) => [m.slug, m.display_name, m.description, m.context_window, m.max_context_window]),
    [
      ['g1', 'G1 快速档', 'G1 快速档', 400000, 400000],
      ['g2', 'g2', 'g2', 128000, 128000],
      ['g3', 'g3', 'g3', 128000, 128000],
    ],
  );
});

test('buildModelCatalog：行对象非法值（负数/NaN/非字符串 model）剔除或回落', () => {
  const cat = buildModelCatalog(
    [{ model: 'g1', contextWindow: -5 }, { model: 42 }, { model: 'g2', contextWindow: Number.NaN }],
    template,
  );
  assert.deepEqual(
    cat.models.map((m) => m.slug),
    ['g1', 'g2'],
  );
  assert.equal(cat.models[0].context_window, 128000);
});

// ─── 反解析（catalog 文件 → 表格行，回退值折叠为空）───

test('catalogToRows：displayName=slug 与 128k 折叠为空，其余原样', () => {
  const rows = catalogToRows({
    models: [
      { slug: 'g1', display_name: 'G1 快速档', context_window: 400000 },
      { slug: 'g2', display_name: 'g2', context_window: 128000 },
      { slug: 'g3', display_name: 'G3' },
    ],
  });
  assert.deepEqual(rows, [
    { model: 'g1', displayName: 'G1 快速档', contextWindow: 400000 },
    { model: 'g2', displayName: '', contextWindow: null },
    { model: 'g3', displayName: 'G3', contextWindow: null },
  ]);
});

test('catalogToRows：无 slug 条目剔除，非法输入返回空数组', () => {
  assert.deepEqual(catalogToRows({ models: [{ display_name: 'x' }, { slug: 'g1' }] }), [
    { model: 'g1', displayName: '', contextWindow: null },
  ]);
  assert.deepEqual(catalogToRows(null), []);
  assert.deepEqual(catalogToRows('not json'), []);
  assert.deepEqual(catalogToRows({ models: 'oops' }), []);
});
