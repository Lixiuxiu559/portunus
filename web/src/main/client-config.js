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
const { buildModelCatalog, catalogToRows } = require('./codex-catalog');

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

// Codex 鉴权文件与 config.toml 同一模型：源码编辑、整写保存（JSON 校验 + 首写备份）。
// OPENAI_API_KEY 是 portunus 触碰的唯一键（渲染层点改），tokens 等登录材料随源码保留。
const AUTH_DEFAULT = '{\n  "OPENAI_API_KEY": ""\n}\n';

function codexAuthPath() {
  return path.join(os.homedir(), '.codex', 'auth.json');
}

function readCodexAuth() {
  return readOrDefault(codexAuthPath(), AUTH_DEFAULT);
}

function saveCodexAuth(text) {
  return saveRaw(codexAuthPath(), text, (t) => {
    try {
      JSON.parse(t);
    } catch (e) {
      throw new Error('auth.json 不是合法 JSON：' + e.message);
    }
  });
}

function readBackupCodexAuth() {
  return readBackup(codexAuthPath());
}

function rollbackCodexAuth() {
  return rollback(codexAuthPath());
}

const CODEX_CATALOG_FILENAME = 'portunus-model-catalog.json';

/** 生成并原子写入 ~/.codex/portunus-model-catalog.json（Codex /model 菜单的数据源），
 * 返回绝对路径供 config.toml 的 model_catalog_json 引用。空列表抛错由调用方降级。 */
function writeCodexCatalog(models) {
  const catalog = buildModelCatalog(Array.isArray(models) ? models : []);
  if (!catalog) {
    throw new Error('模型目录为空，未写入');
  }
  const file = path.join(os.homedir(), '.codex', CODEX_CATALOG_FILENAME);
  atomicWrite(file, JSON.stringify(catalog, null, 2) + '\n');
  return { ok: true, path: file };
}

/** 读取 portunus 生成的模型目录并反解析为映射表格行（不存在的文件返回空行集）。 */
function readCodexCatalog() {
  const file = path.join(os.homedir(), '.codex', CODEX_CATALOG_FILENAME);
  if (!fs.existsSync(file)) {
    return { ok: true, rows: [] };
  }
  try {
    return { ok: true, rows: catalogToRows(fs.readFileSync(file, 'utf-8')) };
  } catch {
    return { ok: true, rows: [] };
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
  ipcMain.handle('client-config:save-codex-auth', wrap((t) => saveCodexAuth(t)));
  ipcMain.handle('client-config:read-backup-codex-auth', wrap(() => readBackupCodexAuth()));
  ipcMain.handle('client-config:rollback-codex-auth', wrap(() => rollbackCodexAuth()));
  ipcMain.handle('client-config:write-codex-catalog', wrap((m) => writeCodexCatalog(m)));
  ipcMain.handle('client-config:read-codex-catalog', wrap(() => readCodexCatalog()));
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
  saveCodexAuth,
  readBackupCodexAuth,
  rollbackCodexAuth,
  writeCodexCatalog,
  readCodexCatalog,
  readBackupClaude,
  readBackupCodex,
  saveClaude,
  saveCodex,
  rollbackClaude,
  rollbackCodex,
};