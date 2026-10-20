// 仅开发预览使用；正式发布继续使用 package.json 的 build 配置。
const { build } = require('./package.json');
module.exports = {
  ...build,
  appId: 'com.portunus.app.dev',
  productName: 'Portunus Dev',
  directories: { output: 'dist-dev' },
  extraMetadata: { portunusProfile: 'development', productName: 'Portunus Dev' },
  publish: null,
  mac: { ...build.mac, identity: null, target: ['dir'] },
  linux: { ...build.linux, executableName: 'portunus-dev', target: ['dir'] },
  win: { ...build.win, target: ['dir'] },
};
