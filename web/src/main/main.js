const { app, BrowserWindow, ipcMain, shell } = require('electron');
const path = require('path');
const { forwardEvents, checkForUpdates, downloadUpdate, quitAndInstall } = require('./updater');
const { startServer, stopServer, SERVER_PORT, isDev: sidecarIsDev } = require('./sidecar');

const isDev = process.env.NODE_ENV === 'development';

let mainWindow = null;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 720,
    minWidth: 1024,
    minHeight: 576,
    resizable: true,
    title: 'Portunus',
    titleBarStyle: 'hiddenInset',
    trafficLightPosition: { x: 16, y: 16 },
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  if (isDev) {
    mainWindow.loadURL('http://localhost:5173');
    mainWindow.webContents.openDevTools({ mode: 'detach' });
  } else {
    mainWindow.loadFile(path.join(__dirname, '../../dist/renderer/index.html'));
  }

  // 启动自动更新检查（仅生产模式）
  if (!isDev) {
    forwardEvents(mainWindow);
    checkForUpdates().catch(() => {
      // 静默失败
    });
  }
}

// ─── IPC：更新操作 ───
ipcMain.handle('update:check', () => checkForUpdates());
ipcMain.handle('update:download', () => downloadUpdate());
ipcMain.handle('update:install', () => quitAndInstall());

// ─── IPC：打开外部链接 ───
ipcMain.handle('open-external', (_event, url) => {
  // 仅允许 http/https，防止被渲染进程滥用打开任意协议
  if (typeof url === 'string' && /^https?:\/\//i.test(url)) {
    return shell.openExternal(url);
  }
  return Promise.reject(new Error('不支持的链接'));
});

app.whenReady().then(async () => {
  // 先启动 Go 后端，再创建窗口
  try {
    await startServer();
    console.log('[main] 后端服务已就绪');
  } catch (err) {
    console.error('[main] 后端服务启动失败:', err.message);
    // 即使后端没起来也创建窗口，让用户看到 UI
  }
  createWindow();
});

app.on('window-all-closed', () => {
  stopServer();
  if (process.platform !== 'darwin') {
    app.quit();
  }
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
});

app.on('before-quit', () => {
  stopServer();
});