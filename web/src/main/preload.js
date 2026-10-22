const { contextBridge, ipcRenderer } = require('electron');
const { addressOf } = require('./server-address');
const { UPDATE_CHANNEL_NAMES } = require('./update-channels');

const { preloadProfile } = require('./runtime-profile');
const runtime = preloadProfile(process.argv);
const SERVER_URL = addressOf({ profile: runtime.profile }).url;

contextBridge.exposeInMainWorld('api', {
  // ─── 应用信息 ───
  appName: runtime.appName,
  isDevelopment: runtime.isDevelopment,
  updatesEnabled: runtime.updatesEnabled,
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

  // ─── 客户端配置（仅 Claude Code，路径由主进程固定）───
  // 只暴露固定通道；渲染层不能传路径，也不存在通用 invoke。
  clientConfig: {
    getPaths: () => ipcRenderer.invoke('client-config:paths'),
    readClaude: () => ipcRenderer.invoke('client-config:read-claude'),
    readBackupClaude: () => ipcRenderer.invoke('client-config:read-backup-claude'),
    saveClaude: (text) => ipcRenderer.invoke('client-config:save-claude', text),
    rollbackClaude: () => ipcRenderer.invoke('client-config:rollback-claude'),
  },

  // 打开外部链接（默认浏览器）
  openExternal: (url) => ipcRenderer.invoke('open-external', url),
});
