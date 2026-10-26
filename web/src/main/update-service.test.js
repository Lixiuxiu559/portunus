const { test } = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { createUpdateService, COS_SOURCE } = require('./update-service');

const available = (version = '0.5.0') => ({ isUpdateAvailable: true, updateInfo: { version } });
const unavailable = { isUpdateAvailable: false, updateInfo: { version: '0.4.0' } };

function setup({ checks = [available()], downloads = [['installer.zip']], attachWindow = true } = {}) {
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
    const files = await result;
    updater.emit('update-downloaded', { version: '0.5.0' });
    return files;
  };
  updater.quitAndInstall = (...args) => calls.push(['install', ...args]);
  const service = createUpdateService(updater, { warn() {} });
  const win = { isDestroyed: () => false, webContents: { send: (...args) => events.push(args) } };
  if (attachWindow) service.forwardEvents(win);
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
    assert.equal(service.getState().latestVersion, result?.updateInfo?.version ?? null);
    assert.deepEqual(calls, ['source:generic', 'check:generic']);
    await assert.rejects(service.downloadUpdate(), /请先检查更新/);
  }
});

test('两个源检查都失败：只向界面报告最终错误，并允许重新从 COS 检查', async () => {
  const { service, calls, events } = setup({ checks: [new Error('COS 404'), new Error('GitHub offline'), available()] });
  await assert.rejects(service.checkForUpdates(), /GitHub offline/);
  assert.deepEqual(errors(events), [['update:error', 'GitHub offline']]);
  await service.checkForUpdates();
  await service.downloadUpdate();
  assert.deepEqual(calls.slice(-3), ['source:generic', 'check:generic', 'download:generic']);
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
  assert.deepEqual(nextEvents.filter(([channel]) => channel !== 'update:state'), [['update:download-progress', { percent: 50 }]]);
  assert.equal(nextEvents[0][1].progress.percent, 50);
  destroyed = true;
  updater.emit('error', new Error('install failed'));
  assert.equal(nextEvents.length, 2);
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
    updater.downloadUpdate = async () => [];
    const service = createPackagedService(updater, { warn() {} });
    assert.equal((await service.checkForUpdates()).updateInfo.version, '0.5.0');
    assert.deepEqual(feeds[1], { provider: 'github', owner: 'Lixiuxiu559', repo: 'portunus' });
    assert.deepEqual(feeds[1], require('../../package.json').build.publish);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});


const settle = () => new Promise((resolve) => setImmediate(resolve));

test('无窗口时检查到更新仍自动下载，窗口恢复后可获取权威快照', async () => {
  const { service, calls, events, win } = setup({ attachWindow: false });
  const initial = service.getState();
  assert.deepEqual(initial, {
    revision: 0, checking: false, available: false, downloading: false,
    downloaded: false, installing: false, latestVersion: null,
    progress: { percent: 0, bytesPerSecond: 0 }, error: null,
  });
  await service.checkForUpdates();
  await settle();
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'download:generic']);
  assert.equal(events.length, 0);
  service.forwardEvents(win);
  const handlers = new Map();
  service.register({ handle: (channel, handler) => handlers.set(channel, handler) });
  const restored = handlers.get('update:get-state')();
  assert.equal(restored.downloaded, true);
  assert.equal(restored.latestVersion, '0.5.0');
  assert.equal(restored.progress.percent, 100);
  assert.ok(restored.revision > initial.revision);
});

test('检查返回元数据而不等待后台下载，多次检查和下载不重复请求', async () => {
  let resolveDownload;
  const pending = new Promise((resolve) => { resolveDownload = resolve; });
  const { service, calls } = setup({ downloads: [pending] });
  const metadata = await service.checkForUpdates();
  assert.equal(metadata.updateInfo.version, '0.5.0');
  assert.equal(service.getState().downloading, true);
  const download = service.downloadUpdate();
  assert.equal(service.downloadUpdate(), download);
  assert.deepEqual(await service.checkForUpdates(), metadata);
  assert.equal(calls.filter((call) => call.startsWith('check:')).length, 1);
  assert.throws(() => service.quitAndInstall(), /尚未下载完成/);
  resolveDownload(['installer.zip']);
  await download;
  assert.equal(service.getState().downloaded, true);
  assert.equal(service.getState().downloading, false);
});

test('自动下载失败可手动重试，清除错误并在下载完成后准备安装', async () => {
  const cancelled = Object.assign(new Error('cancelled'), { code: 'ERR_CANCELLED' });
  const { service, calls, events } = setup({ downloads: [cancelled, ['installer.zip']] });
  await service.checkForUpdates();
  await settle();
  assert.equal(service.getState().error, 'cancelled');
  assert.equal(service.getState().downloading, false);
  assert.equal(service.getState().downloaded, false);
  assert.deepEqual(await service.downloadUpdate(), ['installer.zip']);
  assert.equal(service.getState().error, null);
  assert.equal(service.getState().downloaded, true);
  assert.equal(calls.filter((call) => call.startsWith('download:')).length, 2);
  assert.equal(errors(events).length, 1);
});

test('已下载版本保持一致，重复下载和检查复用缓存，三端均静默安装并重启一次', async () => {
  const { service, calls } = setup({ checks: [available(), available('0.6.0')] });
  await service.checkForUpdates();
  await service.downloadUpdate();
  assert.equal((await service.checkForUpdates()).updateInfo.version, '0.5.0');
  assert.deepEqual(await service.downloadUpdate(), ['installer.zip']);
  assert.equal(service.getState().latestVersion, '0.5.0');
  assert.equal(service.quitAndInstall(), true);
  assert.equal(service.quitAndInstall(), false);
  assert.equal(service.getState().installing, true);
  assert.deepEqual(calls, ['source:generic', 'check:generic', 'download:generic', ['install', true, true]]);
});

test('状态查询和广播快照不能被外部修改，revision 随状态变化递增', async () => {
  const { service, updater, events } = setup();
  const snapshot = service.getState();
  snapshot.progress.percent = 80;
  snapshot.error = 'tampered';
  assert.equal(service.getState().progress.percent, 0);
  assert.equal(service.getState().error, null);
  updater.emit('download-progress', { percent: 30, bytesPerSecond: 100 });
  const broadcast = events.find(([channel]) => channel === 'update:state')[1];
  broadcast.progress.percent = 90;
  assert.equal(service.getState().progress.percent, 30);
  await service.checkForUpdates();
  await service.downloadUpdate();
  const revisions = events.filter(([channel]) => channel === 'update:state').map(([, value]) => value.revision);
  assert.ok(revisions.every((revision, index) => index === 0 || revision > revisions[index - 1]));
});

test('安装器抛错时保留已下载更新并允许重新安装', async () => {
  const { service, updater } = setup();
  assert.throws(() => service.quitAndInstall(), /尚未下载完成/);
  await service.checkForUpdates();
  await service.downloadUpdate();
  updater.quitAndInstall = () => { throw new Error('installer failed'); };
  assert.throws(() => service.quitAndInstall(), /installer failed/);
  assert.equal(service.getState().installing, false);
  assert.equal(service.getState().downloaded, true);
  updater.quitAndInstall = (...args) => assert.deepEqual(args, [true, true]);
  assert.equal(service.quitAndInstall(), true);
});


test('缓存仅保留版本元数据，不克隆更新器内部取消令牌', async () => {
  const result = { ...available(), cancellationToken: { cancel() {} }, downloadPromise: null };
  const { service } = setup({ checks: [result] });
  assert.equal(await service.checkForUpdates(), result);
  await service.downloadUpdate();
  const cached = await service.checkForUpdates();
  assert.deepEqual(cached, available());
  cached.updateInfo.version = 'tampered';
  assert.equal((await service.checkForUpdates()).updateInfo.version, '0.5.0');
});

test('独立安装器异步准备失败时恢复可重试状态，重试成功后退出', async () => {
  const { service, updater } = setup();
  await service.checkForUpdates();
  await service.downloadUpdate();
  updater.quitAndInstall = async () => { throw new Error('helper preflight failed'); };
  await assert.rejects(service.quitAndInstall(), /helper preflight failed/);
  assert.equal(service.getState().installing, false);
  assert.equal(service.getState().downloaded, true);
  updater.quitAndInstall = async () => {};
  assert.equal(await service.quitAndInstall(), true);
});
