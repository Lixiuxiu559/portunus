const { autoUpdater } = require('electron-updater');
const { createUpdateService } = require('./update-service');

module.exports = createUpdateService(autoUpdater);
