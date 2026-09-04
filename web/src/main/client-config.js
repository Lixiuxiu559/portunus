// 客户端配置文件读写模块（Electron 主进程）。
// 负责把「portunus 作为上游」的配置写进本机的 Claude Code / Codex 配置文件，
// 并读回现状。参考 cc-switch 的字段约定（docs/research/cc-switch-claude-codex-config.md），
// 但只做单向读写，不做多供应商切换 / 接管 / 自动还原。
//
// - Claude Code CLI → ~/.claude/settings.json（JSON），写 env.ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN
// - Codex           → ~/.codex/config.toml（TOML），写 [model_providers.portunus] 段 + 激活 model_provider

const fs = require('fs');
const path = require('path');
const os = require('os');
const { parse: parseToml, stringify: stringifyToml } = require('smol-toml');

function claudePath() {
  return path.join(os.homedir(), '.claude', 'settings.json');
}

function codexPath() {
  return path.join(os.homedir(), '.codex', 'config.toml');
}

/** 两个配置文件路径（供 UI 展示与空判断）。 */
function getPaths() {
  return { claude: claudePath(), codex: codexPath() };
}

/**
 * 原子写入：写同目录临时文件后 rename 覆盖，避免半写状态损坏用户配置。
 * 对齐 cc-switch 的 atomic_write 思路。
 */
function atomicWrite(file, content) {
  const dir = path.dirname(file);
  fs.mkdirSync(dir, { recursive: true });
  const tmp = path.join(dir, `.${path.basename(file)}.tmp.${process.pid}.${Date.now()}`);
  fs.writeFileSync(tmp, content, 'utf-8');
  fs.renameSync(tmp, file);
}

/**
 * 首次写前备份：目标存在且尚无 `.portunus.bak` 时复制一份（同目录），
 * 之后不再覆盖，保证 `.bak` 始终是「用户用 portunus 之前」的原始配置，
 * 作为单向写入场景下唯一的回滚手段。只备份不自动还原。
 */
function backupIfExists(file) {
  const bak = `${file}.portunus.bak`;
  if (fs.existsSync(file) && !fs.existsSync(bak)) {
    fs.copyFileSync(file, bak);
    return bak;
  }
  return null;
}

// ─── Claude Code CLI ───

/** 读取 ~/.claude/settings.json，不存在或为空返回 {}；JSON 损坏则抛错。 */
function readClaude() {
  const file = claudePath();
  if (!fs.existsSync(file)) return {};
  const raw = fs.readFileSync(file, 'utf-8');
  if (!raw.trim()) return {};
  try {
    return JSON.parse(raw);
  } catch (e) {
    throw new Error(`读取 Claude Code 配置失败（${file}）：${e.message}`);
  }
}

/**
 * 把 baseUrl / apiKey 合并写进 settings.json 的 env，
 * 只覆盖 ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN，其余字段原样保留。
 */
function writeClaude({ baseUrl, apiKey }) {
  const file = claudePath();
  const current = readClaude();
  const root = current && typeof current === 'object' && !Array.isArray(current) ? current : {};

  const env = { ...(root.env || {}) };
  if (baseUrl) env.ANTHROPIC_BASE_URL = baseUrl;
  if (apiKey) env.ANTHROPIC_AUTH_TOKEN = apiKey;

  const next = { ...root, env };
  backupIfExists(file);
  atomicWrite(file, `${JSON.stringify(next, null, 2)}\n`);
  return next;
}

// ─── Codex ───

/** 读取 ~/.codex/config.toml，不存在或为空返回 {}；TOML 损坏则抛错。 */
function readCodex() {
  const file = codexPath();
  if (!fs.existsSync(file)) return {};
  const raw = fs.readFileSync(file, 'utf-8');
  if (!raw.trim()) return {};
  try {
    return parseToml(raw);
  } catch (e) {
    throw new Error(`读取 Codex 配置失败（${file}）：${e.message}`);
  }
}

/**
 * 把 baseUrl / apiKey 合并写进 config.toml 的 [model_providers.portunus] 段并激活，
 * 其余 provider 段与顶层字段原样保留；凭据走 experimental_bearer_token，不碰 auth.json。
 */
function writeCodex({ baseUrl, apiKey }) {
  const file = codexPath();
  const current = readCodex();
  const root = current && typeof current === 'object' && !Array.isArray(current) ? current : {};

  const providers = { ...(root.model_providers || {}) };
  const prev = providers.portunus && typeof providers.portunus === 'object' ? providers.portunus : {};
  const portunus = { ...prev };
  portunus.name = portunus.name || 'Portunus';
  portunus.wire_api = portunus.wire_api || 'responses';
  if (baseUrl) portunus.base_url = baseUrl;
  if (apiKey) portunus.experimental_bearer_token = apiKey;
  providers.portunus = portunus;

  const next = { ...root, model_provider: 'portunus', model_providers: providers };
  backupIfExists(file);
  atomicWrite(file, stringifyToml(next));
  return next;
}

module.exports = {
  getPaths,
  readClaude,
  writeClaude,
  readCodex,
  writeCodex,
};