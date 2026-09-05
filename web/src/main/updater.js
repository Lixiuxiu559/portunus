const { autoUpdater } = require('electron-updater');
const { BrowserWindow } = require('electron');

autoUpdater.autoDownload = false;
autoUpdater.autoInstallOnAppQuit = true;

// 更新源：GitHub Releases 为主（package.json 的 build.publish 已配 provider: github）。
//
// 兜底镜像是可选的 generic feed「根 URL」——该地址下要能取 latest.yml / latest-mac.yml
// 及对应安装包（即一个静态托管的完整更新源）。通过环境变量 PORTUNUS_UPDATE_MIRROR
// 注入；留空则只用 GitHub、不做兜底。
//
// 注意：这里不做「github 资产代理」式的镜像（把 github.com/... 前缀改写），
// 因为 electron-updater 的 github provider 依赖 api.github.com 解析「最新 tag」，
// 资产代理镜像只代理下载、不代理 API，接不上。要国内兜底，二选一：
//   1) 一个同时代理 api.github.com + github.com 的小代理；
//   2) 把 latest.yml + 安装包放到国内可达的静态地址，把该地址根喂给 MIRROR_FEED_URL。
const MIRROR_FEED_URL = process.env.PORTUNUS_UPDATE_MIRROR || '';

/**
 * 将更新事件转发到渲染进程
 * @param {BrowserWindow} win
 */
function forwardEvents(win) {
  autoUpdater.on('checking-for-update', () => {
    win.webContents.send('update:checking');
  });

  autoUpdater.on('update-available', (info) => {
    win.webContents.send('update:available', info);
  });

  autoUpdater.on('update-not-available', (info) => {
    win.webContents.send('update:not-available', info);
  });

  autoUpdater.on('download-progress', (progress) => {
    win.webContents.send('update:download-progress', progress);
  });

  autoUpdater.on('update-downloaded', (info) => {
    win.webContents.send('update:downloaded', info);
  });

  autoUpdater.on('error', (err) => {
    win.webContents.send('update:error', err.message);
  });
}

/**
 * 检查更新：先走 GitHub；失败且配了镜像 feed 时，切到镜像源重试一次。
 */
function checkForUpdates() {
  return autoUpdater.checkForUpdates().catch((err) => {
    if (MIRROR_FEED_URL) {
      console.warn('[updater] GitHub 检查失败，尝试镜像兜底:', err.message);
      autoUpdater.setFeedURL({
        provider: 'generic',
        url: MIRROR_FEED_URL,
        useMultipleRangeRequest: false,
      });
      return autoUpdater.checkForUpdates();
    }
    throw err;
  });
}

function downloadUpdate() {
  return autoUpdater.downloadUpdate();
}

function quitAndInstall() {
  autoUpdater.quitAndInstall();
}

module.exports = { forwardEvents, checkForUpdates, downloadUpdate, quitAndInstall };