const { autoUpdater } = require('electron-updater');
const { BrowserWindow } = require('electron');

autoUpdater.autoDownload = false;
autoUpdater.autoInstallOnAppQuit = true;

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

function checkForUpdates() {
  return autoUpdater.checkForUpdates();
}

function downloadUpdate() {
  return autoUpdater.downloadUpdate();
}

function quitAndInstall() {
  autoUpdater.quitAndInstall();
}

module.exports = { forwardEvents, checkForUpdates, downloadUpdate, quitAndInstall };