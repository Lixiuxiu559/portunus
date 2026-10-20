// 开发环境永远不能安装正式更新，包括直接调用 IPC 的情况。
const getState = () => ({
  revision: 0, checking: false, available: false, downloading: false,
  downloaded: false, installing: false, latestVersion: null,
  progress: { percent: 0, bytesPerSecond: 0 }, error: null,
});
const disabled = async () => { throw new Error('Portunus Dev 禁用正式更新，请重新构建开发预览'); };
module.exports = {
  getState, forwardEvents() {}, checkForUpdates: disabled,
  downloadUpdate: disabled, quitAndInstall: disabled,
  register(ipcMain) {
    ipcMain.handle('update:get-state', getState);
    for (const name of ['check', 'download', 'install']) ipcMain.handle(`update:${name}`, disabled);
  },
};
