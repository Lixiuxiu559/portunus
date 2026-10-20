const { contextBridge, ipcRenderer } = require('electron');
const { addressOf } = require('./server-address');
const { UPDATE_CHANNEL_NAMES } = require('./update-channels');

// 后端固定本机；端口按模式区分：开发 3060（外部 go run），打包正式版 13060（sidecar）。
// 地址由 server-address 纯模块唯一产出（url 恒回环 127.0.0.1，与监听 host 解耦）。
const SERVER_URL = addressOf({ isPackaged: process.env.NODE_ENV !== 'development' }).url;

contextBridge.exposeInMainWorld('api', {
  // ─── 应用信息 ───
  getAppVersion: () => ipcRenderer.invoke('app:version'),
  // 平台标识
  platform: process.platform,

  // ─── 更新 ───
  checkForUpdate: () => ipcRenderer.invoke('update:check'),
  downloadUpdate: () => ipcRenderer.invoke('update:download'),
  installUpdate: () => ipcRenderer.invoke('update:install'),
  getUpdateState: () => ipcRenderer.invoke('update:get-state'),

  // 监听更新事件（channel 白名单来自 update-channels 单一来源）
  onUpdateEvent: (channel, callback) => {
    if (UPDATE_CHANNEL_NAMES.includes(channel)) {
      const listener = (_event, ...args) => callback(...args);
      ipcRenderer.on(channel, listener);
      return () => ipcRenderer.removeListener(channel, listener);
    }
  },

  // ─── 网络监听范围 ───
  getNetworkMode: () => ipcRenderer.invoke('config:get-network-mode'),
  setNetworkMode: (mode) => ipcRenderer.invoke('config:set-network-mode', mode),

  // 后端服务地址（固定本机）
  getServerUrl: () => SERVER_URL,

  // 打开外部链接（默认浏览器）
  openExternal: (url) => ipcRenderer.invoke('open-external', url),
});
