import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readActiveProviderId, readCodexKey, upsertCodexKey } from './codexToml.js';

// Codex config.toml「激活 provider 段」定位与点改的回归测试（node --test，运行：cd web && pnpm test）。
// 语义对齐 Codex CLI 0.145.0：base_url / experimental_bearer_token 只在 model_provider
// 指向的 [model_providers.<id>] 段内生效；研究依据 docs/research/cc-switch-codex-config.md §3。

// portunus 新默认模板形态（client-config.js 的 CODEX_DEFAULT）
const PORTUNUS_TOML = [
  'model_provider = "portunus"',
  'model = "gpt-5.6"',
  '',
  '[model_providers.portunus]',
  'name = "Portunus"',
  'base_url = "http://127.0.0.1:3060/v1"',
  'wire_api = "responses"',
  'experimental_bearer_token = "sk-old"',
  '',
  '[mcp_servers.foo]',
  'command = "uvx"',
].join('\n') + '\n';

// cc-switch 托管形态：多 provider 并存，激活段是第二个
const MULTI_TOML = [
  'model_provider = "custom"',
  '',
  '[model_providers.aaa]',
  'name = "AAA"',
  'base_url = "https://aaa.example/v1"',
  '',
  '[model_providers.custom]',
  'name = "custom"',
  'base_url = "http://127.0.0.1:13060/v1"',
  'requires_openai_auth = true',
].join('\n') + '\n';

test('readActiveProviderId：读顶层 model_provider', () => {
  assert.equal(readActiveProviderId(PORTUNUS_TOML), 'portunus');
  assert.equal(readActiveProviderId(MULTI_TOML), 'custom');
  assert.equal(readActiveProviderId('[model_providers.x]\nbase_url = "x"\n'), null);
});

test('readCodexKey：锚定激活段，不读全文第一个匹配', () => {
  // aaa 段在前，但激活的是 custom —— 必须读到 custom 的
  assert.equal(readCodexKey(MULTI_TOML, 'base_url'), 'http://127.0.0.1:13060/v1');
  assert.equal(readCodexKey(PORTUNUS_TOML, 'base_url'), 'http://127.0.0.1:3060/v1');
});

test('readCodexKey：激活段缺键 / 无段 / 段不存在 → 空串', () => {
  assert.equal(readCodexKey(MULTI_TOML, 'experimental_bearer_token'), '');
  assert.equal(readCodexKey('[model_providers.orphan]\nbase_url = "x"\n', 'base_url'), '');
  assert.equal(readCodexKey('model_provider = "ghost"\n', 'base_url'), '');
});

test('readCodexKey：子表（[p.headers]）里的同名键不干扰', () => {
  const t = [
    'model_provider = "p"',
    '',
    '[model_providers.p]',
    'base_url = "https://direct/v1"',
    '',
    '[model_providers.p.headers]',
    'base_url = "https://decoy/v1"',
  ].join('\n') + '\n';
  assert.equal(readCodexKey(t, 'base_url'), 'https://direct/v1');
});

test('upsertCodexKey：已有键原位替换，其余内容逐字节保留', () => {
  const r = upsertCodexKey(PORTUNUS_TOML, 'base_url', 'https://p.example/v1');
  assert.equal(r.ok, true);
  assert.equal(
    r.text,
    PORTUNUS_TOML.replace('http://127.0.0.1:3060/v1', 'https://p.example/v1'),
  );
});

test('upsertCodexKey：非激活段的同名键不受影响', () => {
  const r = upsertCodexKey(MULTI_TOML, 'base_url', 'https://p/v1');
  assert.equal(r.ok, true);
  assert.match(r.text, /base_url = "https:\/\/aaa\.example\/v1"/);
  assert.match(r.text, /base_url = "https:\/\/p\/v1"/);
});

test('upsertCodexKey：激活段缺键 → 插到段头之后，其余内容保留', () => {
  const r = upsertCodexKey(MULTI_TOML, 'experimental_bearer_token', 'sk-new');
  assert.equal(r.ok, true);
  assert.equal(
    r.text,
    MULTI_TOML.replace(
      '[model_providers.custom]\n',
      '[model_providers.custom]\nexperimental_bearer_token = "sk-new"\n',
    ),
  );
});

test('upsertCodexKey：无激活段（无 model_provider / 段缺失）→ 原样返回 ok=false', () => {
  const orphan = '[model_providers.orphan]\nbase_url = "x"\n';
  assert.deepEqual(upsertCodexKey(orphan, 'base_url', 'y'), { text: orphan, ok: false });
  const ghost = 'model_provider = "ghost"\n[mcp_servers.x]\ncommand = "uvx"\n';
  assert.deepEqual(upsertCodexKey(ghost, 'base_url', 'y'), { text: ghost, ok: false });
});

test('upsertCodexKey：空串显式清空', () => {
  const r = upsertCodexKey(PORTUNUS_TOML, 'experimental_bearer_token', '');
  assert.equal(r.ok, true);
  assert.match(r.text, /^experimental_bearer_token = ""$/m);
});

test('upsertCodexKey：值含引号、反斜杠、$ 不丢不改', () => {
  const r = upsertCodexKey(PORTUNUS_TOML, 'base_url', 'https://x/$2a"b\\c');
  assert.equal(r.ok, true);
  assert.match(r.text, /^base_url = "https:\/\/x\/\$2a\\"b\\\\c"$/m);
  assert.equal(readCodexKey(r.text, 'base_url'), 'https://x/$2a"b\\c');
});

test('upsertCodexKey：幂等 —— 同值重复写入结果不变', () => {
  const once = upsertCodexKey(MULTI_TOML, 'base_url', 'https://p/v1');
  const twice = upsertCodexKey(once.text, 'base_url', 'https://p/v1');
  assert.equal(once.text, twice.text);
});

test('upsertCodexKey：段头是文件最后一行（无尾随换行）也能插入', () => {
  const r = upsertCodexKey('model_provider = "p"\n[model_providers.p]', 'base_url', 'https://x/v1');
  assert.equal(r.ok, true);
  assert.equal(r.text, 'model_provider = "p"\n[model_providers.p]\nbase_url = "https://x/v1"\n');
});
