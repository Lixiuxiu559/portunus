const { UPDATE_CHANNELS } = require('./update-channels');

const COS_SOURCE = {
  provider: 'generic',
  url: 'https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/',
  useMultipleRangeRequest: false,
};
const GITHUB_SOURCE = require('../../package.json').build.publish;

// 注入 updater，允许离线验证切源与事件行为，无需启动 Electron。
function createUpdateService(autoUpdater, logger = console) {
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = true;

  let currentWindow = null;
  let forwarded = false;
  let source = null;
  let offeredVersion = null;
  let checkPromise = null;
  let downloadPromise = null;
  let refreshingDownload = false;

  const send = (channel, ...args) => {
    if (currentWindow && !currentWindow.isDestroyed()) {
      currentWindow.webContents.send(channel, ...args);
    }
  };

  function forwardEvents(win) {
    currentWindow = win;
    if (forwarded) return;
    forwarded = true;
    for (const [event, channel] of [
      ['checking-for-update', UPDATE_CHANNELS.checking],
      ['update-available', UPDATE_CHANNELS.available],
      ['update-not-available', UPDATE_CHANNELS.notAvailable],
    ]) {
      autoUpdater.on(event, (info) => {
        // 下载切源时重新检查元数据，不改变用户已经确认的更新版本。
        if (!refreshingDownload) send(channel, info);
      });
    }
    autoUpdater.on('download-progress', (info) => send(UPDATE_CHANNELS.downloadProgress, info));
    autoUpdater.on('update-downloaded', (info) => send(UPDATE_CHANNELS.downloaded, info));
    autoUpdater.on('error', (err) => {
      // electron-updater 会先发 error 再 reject；受控操作只上报最终失败。
      if (!checkPromise && !downloadPromise) send(UPDATE_CHANNELS.error, err.message);
    });
  }

  function selectSource(next) {
    autoUpdater.setFeedURL(next);
    source = next;
  }

  async function checkSources() {
    offeredVersion = null;
    try {
      selectSource(COS_SOURCE);
      return await autoUpdater.checkForUpdates();
    } catch (err) {
      logger.warn('[updater] COS 检查失败，切换 GitHub:', err.message);
      selectSource(GITHUB_SOURCE);
      return autoUpdater.checkForUpdates();
    }
  }

  function checkForUpdates() {
    // 下载期间不切换 provider；重复检查共享同一个操作。
    if (downloadPromise) return downloadPromise.then(() => null);
    if (checkPromise) return checkPromise;
    checkPromise = Promise.resolve()
      .then(checkSources)
      .then((result) => {
        if (result?.isUpdateAvailable) offeredVersion = result.updateInfo.version;
        return result;
      })
      .catch((err) => {
        send(UPDATE_CHANNELS.error, err.message);
        throw err;
      })
      .finally(() => { checkPromise = null; });
    return checkPromise;
  }

  async function downloadWithFallback() {
    if (!offeredVersion) throw new Error('请先检查更新，确认有可用的新版本');
    const version = offeredVersion;
    try {
      return await autoUpdater.downloadUpdate();
    } catch (err) {
      if (source !== COS_SOURCE || err.code === 'ERR_CANCELLED') throw err;
      logger.warn('[updater] COS 下载失败，切换 GitHub:', err.message);
    }

    // setFeedURL 不会更新已缓存的下载信息，必须重新检查备用源。
    offeredVersion = null;
    refreshingDownload = true;
    try {
      selectSource(GITHUB_SOURCE);
      const result = await autoUpdater.checkForUpdates();
      if (!result?.isUpdateAvailable || result.updateInfo.version !== version) {
        throw new Error(`GitHub 备用源尚未提供版本 ${version}，请稍后重新检查更新`);
      }
      offeredVersion = version;
    } finally {
      refreshingDownload = false;
    }
    send(UPDATE_CHANNELS.downloadProgress, { percent: 0, bytesPerSecond: 0 });
    return autoUpdater.downloadUpdate();
  }

  function downloadUpdate() {
    if (downloadPromise) return downloadPromise;
    if (checkPromise) return checkPromise.then(() => downloadUpdate());
    downloadPromise = Promise.resolve()
      .then(downloadWithFallback)
      .catch((err) => {
        send(UPDATE_CHANNELS.error, err.message);
        throw err;
      })
      .finally(() => { downloadPromise = null; });
    return downloadPromise;
  }

  function quitAndInstall() {
    autoUpdater.quitAndInstall();
  }

  function register(ipcMain) {
    ipcMain.handle('update:check', () => checkForUpdates());
    ipcMain.handle('update:download', () => downloadUpdate());
    ipcMain.handle('update:install', () => quitAndInstall());
  }

  return { forwardEvents, register, checkForUpdates, downloadUpdate, quitAndInstall };
}

module.exports = { createUpdateService, COS_SOURCE };
