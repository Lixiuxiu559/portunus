const { UPDATE_CHANNELS } = require('./update-channels');

const COS_SOURCE = {
  provider: 'generic',
  url: 'https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/',
  useMultipleRangeRequest: false,
};
// electron-builder 会移除 package.json 的 build 字段，运行时必须独立提供备用源。
const GITHUB_SOURCE = { provider: 'github', owner: 'Lixiuxiu559', repo: 'portunus' };

// 注入 updater，允许离线验证切源与事件行为，无需启动 Electron。
function createUpdateService(autoUpdater, logger = console) {
  // 由服务调度下载，确保自动下载也经过同版本备用源校验。
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = true;

  let currentWindow = null;
  let source = null;
  let offeredVersion = null;
  let checkPromise = null;
  let downloadPromise = null;
  let downloadedFiles = null;
  let lastCheckResult = null;
  let refreshingDownload = false;
  let state = {
    revision: 0,
    checking: false,
    available: false,
    downloading: false,
    downloaded: false,
    installing: false,
    latestVersion: null,
    progress: { percent: 0, bytesPerSecond: 0 },
    error: null,
  };

  const getState = () => structuredClone(state);
  const send = (channel, ...args) => {
    if (currentWindow && !currentWindow.isDestroyed()) {
      currentWindow.webContents.send(channel, ...args);
    }
  };
  function updateState(changes) {
    state = { ...state, ...structuredClone(changes), revision: state.revision + 1 };
    send(UPDATE_CHANNELS.state, getState());
  }
  function reportError(err) {
    updateState({ checking: false, downloading: false, installing: false, error: err.message });
    send(UPDATE_CHANNELS.error, err.message);
  }

  // 在服务创建时监听，无窗口期间的下载结果仍然可以由新窗口恢复。
  autoUpdater.on('checking-for-update', () => {
    if (!refreshingDownload) send(UPDATE_CHANNELS.checking);
  });
  autoUpdater.on('update-available', (info) => {
    if (!refreshingDownload) send(UPDATE_CHANNELS.available, info);
  });
  autoUpdater.on('update-not-available', (info) => {
    if (!refreshingDownload) send(UPDATE_CHANNELS.notAvailable, info);
  });
  autoUpdater.on('download-progress', (info) => {
    updateState({ progress: info });
    send(UPDATE_CHANNELS.downloadProgress, info);
  });
  autoUpdater.on('update-downloaded', (info) => {
    updateState({
      checking: false,
      available: true,
      downloading: false,
      downloaded: true,
      latestVersion: state.latestVersion || info.version,
      progress: { ...state.progress, percent: 100 },
      error: null,
    });
    send(UPDATE_CHANNELS.downloaded, info);
  });
  autoUpdater.on('error', (err) => {
    // electron-updater 会先发 error 再 reject；受控操作只上报最终失败。
    if (!checkPromise && !downloadPromise) reportError(err);
  });

  function forwardEvents(win) {
    currentWindow = win;
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
    // 包已就绪或正在下载时保留同一版本，避免后续检查使安装包与界面不一致。
    if (downloadPromise || state.downloaded || state.installing) return Promise.resolve(structuredClone(lastCheckResult));
    if (checkPromise) return checkPromise;
    updateState({ checking: true, available: false, latestVersion: null, error: null });
    checkPromise = Promise.resolve()
      .then(checkSources)
      .then((result) => {
        // 只缓存版本元数据；真实 UpdateCheckResult 还含不可克隆的取消令牌。
        lastCheckResult = result == null ? result : {
          isUpdateAvailable: result.isUpdateAvailable,
          updateInfo: structuredClone(result.updateInfo),
          ...(result.versionInfo ? { versionInfo: structuredClone(result.versionInfo) } : {}),
        };
        offeredVersion = result?.isUpdateAvailable ? result.updateInfo.version : null;
        updateState({
          checking: false,
          available: Boolean(offeredVersion),
          latestVersion: result?.updateInfo?.version ?? null,
        });
        // 不等待下载，检查仍返回元数据；直接启动避免与 checkPromise 互相等待。
        if (offeredVersion) startDownload().catch(() => {});
        return result;
      })
      .catch((err) => {
        reportError(err);
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
      if (!offeredVersion) updateState({ available: false });
    }
    const progress = { percent: 0, bytesPerSecond: 0 };
    updateState({ progress });
    send(UPDATE_CHANNELS.downloadProgress, progress);
    return autoUpdater.downloadUpdate();
  }

  function startDownload() {
    if (downloadPromise) return downloadPromise;
    if (state.downloaded) return Promise.resolve(structuredClone(downloadedFiles));
    updateState({ downloading: true, progress: { percent: 0, bytesPerSecond: 0 }, error: null });
    downloadPromise = Promise.resolve()
      .then(downloadWithFallback)
      .then((files) => {
        downloadedFiles = structuredClone(files);
        return files;
      })
      .catch((err) => {
        reportError(err);
        throw err;
      })
      .finally(() => { downloadPromise = null; });
    return downloadPromise;
  }

  function downloadUpdate() {
    if (downloadPromise) return downloadPromise;
    if (state.downloaded) return Promise.resolve(structuredClone(downloadedFiles));
    if (checkPromise) return checkPromise.then(() => downloadUpdate());
    return startDownload();
  }

  function quitAndInstall() {
    if (state.installing) return false;
    if (!state.downloaded) throw new Error('更新尚未下载完成');
    updateState({ installing: true, error: null });
    try {
      autoUpdater.quitAndInstall(true, true);
      return true;
    } catch (err) {
      reportError(err);
      throw err;
    }
  }

  function register(ipcMain) {
    ipcMain.handle('update:get-state', () => getState());
    ipcMain.handle('update:check', () => checkForUpdates());
    ipcMain.handle('update:download', () => downloadUpdate());
    ipcMain.handle('update:install', () => quitAndInstall());
  }

  return { forwardEvents, register, getState, checkForUpdates, downloadUpdate, quitAndInstall };
}

module.exports = { createUpdateService, COS_SOURCE };
