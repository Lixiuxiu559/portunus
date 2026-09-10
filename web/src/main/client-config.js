// 客户端配置文件读写模块（Electron 主进程）。
// 以「原始文本」为模型：编辑器直接持有 settings.json / config.toml 的全文，
// 读回原文、整写保存（写前首次备份 .portunus.bak）、并从 .bak 回滚。
//
// - Claude Code CLI → ~/.claude/settings.json（JSON）
// - Codex           → ~/.codex/config.toml（TOML）

const fs = require('fs');
const path = require('path');
const os = require('os');
const { parse: parseToml } = require('smol-toml');

// 目标文件不存在时的默认模板（最简起步内容）。
// Codex 模板四键缺一不可（研究依据 docs/research/cc-switch-codex-config.md §3/§5.1）：
// 无 base_url 会静默回落 api.openai.com，无鉴权键则不发 Authorization 头必 401；
// base_url / experimental_bearer_token 预置空壳是为了让客户端页的正则点改能命中。
const CLAUDE_DEFAULT = '{\n  "env": {}\n}\n';
const CODEX_DEFAULT = [
  'model_provider = "portunus"',
  '# model 必须是 portunus 分组里真实存在的模型名',
  'model = "在此填入 portunus 中的模型名"',
  'model_reasoning_effort = "high"',
  'disable_response_storage = true',
  '',
  '[model_providers.portunus]',
  'name = "Portunus"',
  'base_url = "http://127.0.0.1:3060/v1"',
  'wire_api = "responses"',
  'experimental_bearer_token = ""',
  '',
].join('\n') + '\n';

function claudePath() {
  return path.join(os.homedir(), '.claude', 'settings.json');
}

function codexPath() {
  return path.join(os.homedir(), '.codex', 'config.toml');
}

function codexAuthPath() {
  return path.join(os.homedir(), '.codex', 'auth.json');
}

/**
 * 只读 ~/.codex/auth.json 的 OPENAI_API_KEY（Codex 的另一鉴权来源，优先级低于
 * config.toml 的 experimental_bearer_token）。portunus 永不写此文件，保护官方登录态。
 */
function readCodexAuth() {
  const file = codexAuthPath();
  if (!fs.existsSync(file)) {
    return { exists: false, key: '' };
  }
  try {
    const obj = JSON.parse(fs.readFileSync(file, 'utf-8'));
    return { exists: true, key: typeof obj.OPENAI_API_KEY === 'string' ? obj.OPENAI_API_KEY : '' };
  } catch {
    return { exists: true, key: '', error: 'auth.json 不是合法 JSON' };
  }
}

/** 两个配置文件路径（供 UI 展示）。 */
function getPaths() {
  return { claude: claudePath(), codex: codexPath() };
}

/**
 * 原子写入：写同目录临时文件后 rename 覆盖，避免半写状态损坏用户配置。
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
 * 之后不再覆盖，保证 `.bak` 始终是「用户用 portunus 之前」的原始配置。
 */
function backupIfExists(file) {
  const bak = file + '.portunus.bak';
  if (fs.existsSync(file) && !fs.existsSync(bak)) {
    fs.copyFileSync(file, bak);
  }
}

/** 读取文件原文；不存在则返回默认模板。返回 { text, exists, backupExists }。 */
function readOrDefault(file, def) {
  if (fs.existsSync(file)) {
    const raw = fs.readFileSync(file, 'utf-8');
    return {
      text: raw.trim() ? raw : def,
      exists: true,
      backupExists: fs.existsSync(file + '.portunus.bak'),
    };
  }
  return { text: def, exists: false, backupExists: false };
}

function readClaude() {
  return readOrDefault(claudePath(), CLAUDE_DEFAULT);
}

function readCodex() {
  return readOrDefault(codexPath(), CODEX_DEFAULT);
}

/** 读取 .portunus.bak 备份原文；不存在返回 { exists: false, text: '' }。 */
function readBackup(file) {
  const bak = file + '.portunus.bak';
  if (!fs.existsSync(bak)) {
    return { exists: false, text: '' };
  }
  return { exists: true, text: fs.readFileSync(bak, 'utf-8') };
}

function readBackupClaude() {
  return readBackup(claudePath());
}

function readBackupCodex() {
  return readBackup(codexPath());
}

/** 校验并整写保存（写前首次备份）。返回 { ok, backupExists }。 */
function saveRaw(file, text, validate) {
  if (typeof text !== 'string') {
    throw new Error('配置内容必须是字符串');
  }
  validate(text);
  backupIfExists(file);
  atomicWrite(file, text);
  return { ok: true, backupExists: fs.existsSync(file + '.portunus.bak') };
}

function saveClaude(text) {
  return saveRaw(claudePath(), text, (t) => {
    try {
      JSON.parse(t);
    } catch (e) {
      throw new Error('settings.json 不是合法 JSON：' + e.message);
    }
  });
}

function saveCodex(text) {
  return saveRaw(codexPath(), text, (t) => {
    try {
      parseToml(t);
    } catch (e) {
      throw new Error('config.toml 不是合法 TOML：' + e.message);
    }
  });
}

/**
 * 从 .portunus.bak 回滚：读备份覆盖写回目标文件，并保留 .bak（可反复回滚）。
 * 无备份时抛错。
 */
function rollback(file) {
  const bak = file + '.portunus.bak';
  if (!fs.existsSync(bak)) {
    throw new Error('没有可回滚的备份（' + bak + '）');
  }
  atomicWrite(file, fs.readFileSync(bak, 'utf-8'));
  return { ok: true };
}

function rollbackClaude() {
  return rollback(claudePath());
}

function rollbackCodex() {
  return rollback(codexPath());
}

/**
 * 注册 client-config IPC 通道到主进程（ipcMain 注入，保持本模块 electron-free）。
 * 统一包成 { ok, data | error }，避免主进程异常直接冒泡到渲染层。
 */
function register(ipcMain) {
  const wrap = (fn) => async (_event, ...args) => {
    try {
      return { ok: true, data: await fn(...args) };
    } catch (err) {
      return { ok: false, error: err?.message || String(err) };
    }
  };

  ipcMain.handle('client-config:paths', wrap(() => getPaths()));
  ipcMain.handle('client-config:read-claude', wrap(() => readClaude()));
  ipcMain.handle('client-config:read-codex', wrap(() => readCodex()));
  ipcMain.handle('client-config:read-codex-auth', wrap(() => readCodexAuth()));
  ipcMain.handle('client-config:read-backup-claude', wrap(() => readBackupClaude()));
  ipcMain.handle('client-config:read-backup-codex', wrap(() => readBackupCodex()));
  ipcMain.handle('client-config:save-claude', wrap((t) => saveClaude(t)));
  ipcMain.handle('client-config:save-codex', wrap((t) => saveCodex(t)));
  ipcMain.handle('client-config:rollback-claude', wrap(() => rollbackClaude()));
  ipcMain.handle('client-config:rollback-codex', wrap(() => rollbackCodex()));
}

module.exports = {
  register,
  getPaths,
  readClaude,
  readCodex,
  readCodexAuth,
  readBackupClaude,
  readBackupCodex,
  saveClaude,
  saveCodex,
  rollbackClaude,
  rollbackCodex,
};