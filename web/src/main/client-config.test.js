const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createClientConfig, CLAUDE_DEFAULT } = require('./client-config');

function tempHome() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'portunus-client-config-'));
}

function writeSettings(home, text) {
  const dir = path.join(home, '.claude');
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, 'settings.json');
  fs.writeFileSync(file, text, 'utf-8');
  return file;
}

test('文件不存在时返回默认模板', () => {
  const home = tempHome();
  const config = createClientConfig({ homeDir: home });
  const r = config.readClaude();
  assert.equal(r.exists, false);
  assert.equal(r.backupExists, false);
  assert.equal(r.text, CLAUDE_DEFAULT);
  assert.equal(r.path, path.join(home, '.claude', 'settings.json'));
});

test('空文件读取回退为默认模板', () => {
  const home = tempHome();
  writeSettings(home, '   \n');
  const r = createClientConfig({ homeDir: home }).readClaude();
  assert.equal(r.exists, true);
  assert.equal(r.text, CLAUDE_DEFAULT);
});

test('保存写入文件并在首次写入前生成 .portunus.bak', () => {
  const home = tempHome();
  const file = writeSettings(home, '{"env":{"OLD":"1"}}\n');
  const config = createClientConfig({ homeDir: home });
  const text = '{\n  "env": {\n    "ANTHROPIC_BASE_URL": "http://127.0.0.1:13061"\n  }\n}\n';
  const saved = config.saveClaude(text);
  assert.equal(saved.backupExists, true);
  assert.equal(fs.readFileSync(file, 'utf-8'), text);
  assert.equal(fs.readFileSync(file + '.portunus.bak', 'utf-8'), '{"env":{"OLD":"1"}}\n');
  assert.deepEqual(config.readClaude(), {
    text,
    exists: true,
    backupExists: true,
    path: fs.realpathSync(file),
  });
});

test('二次保存不覆盖已有备份', () => {
  const home = tempHome();
  const file = writeSettings(home, '{"env":{}}\n');
  const config = createClientConfig({ homeDir: home });
  config.saveClaude('{"env":{"A":"1"}}\n');
  config.saveClaude('{"env":{"A":"2"}}\n');
  assert.equal(fs.readFileSync(file + '.portunus.bak', 'utf-8'), '{"env":{}}\n');
  assert.match(fs.readFileSync(file, 'utf-8'), /"A": "2"|"A":"2"/);
});

test('非法 JSON 与非对象顶层被拒绝且磁盘不变', () => {
  const home = tempHome();
  const file = writeSettings(home, '{"env":{}}\n');
  const config = createClientConfig({ homeDir: home });
  assert.throws(() => config.saveClaude('{"env":'), /不是合法 JSON/);
  // 错误信息不能回显配置内容（可能含令牌）
  assert.throws(
    () => config.saveClaude('{"ANTHROPIC_AUTH_TOKEN":"sk-secret" oops}'),
    (err) => !err.message.includes('sk-secret'),
  );
  assert.throws(() => config.saveClaude('[1,2]'), /顶层必须是 JSON 对象/);
  assert.throws(() => config.saveClaude('null'), /顶层必须是 JSON 对象/);
  assert.throws(() => config.saveClaude(123), /必须是字符串/);
  assert.equal(fs.readFileSync(file, 'utf-8'), '{"env":{}}\n');
  assert.equal(fs.existsSync(file + '.portunus.bak'), false);
});

test('超过大小上限时拒绝保存', () => {
  const home = tempHome();
  writeSettings(home, '{"env":{}}\n');
  const config = createClientConfig({ homeDir: home, maxBytes: 32 });
  assert.throws(() => config.saveClaude(JSON.stringify({ env: { big: 'x'.repeat(64) } })), /超过/);
});

test('从备份回滚并保留 .bak 以支持重复回滚', () => {
  const home = tempHome();
  const file = writeSettings(home, '{"env":{"ORIGINAL":"1"}}\n');
  const config = createClientConfig({ homeDir: home });
  config.saveClaude('{"env":{"CHANGED":"1"}}\n');
  const r = config.rollbackClaude();
  assert.equal(r.backupExists, true);
  assert.equal(fs.readFileSync(file, 'utf-8'), '{"env":{"ORIGINAL":"1"}}\n');
  assert.equal(fs.existsSync(file + '.portunus.bak'), true);
  // 再次回滚仍然可用
  config.rollbackClaude();
});

test('没有备份时回滚报错', () => {
  const home = tempHome();
  writeSettings(home, '{"env":{}}\n');
  assert.throws(() => createClientConfig({ homeDir: home }).rollbackClaude(), /没有可回滚的备份/);
});

test('IPC handlers 走通读取、保存、备份与错误信封', async () => {
  const home = tempHome();
  const handlers = {};
  createClientConfig({ homeDir: home }).register({ handle: (channel, fn) => { handlers[channel] = fn; } });

  const read = await handlers['client-config:read-claude']({});
  assert.equal(read.ok, true);
  assert.equal(read.data.exists, false);

  const first = await handlers['client-config:save-claude']({}, '{"env":{"A":"1"}}');
  assert.equal(first.ok, true);
  assert.equal(first.data.backupExists, false);

  const second = await handlers['client-config:save-claude']({}, '{"env":{"A":"2"}}');
  assert.equal(second.data.backupExists, true);

  const bad = await handlers['client-config:save-claude']({}, '{');
  assert.equal(bad.ok, false);
  assert.match(bad.error, /不是合法 JSON/);

  const backup = await handlers['client-config:read-backup-claude']({});
  assert.equal(backup.ok, true);
  assert.equal(backup.data.exists, true);

  const paths = await handlers['client-config:paths']({});
  assert.equal(paths.ok, true);
  assert.ok(paths.data.claude.endsWith('settings.json'));
});

test('register 只注册固定的 5 个通道', () => {
  const channels = [];
  createClientConfig({ homeDir: tempHome() }).register({ handle: (channel) => channels.push(channel) });
  assert.deepEqual(channels.sort(), [
    'client-config:paths',
    'client-config:read-backup-claude',
    'client-config:read-claude',
    'client-config:rollback-claude',
    'client-config:save-claude',
  ]);
});

test('软链接配置写入真实目标而不替换链接', { skip: process.platform === 'win32' }, () => {
  const home = tempHome();
  const dotfiles = path.join(home, 'dotfiles');
  fs.mkdirSync(dotfiles, { recursive: true });
  const real = path.join(dotfiles, 'claude-settings.json');
  fs.writeFileSync(real, '{"env":{}}\n', 'utf-8');
  const link = path.join(home, '.claude', 'settings.json');
  fs.mkdirSync(path.dirname(link), { recursive: true });
  fs.symlinkSync(real, link);

  const config = createClientConfig({ homeDir: home });
  const text = '{\n  "env": {\n    "A": "1"\n  }\n}\n';
  const saved = config.saveClaude(text);
  assert.equal(fs.lstatSync(link).isSymbolicLink(), true);
  assert.equal(fs.readFileSync(real, 'utf-8'), text);
  assert.equal(saved.path, fs.realpathSync(real));
  assert.equal(fs.existsSync(link + '.portunus.bak'), true);
});
