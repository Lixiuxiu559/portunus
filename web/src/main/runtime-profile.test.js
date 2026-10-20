const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { profileFor, initializeProfile, preloadProfile } = require('./runtime-profile');
const { addressOf } = require('./server-address');

test('热开发、直接运行与打包 Dev 使用同一身份，未标记的发布包仍是正式版', () => {
  for (const isPackaged of [false, true]) {
    const profile = profileFor({ isPackaged, metadata: { portunusProfile: 'development' } });
    assert.equal(profile, 'development');
    assert.equal(addressOf({ profile }).port, 13061);
  }
  assert.equal(profileFor({ isPackaged: false }), 'development');
  assert.equal(profileFor({ isPackaged: true }), 'production');
  assert.equal(preloadProfile(['--portunus-profile=development']).updatesEnabled, false);
  assert.equal(preloadProfile([]).profile, 'development');
});

test('Dev 显式隔离名称、userData、session、日志；正式路径和名称不改写', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'portunus-profile-'));
  const writes = [];
  const app = {
    isPackaged: true,
    getPath: () => root,
    setName: (name) => writes.push(['name', name]),
    setPath: (...args) => writes.push(args),
    setAppLogsPath: (dir) => writes.push(['logs', dir]),
  };
  try {
    initializeProfile(app, {});
    assert.deepEqual(writes, []);
    initializeProfile(app, { portunusProfile: 'development' });
    assert.deepEqual(writes, [
      ['name', 'Portunus Dev'], ['userData', path.join(root, 'Portunus Dev')],
      ['sessionData', path.join(root, 'Portunus Dev/session')], ['logs', path.join(root, 'Portunus Dev/logs')],
    ]);
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test('Dev 更新服务不加载安装器，所有更新 IPC 都拒绝执行', async () => {
  let service;
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, 'updater.js'), 'utf8'), {
    module: { set exports(value) { service = value; } },
    require(id) {
      if (id === 'electron') return { app: { isPackaged: true } };
      if (id === '../../package.json') return { portunusProfile: 'development' };
      assert.notEqual(id, 'electron-updater');
      return require(id);
    },
  });
  const handlers = {};
  service.register({ handle: (name, fn) => { handlers[name] = fn; } });
  assert.equal(handlers['update:get-state']().downloaded, false);
  for (const action of ['check', 'download', 'install']) await assert.rejects(handlers[`update:${action}`](), /Dev 禁用正式更新/);
});

test('预览构建配置拥有独立应用 ID、名称和元数据，正式配置保持原值', () => {
  const production = require('../../package.json').build;
  const dev = require('../../electron-builder.dev.cjs');
  assert.equal(production.appId, 'com.portunus.app');
  assert.equal(production.productName, 'Portunus');
  assert.equal(dev.appId, 'com.portunus.app.dev');
  assert.equal(dev.productName, 'Portunus Dev');
  assert.equal(dev.extraMetadata.portunusProfile, 'development');
  assert.equal(dev.directories.output, 'dist-dev');
  assert.equal(dev.publish, null);
});

test('主进程先确定身份再获取锁；锁失败不创建后端、不注册启动回调', () => {
  for (const locked of [false, true]) {
    const calls = [];
    let backendOptions;
    const app = {
      isPackaged: true,
      getPath: () => '/isolated/Portunus Dev',
      requestSingleInstanceLock() { calls.push('lock'); return locked; },
      quit() { calls.push('quit'); },
      whenReady() { calls.push('whenReady'); return { then() {} }; },
      on() {},
    };
    const source = fs.readFileSync(path.join(__dirname, 'main.js'), 'utf8');
    vm.runInNewContext(`(function(){${source}\n})()`, {
      __dirname, console,
      process: { platform: 'darwin', resourcesPath: '/dev-resources', env: { NODE_ENV: 'development' }, on() {} },
      require(id) {
        if (id === 'electron') return { app, ipcMain: { handle() {} } };
        if (id === './runtime-profile') return { initializeProfile() { calls.push('identity'); return { profile: 'development', isDevelopment: true }; } };
        if (id === './updater') return { register() {} };
        if (id === './sidecar') return { createBackend(options) { backendOptions = options; calls.push('backend'); return {}; } };
        if (id === './app-config') return { register() {}, getNetworkMode: () => 'local' };
        if (id === './tray') return {};
        if (id === 'fs') return { mkdirSync() {} };
        return require(id);
      },
    });
    assert.deepEqual(calls.slice(0, 2), ['identity', 'lock']);
    if (!locked) {
      assert.deepEqual(calls, ['identity', 'lock', 'quit']);
      assert.equal(backendOptions, undefined);
    } else {
      assert.equal(backendOptions.port, 13061);
      assert.equal(backendOptions.cwd, '/isolated/Portunus Dev/backend');
      assert.equal(backendOptions.dataDir, '/isolated/Portunus Dev/data');
      assert.equal(backendOptions.databaseType, 'sqlite');
      assert.ok(backendOptions.binPath.startsWith('/dev-resources/'));
    }
  }
});
