const { test } = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { createUpdateService, COS_SOURCE } = require('./update-service');

const available = (version = '0.5.0') => ({ isUpdateAvailable: true, updateInfo: { version } });
const unavailable = { isUpdateAvailable: false, updateInfo: { version: '0.4.0' } };

function setup({ checks = [available()], downloads = [['installer.zip']] } = {}) {
  const updater = new EventEmitter();
  const calls = [];
  const events = [];
  let feed;
  updater.setFeedURL = (value) => { feed = value.provider; calls.push(`source:${feed}`); };
  updater.checkForUpdates = async () => {
    calls.push(`check:${feed}`);
    updater.emit('checking-for-update');
    const result = checks.shift();
    if (result instanceof Error) {
      updater.emit('error', result);
      throw result;
    }
    const value = await result;
    if (value) updater.emit(value.isUpdateAvailable ? 'update-available' : 'update-not-available', value.updateInfo);
    return value;
  };
  updater.downloadUpdate = async () => {
    calls.push(`download:${feed}`);
    const result = downloads.shift();
    if (result instanceof Error) {
      updater.emit('error', result);
      throw result;
    }
    updater.emit('update-downloaded', { version: '0.5.0' });
    return result;
  };
  updater.quitAndInstall = () => calls.push('install');
  const service = createUpdateService(updater, { warn() {} });
  const win = { isDestroyed: () => false, webContents: { send: (...args) => events.push(args) } };
  service.forwardEvents(win);
  return { service, updater, calls, events, win };
}

const errors = (events) => events.filter(([channel]) => channel === 'update:error');

test('默认 COS 地址不带签名，检查和下载均使用 COS', async () => {
  const { service, updater, calls, events } = setup();
  assert.equal(COS_SOURCE.url, 'https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/');
  assert.equal(updater.autoDownload, false);
  assert.equal(updater.autoInstallOnAppQuit, true);
  assert.equal((await service.checkForUpdates()).updateInfo.version, '0.5.0');
  assert.deepEqual(await service.downloadUpdate(), ['installer.zip']);
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'download:generic']);
  assert.equal(errors(events).length, 0);
});

test('COS 检查失败后使用 GitHub，成功时不向界面报告中间错误', async () => {
  const { service, calls, events } = setup({ checks: [new Error('COS 404'), available()] });
  await service.checkForUpdates();
  await service.downloadUpdate();
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'source:github', 'check:github', 'download:github']);
  assert.equal(errors(events).length, 0);
});

test('COS 已是最新或开发环境禁用更新时，不切换 GitHub', async () => {
  for (const result of [unavailable, null]) {
    const { service, calls } = setup({ checks: [result] });
    assert.equal(await service.checkForUpdates(), result);
    assert.deepEqual(calls, ['source:generic', 'check:generic']);
    await assert.rejects(service.downloadUpdate(), /请先检查更新/);
  }
});

test('两个源检查都失败：只向界面报告最终错误，并允许重新从 COS 检查', async () => {
  const { service, calls, events } = setup({ checks: [new Error('COS 404'), new Error('GitHub offline'), available()] });
  await assert.rejects(service.checkForUpdates(), /GitHub offline/);
  assert.deepEqual(errors(events), [['update:error', 'GitHub offline']]);
  await service.checkForUpdates();
  assert.deepEqual(calls.slice(-2), ['source:generic', 'check:generic']);
});

test('COS 下载失败：重新获取 GitHub 同版本信息后下载，且不重复发送版本事件', async () => {
  const { service, calls, events } = setup({
    checks: [available(), available()],
    downloads: [new Error('COS download failed'), ['github-installer.zip']],
  });
  await service.checkForUpdates();
  assert.deepEqual(await service.downloadUpdate(), ['github-installer.zip']);
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'download:generic', 'source:github', 'check:github', 'download:github']);
  assert.equal(events.filter(([channel]) => channel === 'update:available').length, 1);
  assert.equal(errors(events).length, 0);
  assert.ok(events.some(([channel, info]) => channel === 'update:download-progress' && info.percent === 0));
});

test('GitHub 下载也失败时只报告最终错误，不循环切源', async () => {
  const { service, calls, events } = setup({
    checks: [available(), available()],
    downloads: [new Error('COS failed'), new Error('GitHub failed')],
  });
  await service.checkForUpdates();
  await assert.rejects(service.downloadUpdate(), /GitHub failed/);
  assert.deepEqual(errors(events), [['update:error', 'GitHub failed']]);
  assert.equal(calls.filter((call) => call.startsWith('download:')).length, 2);
});

test('备用源没有相同版本时不下载安装包，也不沿用过期下载信息', async () => {
  for (const fallback of [available('0.4.1'), available('0.6.0'), unavailable, null, new Error('GitHub offline')]) {
    const { service, calls } = setup({ checks: [available(), fallback], downloads: [new Error('COS failed')] });
    await service.checkForUpdates();
    await assert.rejects(service.downloadUpdate(), /尚未提供版本 0.5.0|GitHub offline/);
    await assert.rejects(service.downloadUpdate(), /请先检查更新/);
    assert.deepEqual(calls.filter((call) => call.startsWith('download:')), ['download:generic']);
  }
});

test('重复检查与重复下载复用操作，下载期间检查不切换源', async () => {
  const { service, calls } = setup();
  const check = service.checkForUpdates();
  assert.equal(service.checkForUpdates(), check);
  await check;
  const download = service.downloadUpdate();
  assert.equal(service.downloadUpdate(), download);
  await Promise.all([download, service.checkForUpdates()]);
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'download:generic']);
});

test('检查未结束时点击下载，会等待检查结果再下载', async () => {
  let resolve;
  const pending = new Promise((done) => { resolve = done; });
  const { service, calls } = setup({ checks: [pending] });
  const check = service.checkForUpdates();
  const download = service.downloadUpdate();
  resolve(available());
  await Promise.all([check, download]);
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'download:generic']);
});

test('窗口重建不重复注册事件，事件发送到新窗口，已销毁窗口不再发送', () => {
  const { service, updater, events, win } = setup();
  const nextEvents = [];
  let destroyed = false;
  service.forwardEvents(win);
  service.forwardEvents({ isDestroyed: () => destroyed, webContents: { send: (...args) => nextEvents.push(args) } });
  updater.emit('download-progress', { percent: 50 });
  assert.equal(updater.listenerCount('download-progress'), 1);
  assert.equal(events.length, 0);
  assert.deepEqual(nextEvents, [['update:download-progress', { percent: 50 }]]);
  destroyed = true;
  updater.emit('error', new Error('install failed'));
  assert.equal(nextEvents.length, 1);
});


test('等待中的检查失败时，下载不启动，也不重复报告错误', async () => {
  const { service, calls, events } = setup({ checks: [new Error('COS failed'), new Error('GitHub failed')] });
  const check = service.checkForUpdates();
  const download = service.downloadUpdate();
  await Promise.all([assert.rejects(check, /GitHub failed/), assert.rejects(download, /GitHub failed/)]);
  assert.equal(errors(events).length, 1);
  assert.equal(calls.some((call) => call.startsWith('download:')), false);
});

test('用户取消 COS 下载时不切换备用源', async () => {
  const cancelled = Object.assign(new Error('cancelled'), { code: 'ERR_CANCELLED' });
  const { service, calls } = setup({ downloads: [cancelled] });
  await service.checkForUpdates();
  await assert.rejects(service.downloadUpdate(), /cancelled/);
  assert.equal(calls.includes('source:github'), false);
});


test('打包环境 package.json 不含 build 时，模块可加载且 GitHub 备用源可用', async () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const os = require('node:os');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'portunus-packaged-update-'));
  try {
    const mainDir = path.join(root, 'src', 'main');
    fs.mkdirSync(mainDir, { recursive: true });
    fs.writeFileSync(path.join(root, 'package.json'), JSON.stringify({ name: '@portunus/web', version: '0.4.1' }));
    for (const name of ['update-service.js', 'update-channels.js']) {
      fs.copyFileSync(path.join(__dirname, name), path.join(mainDir, name));
    }
    const { createUpdateService: createPackagedService } = require(path.join(mainDir, 'update-service.js'));
    const feeds = [];
    const updater = new EventEmitter();
    updater.setFeedURL = (feed) => feeds.push(feed);
    updater.checkForUpdates = async () => {
      if (feeds.length === 1) throw new Error('COS offline');
      return available();
    };
    const service = createPackagedService(updater, { warn() {} });
    assert.equal((await service.checkForUpdates()).updateInfo.version, '0.5.0');
    assert.deepEqual(feeds[1], { provider: 'github', owner: 'Lixiuxiu559', repo: 'portunus' });
    assert.deepEqual(feeds[1], require('../../package.json').build.publish);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
