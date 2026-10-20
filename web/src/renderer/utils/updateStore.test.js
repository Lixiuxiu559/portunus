import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createUpdateStore } from './updateStore.js';

function setup() {
  let receive;
  let resolveSnapshot;
  let subscriptions = 0;
  let removals = 0;
  let installs = 0;
  const api = {
    onUpdateEvent(channel, callback) {
      assert.equal(channel, 'update:state');
      receive = callback;
      subscriptions++;
      return () => { removals++; };
    },
    getUpdateState: () => new Promise((resolve) => { resolveSnapshot = resolve; }),
    installUpdate: async () => { installs++; },
  };
  const store = createUpdateStore(api);
  const snapshot = (revision, patch = {}) => ({ ...store.getSnapshot(), revision, ...patch });
  return {
    api, store, snapshot,
    emit: (value) => receive(value),
    resolve: (value) => resolveSnapshot(value),
    counts: () => ({ subscriptions, removals, installs }),
  };
}

test('全局提示与设置页共享订阅，切页及重建后仍恢复主进程的已下载状态', async () => {
  const s = setup();
  const offLayout = s.store.subscribe(() => {});
  const offSettings = s.store.subscribe(() => {});
  s.resolve(s.snapshot(4, { downloaded: true, latestVersion: '0.5.0' }));
  await Promise.resolve();
  offSettings();
  const offSettingsAgain = s.store.subscribe(() => {});
  assert.equal(s.store.getSnapshot().downloaded, true);
  assert.equal(s.store.getSnapshot().latestVersion, '0.5.0');
  assert.equal(s.counts().subscriptions, 1);
  offSettingsAgain();
  offLayout();
  assert.equal(s.counts().removals, 1);
  const off = s.store.subscribe(() => {});
  s.resolve(s.snapshot(5, { latestVersion: '0.6.0' }));
  await Promise.resolve();
  assert.equal(s.store.getSnapshot().latestVersion, '0.6.0');
  off();
});

test('下载完成事件先到时，较旧的 IPC 快照不能把状态退回下载中', async () => {
  const s = setup();
  const off = s.store.subscribe(() => {});
  s.emit(s.snapshot(8, { downloaded: true, downloading: false, latestVersion: '0.5.0' }));
  s.resolve(s.snapshot(7, { downloaded: false, downloading: true }));
  await Promise.resolve();
  assert.equal(s.store.getSnapshot().downloaded, true);
  assert.equal(s.store.getSnapshot().downloading, false);
  off();
});

test('卸载后未完成的快照不能覆盖新订阅状态', async () => {
  const s = setup();
  const off = s.store.subscribe(() => {});
  s.resolve(s.snapshot(9, { downloaded: true }));
  off();
  const offNext = s.store.subscribe(() => {});
  await Promise.resolve();
  assert.equal(s.store.getSnapshot().downloaded, false);
  s.resolve(s.snapshot(10, { downloaded: true }));
  await Promise.resolve();
  assert.equal(s.store.getSnapshot().downloaded, true);
  offNext();
});

test('下载完成前不能安装，点击重启后防止全局提示与设置页重复调用', async () => {
  const s = setup();
  const off = s.store.subscribe(() => {});
  await s.store.install();
  assert.equal(s.counts().installs, 0);
  s.emit(s.snapshot(3, { downloaded: true }));
  await Promise.all([s.store.install(), s.store.install()]);
  assert.equal(s.counts().installs, 1);
  assert.equal(s.store.getSnapshot().installing, true);
  // 与当前版本相同的延迟快照也不能重新启用按钮。
  s.resolve(s.snapshot(3, { installing: false }));
  await Promise.resolve();
  assert.equal(s.store.getSnapshot().installing, true);
  off();
});

test('安装失败保留已下载状态与错误，允许重试', async () => {
  const s = setup();
  s.api.installUpdate = async () => { throw new Error('permission denied'); };
  const off = s.store.subscribe(() => {});
  s.emit(s.snapshot(3, { downloaded: true }));
  await s.store.install();
  assert.equal(s.store.getSnapshot().downloaded, true);
  assert.equal(s.store.getSnapshot().installing, false);
  assert.equal(s.store.getSnapshot().error, 'permission denied');
  s.api.installUpdate = async () => {};
  await s.store.install();
  assert.equal(s.store.getSnapshot().installing, true);
  assert.equal(s.store.getSnapshot().error, null);
  off();
});

test('浏览器预览无 Electron 桥时可以挂载，手动检查给出明确反馈', async () => {
  const store = createUpdateStore();
  const off = store.subscribe(() => {});
  await store.check();
  assert.equal(store.getSnapshot().error, '请在桌面应用中更新');
  off();
});
