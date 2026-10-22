// 只生成可复制的接入示例；客户端文件读写由 main/client-config 与「客户端配置」面板单独负责。
export const KEY_PLACEHOLDER = '<PORTUNUS_API_KEY>';
export const MODEL_PLACEHOLDER = '<分组名称>';

export function buildConnectionGuide({ serverUrl, apiKey, model }) {
  const baseUrl = serverUrl.replace(/\/+$/, '');
  const key = apiKey || KEY_PLACEHOLDER;
  const group = model || MODEL_PLACEHOLDER;
  // JSON 双引号字符串也是合法 TOML 基本字符串；同时转义引号、反斜杠和换行。
  const quote = JSON.stringify;
  return {
    baseUrl,
    openAIBaseUrl: `${baseUrl}/v1`,
    claude: JSON.stringify({
      env: {
        ANTHROPIC_BASE_URL: baseUrl,
        ANTHROPIC_AUTH_TOKEN: key,
        ANTHROPIC_MODEL: group,
        ANTHROPIC_DEFAULT_SONNET_MODEL: group,
        ANTHROPIC_DEFAULT_OPUS_MODEL: group,
        ANTHROPIC_DEFAULT_HAIKU_MODEL: group,
      },
    }, null, 2),
    codex: [
      'model_provider = "portunus"',
      `model = ${quote(group)}`,
      '',
      '[model_providers.portunus]',
      'name = "Portunus"',
      `base_url = ${quote(`${baseUrl}/v1`)}`,
      'wire_api = "responses"',
      `experimental_bearer_token = ${quote(key)}`,
    ].join('\n'),
    opencode: JSON.stringify({
      $schema: 'https://opencode.ai/config.json',
      model: `portunus/${group}`,
      provider: { portunus: {
        npm: '@ai-sdk/openai-compatible', name: 'Portunus',
        options: { baseURL: `${baseUrl}/v1`, apiKey: key },
        models: { [group]: { name: group } },
      } },
    }, null, 2),
    opencodeV2: JSON.stringify({
      $schema: 'https://opencode.ai/config.json',
      model: `portunus/${group}`,
      providers: { portunus: {
        name: 'Portunus', package: '@opencode/ai/providers/openai-compatible',
        settings: { baseURL: `${baseUrl}/v1`, apiKey: key },
        models: { [group]: { name: group } },
      } },
    }, null, 2),
    openclaw: JSON.stringify({
      agents: { defaults: { model: { primary: `portunus/${group}` } } },
      models: { providers: { portunus: {
        baseUrl: `${baseUrl}/v1`, apiKey: key, api: 'openai-completions',
        models: [{ id: group, name: group }],
      } } },
    }, null, 2),
  };
}
