const { app } = require('electron');
const { profileFor } = require('./runtime-profile');

// Dev 不加载 electron-updater，避免接触正式更新缓存或安装器。
if (profileFor({ isPackaged: app.isPackaged, metadata: require('../../package.json') }) === 'development') {
  module.exports = require('./disabled-updater');
} else {
  const updaterModule = require('electron-updater');
  const autoUpdater = process.platform === 'darwin'
    ? require('./mac-update-installer').createMacUpdater({ MacUpdater: updaterModule.MacUpdater, app })
    : updaterModule.autoUpdater;
  const { createUpdateService } = require('./update-service');
  module.exports = createUpdateService(autoUpdater);
}
