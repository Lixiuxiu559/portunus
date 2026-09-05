const { contextBridge, ipcRenderer } = require('electron');

// 后端固定本机（sidecar 打包或外部后端都在 127.0.0.1:3061）
const SERVER_URL = 'http://localhost:3061';

contextBridge.exposeInMainWorld('api', {
  // ─── 应用信息 ───
  getAppVersion: () => ipcRenderer.invoke('app:version'),
  // 平台标识（渲染进程据此区分 mac 半自动 / win 全自动更新）
  platform: process.platform,

  // ─── 更新 ───
  checkForUpdate: () => ipcRenderer.invoke('update:check'),
  downloadUpdate: () => ipcRenderer.invoke('update:download'),
  installUpdate: () => ipcRenderer.invoke('update:install'),

  // 监听更新事件
  onUpdateEvent: (channel, callback) => {
    const validChannels = [
      'update:checking',
      'update:available',
      'update:not-available',
      'update:download-progress',
      'update:downloaded',
      'update:error',
    ];
    if (validChannels.includes(channel)) {
      ipcRenderer.on(channel, (_event, ...args) => callback(...args));
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

// ─── 客户端配置文件读写（Claude Code / Codex）───
contextBridge.exposeInMainWorld('clientConfig', {
  getPaths: () => ipcRenderer.invoke('client-config:paths'),
  readClaude: () => ipcRenderer.invoke('client-config:read-claude'),
  readCodex: () => ipcRenderer.invoke('client-config:read-codex'),
  readBackupClaude: () => ipcRenderer.invoke('client-config:read-backup-claude'),
  readBackupCodex: () => ipcRenderer.invoke('client-config:read-backup-codex'),
  saveClaude: (text) => ipcRenderer.invoke('client-config:save-claude', text),
  saveCodex: (text) => ipcRenderer.invoke('client-config:save-codex', text),
  rollbackClaude: () => ipcRenderer.invoke('client-config:rollback-claude'),
  rollbackCodex: () => ipcRenderer.invoke('client-config:rollback-codex'),
});