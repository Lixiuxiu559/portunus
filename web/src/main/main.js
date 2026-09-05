const { app, BrowserWindow, ipcMain, shell } = require('electron');
const path = require('path');
const { forwardEvents, checkForUpdates, downloadUpdate, quitAndInstall } = require('./updater');
const { startServer, stopServer, restartServer } = require('./sidecar');
const appConfig = require('./app-config');
const clientConfig = require('./client-config');

const isDev = process.env.NODE_ENV === 'development';

// 忽略 electron-updater 的未捕获异常（如 GitHub Releases 返回404）
process.on('uncaughtException', (err) => {
  if (err?.name === 'HttpError' || err?.message?.includes('httpExecutor')) {
    console.warn('[main] updater 非致命错误，已忽略:', err.message);
    return;
  }
  console.error('[main] 未捕获异常:', err);
});

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
      // Electron 20 起 sandbox 默认 true，会导致 preload 里 require('fs')/require('path')
      // 失效、整个 preload 执行中断。显式关闭，让 preload 能访问 Node 内建模块。
      sandbox: false,
    },
  });

  if (isDev) {
    mainWindow.loadURL('http://localhost:5173');
    mainWindow.webContents.openDevTools({ mode: 'detach' });
  } else {
    mainWindow.loadFile(path.join(__dirname, '../../dist/renderer/index.html'));
  }

  // 页面加载完成后探测 preload 桥是否注入成功（诊断 window.api / window.clientConfig）
  mainWindow.webContents.on('did-finish-load', () => {
    mainWindow.webContents
      .executeJavaScript('({ api: typeof window.api !== "undefined", clientConfig: typeof window.clientConfig !== "undefined" })')
      .then((r) => console.log('[main] preload 桥探测:', JSON.stringify(r)))
      .catch((e) => console.warn('[main] preload 桥探测失败:', e.message));
  });

  // 启动自动更新检查（仅生产模式）
  if (!isDev) {
    forwardEvents(mainWindow);
    checkForUpdates().catch(() => {
      // 静默失败
    });
  }
}

// ─── IPC：应用信息 ───
ipcMain.handle('app:version', () => app.getVersion());
ipcMain.handle('app:platform', () => process.platform);

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

// ─── IPC：网络监听范围（后端 sidecar）───
ipcMain.handle('config:get-network-mode', () => appConfig.getNetworkMode());

ipcMain.handle('config:set-network-mode', async (_event, mode) => {
  const normalized = mode === 'lan' ? 'lan' : 'local';
  appConfig.setNetworkMode(normalized);
  // 打包模式：监听范围变了要重启后端子进程才生效；开发模式后端是外部的，仅落盘。
  const restarted = app.isPackaged;
  if (restarted) {
    await restartServer();
  }
  return { ok: true, networkMode: normalized, restarted };
});

// ─── IPC：客户端配置文件读写（Claude Code / Codex）───
// 统一包成 { ok, data | error }，避免主进程异常直接冒泡到渲染层。
function wrapClientConfig(fn) {
  return async (_event, ...args) => {
    try {
      return { ok: true, data: await fn(...args) };
    } catch (err) {
      return { ok: false, error: err?.message || String(err) };
    }
  };
}

ipcMain.handle('client-config:paths', wrapClientConfig(() => clientConfig.getPaths()));
ipcMain.handle('client-config:read-claude', wrapClientConfig(() => clientConfig.readClaude()));
ipcMain.handle('client-config:read-codex', wrapClientConfig(() => clientConfig.readCodex()));
ipcMain.handle('client-config:read-backup-claude', wrapClientConfig(() => clientConfig.readBackupClaude()));
ipcMain.handle('client-config:read-backup-codex', wrapClientConfig(() => clientConfig.readBackupCodex()));
ipcMain.handle('client-config:save-claude', wrapClientConfig((t) => clientConfig.saveClaude(t)));
ipcMain.handle('client-config:save-codex', wrapClientConfig((t) => clientConfig.saveCodex(t)));
ipcMain.handle('client-config:rollback-claude', wrapClientConfig(() => clientConfig.rollbackClaude()));
ipcMain.handle('client-config:rollback-codex', wrapClientConfig(() => clientConfig.rollbackCodex()));

app.whenReady().then(() => {
  // 先创建窗口，让用户看到 UI
  createWindow();

  // 后台拉起 Go 后端（打包模式 spawn 子进程；开发模式等外部后端），不阻塞窗口显示
  startServer()
    .then(() => {
      console.log('[main] 后端服务已就绪');
    })
    .catch((err) => {
      console.error('[main] 后端服务启动失败:', err.message);
    });
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