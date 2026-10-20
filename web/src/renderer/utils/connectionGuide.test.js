import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parse } from 'smol-toml';
import { buildConnectionGuide, KEY_PLACEHOLDER, MODEL_PLACEHOLDER } from './connectionGuide.js';

for (const port of [13060, 3060]) {
  test(`接入示例使用实际 ${port} 网关和对外分组名，JSON/TOML 均可解析`, () => {
    const guide = buildConnectionGuide({ serverUrl: `http://127.0.0.1:${port}///`, apiKey: 'pk-test', model: 'coding' });
    const claude = JSON.parse(guide.claude).env;
    assert.equal(claude.ANTHROPIC_BASE_URL, `http://127.0.0.1:${port}`);
    assert.equal(claude.ANTHROPIC_AUTH_TOKEN, 'pk-test');
    for (const name of ['ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL']) assert.equal(claude[name], 'coding');
    const codex = parse(guide.codex);
    assert.equal(codex.model_provider, 'portunus');
    assert.equal(codex.model, 'coding');
    assert.equal(codex.model_providers.portunus.base_url, `http://127.0.0.1:${port}/v1`);
    assert.equal(codex.model_providers.portunus.experimental_bearer_token, 'pk-test');
    assert.equal(codex.model_providers.portunus.wire_api, 'responses');
  });
}

test('分组名和令牌含引号、反斜杠、换行及中文时，生成内容不会破坏语法或注入额外字段', () => {
  const apiKey = 'pk-"\\\nkey';
  const model = '研发"\\\nmodel_provider = "other"';
  const guide = buildConnectionGuide({ serverUrl: 'http://127.0.0.1:13060', apiKey, model });
  assert.equal(JSON.parse(guide.claude).env.ANTHROPIC_MODEL, model);
  assert.equal(JSON.parse(guide.claude).env.ANTHROPIC_AUTH_TOKEN, apiKey);
  const codex = parse(guide.codex);
  assert.equal(codex.model, model);
  assert.equal(codex.model_provider, 'portunus');
  assert.equal(codex.model_providers.portunus.experimental_bearer_token, apiKey);
});

test('未生成令牌或未选择分组时使用明确占位符，不包含 undefined 或真实密钥', () => {
  const guide = buildConnectionGuide({ serverUrl: 'http://127.0.0.1:13060' });
  assert.equal(JSON.parse(guide.claude).env.ANTHROPIC_AUTH_TOKEN, KEY_PLACEHOLDER);
  assert.equal(parse(guide.codex).model, MODEL_PLACEHOLDER);
});

for (const model of ['coding', '研发"\\\n分组', '__proto__']) {
  test(`新增客户端示例使用分组与真实令牌，安全处理模型名 ${JSON.stringify(model)}`, () => {
    const apiKey = 'pk-"\\test';
    const guide = buildConnectionGuide({ serverUrl: 'http://127.0.0.1:13060/', apiKey, model });
    const v1 = JSON.parse(guide.opencode);
    const v2 = JSON.parse(guide.opencodeV2);
    assert.equal(v1.model, `portunus/${model}`);
    assert.equal(v1.provider.portunus.options.apiKey, apiKey);
    assert.equal(v1.provider.portunus.options.baseURL, 'http://127.0.0.1:13060/v1');
    assert.equal(v1.provider.portunus.npm, '@ai-sdk/openai-compatible');
    assert.equal(v1.provider.portunus.models[model].name, model);
    assert.equal(v2.model, `portunus/${model}`);
    assert.equal(v2.providers.portunus.settings.apiKey, apiKey);
    assert.equal(v2.providers.portunus.models[model].name, model);
    const claw = JSON.parse(guide.openclaw);
    assert.equal(claw.agents.defaults.model.primary, `portunus/${model}`);
    assert.equal(claw.models.providers.portunus.api, 'openai-completions');
    assert.equal(claw.models.providers.portunus.models[0].id, model);
    assert.equal(claw.models.providers.portunus.apiKey, apiKey);

  });
}
