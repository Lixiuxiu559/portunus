// 客户端配置文件通道（Electron 主进程，目前只支持 Claude Code）。
// 以「原始文本」为模型：读回 ~/.claude/settings.json 全文，整写保存
// （写前首次备份 .portunus.bak），并从 .bak 回滚。
//
// 安全边界：渲染层不能传入路径，所有读写都固定在 <homeDir>/.claude/settings.json；
// 写入前校验 JSON 与大小，同目录临时文件 + rename 原子落盘，保留原文件权限。

const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');

// 目标文件不存在时的默认模板（最简起步内容）。
const CLAUDE_DEFAULT = '{\n  "env": {}\n}\n';
const DEFAULT_MAX_BYTES = 512 * 1024;

function createClientConfig({
  homeDir = process.env.PORTUNUS_CLIENT_HOME || os.homedir(),
  maxBytes = DEFAULT_MAX_BYTES,
} = {}) {
  const claudePath = path.join(homeDir, '.claude', 'settings.json');
  const backupPath = claudePath + '.portunus.bak';

  const sizeError = () => new Error(`配置文件超过 ${Math.round(maxBytes / 1024)} KiB，请在外部编辑器处理`);

  // 配置可能是指向 dotfiles 的软链：拿到真实目标，避免 rename 把软链替换成普通文件。
  function realTarget(file) {
    try {
      return fs.realpathSync(file);
    } catch {
      return file;
    }
  }

  function readText(file) {
    if (fs.statSync(file).size > maxBytes) throw sizeError();
    return fs.readFileSync(file, 'utf-8');
  }

  function readClaude() {
    if (!fs.existsSync(claudePath)) {
      return { text: CLAUDE_DEFAULT, exists: false, backupExists: false, path: claudePath };
    }
    const text = readText(claudePath);
    return {
      text: text.trim() ? text : CLAUDE_DEFAULT,
      exists: true,
      backupExists: fs.existsSync(backupPath),
      path: realTarget(claudePath),
    };
  }

  function readBackupClaude() {
    if (!fs.existsSync(backupPath)) return { exists: false, text: '', path: backupPath };
    return { exists: true, text: readText(backupPath), path: backupPath };
  }

  // 校验保存内容：字符串、大小上限、合法 JSON，且顶层必须是对象。
  function validate(text) {
    if (typeof text !== 'string') throw new Error('配置内容必须是字符串');
    if (Buffer.byteLength(text, 'utf-8') > maxBytes) throw sizeError();
    let parsed;
    try {
      parsed = JSON.parse(text);
    } catch (e) {
      // Node 的错误信息可能带整段原文，只保留原因，避免把配置内容（含令牌）回传到渲染层
      const reason = String(e?.message || 'JSON 语法错误').split('"')[0].split(' at ')[0].replace(/[,:\s]+$/, '');
      throw new Error('settings.json 不是合法 JSON：' + (reason || 'JSON 语法错误'));
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error('settings.json 顶层必须是 JSON 对象');
    }
    return parsed;
  }

  // 首次写前备份：.bak 始终保留「Portunus 首次改动前」的原件，之后不再覆盖。
  function backupIfExists(file) {
    if (!fs.existsSync(file) || fs.existsSync(backupPath)) return;
    fs.copyFileSync(file, backupPath);
    try { fs.chmodSync(backupPath, fs.statSync(file).mode & 0o777); } catch { /* 权限复制失败不阻塞保存 */ }
  }

  // 同目录临时文件 + fsync + rename：失败不会留下半写配置。
  function atomicWrite(file, content) {
    const dir = path.dirname(file);
    fs.mkdirSync(dir, { recursive: true });
    const mode = fs.existsSync(file) ? fs.statSync(file).mode & 0o777 : 0o600;
    const tmp = path.join(dir, `.${path.basename(file)}.tmp.${process.pid}.${Date.now()}`);
    try {
      const fd = fs.openSync(tmp, 'w', mode);
      try {
        fs.writeFileSync(fd, content, 'utf-8');
        fs.fsyncSync(fd);
      } finally {
        fs.closeSync(fd);
      }
      fs.chmodSync(tmp, mode);
      fs.renameSync(tmp, file);
    } catch (err) {
      try { fs.unlinkSync(tmp); } catch { /* 临时文件可能未创建 */ }
      throw err;
    }
  }

  function saveClaude(text) {
    validate(text);
    const target = fs.existsSync(claudePath) ? realTarget(claudePath) : claudePath;
    backupIfExists(target);
    atomicWrite(target, text);
    return {
      path: target,
      bytes: Buffer.byteLength(text, 'utf-8'),
      backupExists: fs.existsSync(backupPath),
    };
  }

  function rollbackClaude() {
    if (!fs.existsSync(backupPath)) throw new Error('没有可回滚的备份');
    const text = readText(backupPath);
    validate(text);
    const target = fs.existsSync(claudePath) ? realTarget(claudePath) : claudePath;
    atomicWrite(target, text);
    return { path: target, backupExists: true };
  }

  function getPaths() {
    return { claude: fs.existsSync(claudePath) ? realTarget(claudePath) : claudePath };
  }

  /**
   * 注册 IPC 通道。统一包成 { ok, data | error }，避免主进程异常冒泡到渲染层。
   * 通道固定，参数只有文件内容字符串。
   */
  function register(ipcMain) {
    const wrap = (fn) => async (_event, ...args) => {
      try {
        return { ok: true, data: await fn(...args) };
      } catch (err) {
        return { ok: false, error: err?.message || String(err) };
      }
    };
    ipcMain.handle('client-config:paths', wrap(getPaths));
    ipcMain.handle('client-config:read-claude', wrap(readClaude));
    ipcMain.handle('client-config:read-backup-claude', wrap(readBackupClaude));
    ipcMain.handle('client-config:save-claude', wrap(saveClaude));
    ipcMain.handle('client-config:rollback-claude', wrap(rollbackClaude));
  }

  return { readClaude, readBackupClaude, saveClaude, rollbackClaude, getPaths, register };
}

module.exports = {
  createClientConfig,
  CLAUDE_DEFAULT,
  register: (ipcMain) => createClientConfig().register(ipcMain),
};
