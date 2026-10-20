import { KEY_PLACEHOLDER, MODEL_PLACEHOLDER } from './connectionGuide.js';

// 显式传入的分组不存在时保留空选择，不能把请求悄悄切到其他分组。
export function selectServiceGroup(groups, requested, previous = '') {
  if (requested !== null) return groups.some((g) => g.name === requested) ? requested : '';
  return groups.some((g) => g.name === previous) ? previous : groups[0]?.name || '';
}

const shellQuote = (value) => `'${String(value).replaceAll("'", "'\\''")}'`;

// 仅生成供手动执行的命令，不发起网络请求。
export function buildServiceExamples({ serverUrl, apiKey, model, stream = false }) {
  const base = serverUrl.replace(/\/+$/, '');
  const key = apiKey || KEY_PLACEHOLDER;
  const group = model || MODEL_PLACEHOLDER;
  const definitions = [
    ['chat', 'Chat Completions', '/v1/chat/completions', { model: group, messages: [{ role: 'user', content: '请用一句话介绍自己。' }], max_tokens: 128 }],
    ['responses', 'Responses', '/v1/responses', { model: group, input: '请用一句话介绍自己。', max_output_tokens: 128 }],
    ['messages', 'Messages', '/v1/messages', { model: group, messages: [{ role: 'user', content: '请用一句话介绍自己。' }], max_tokens: 128 }],
  ];
  return definitions.map(([id, label, path, body]) => ({
    id, label, path,
    code: [
      `curl${stream ? ' -N' : ''} ${shellQuote(base + path)} \\`,
      `  -H ${shellQuote(`Authorization: Bearer ${key}`)} \\`,
      '  -H \'Content-Type: application/json\' \\',
      ...(id === 'messages' ? ["  -H 'anthropic-version: 2023-06-01' \\"] : []),
      `  -d ${shellQuote(JSON.stringify({ ...body, stream }, null, 2))}`,
    ].join('\n'),
  }));
}
