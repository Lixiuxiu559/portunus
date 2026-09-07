// 更新事件 IPC channel 名单一来源（main 侧共享）。
// 渲染层侧（useUpdater.js）因 vite 构建隔离无法 import 本模块，
// 经 preload 桥按同一组 channel 名订阅（见 preload.js 白名单）。
const UPDATE_CHANNELS = {
  checking: 'update:checking',
  available: 'update:available',
  notAvailable: 'update:not-available',
  downloadProgress: 'update:download-progress',
  downloaded: 'update:downloaded',
  error: 'update:error',
};

// preload 白名单用：所有合法 channel 名。
const UPDATE_CHANNEL_NAMES = Object.values(UPDATE_CHANNELS);

module.exports = { UPDATE_CHANNELS, UPDATE_CHANNEL_NAMES };
