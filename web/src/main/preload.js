const { contextBridge, ipcRenderer } = require('electron');

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

  // Go 服务端状态（sidecar 占位）
  getServerPort: () => 3060,

  // 打开外部链接（默认浏览器）
  openExternal: (url) => ipcRenderer.invoke('open-external', url),
});