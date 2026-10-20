// 运行身份独立于是否打包；未打包一律为 Dev，发布包必须显式烘焙 Dev 标记。
const path = require('node:path');
const fs = require('node:fs');

function profileFor({ isPackaged, metadata = {} }) {
  return !isPackaged || metadata.portunusProfile === 'development' ? 'development' : 'production';
}
function identity(profile) {
  const isDevelopment = profile === 'development';
  return { profile, isDevelopment, appName: isDevelopment ? 'Portunus Dev' : 'Portunus', updatesEnabled: !isDevelopment };
}
function initializeProfile(app, metadata) {
  const runtime = identity(profileFor({ isPackaged: app.isPackaged, metadata }));
  if (runtime.isDevelopment) {
    const root = path.join(app.getPath('appData'), 'Portunus Dev');
    app.setName(runtime.appName);
    fs.mkdirSync(path.join(root, 'session'), { recursive: true });
    app.setPath('userData', root);
    app.setPath('sessionData', path.join(root, 'session'));
    app.setAppLogsPath(path.join(root, 'logs'));
  }
  return runtime;
}
function preloadProfile(argv = []) {
  const value = argv.find((arg) => arg.startsWith('--portunus-profile='));
  // 未传身份时默认 Dev，绝不回退到正式端口。
  return identity(value === '--portunus-profile=production' ? 'production' : 'development');
}
module.exports = { profileFor, identity, initializeProfile, preloadProfile };
