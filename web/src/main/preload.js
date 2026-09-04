const { contextBridge, ipcRenderer } = require('electron');
const path = require('path');
const fs = require('fs');

// 读取配置文件中的后端地址
function getServerUrl() {
  try {
    const { app } = require('electron');
    const configPath = path.join(app.getPath('userData'), 'config.json');
    if (fs.existsSync(configPath)) {
      const config = JSON.parse(fs.readFileSync(configPath, 'utf-8'));
      if (config.serverUrl) return config.serverUrl;
    }
  } catch (_) {}
  return 'http://localhost:3061';
}

const SERVER_URL = getServerUrl();

contextBridge.exposeInMainWorld('api', {
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

  // 后端服务地址（从配置文件读取）
  getServerUrl: () => SERVER_URL,

  // 打开外部链接（默认浏览器）
  openExternal: (url) => ipcRenderer.invoke('open-external', url),
});

// ─── 客户端配置文件读写（Claude Code / Codex）───
contextBridge.exposeInMainWorld('clientConfig', {
  getPaths: () => ipcRenderer.invoke('client-config:paths'),
  readClaude: () => ipcRenderer.invoke('client-config:read-claude'),
  writeClaude: (cfg) => ipcRenderer.invoke('client-config:write-claude', cfg),
  readCodex: () => ipcRenderer.invoke('client-config:read-codex'),
  writeCodex: (cfg) => ipcRenderer.invoke('client-config:write-codex', cfg),
});
