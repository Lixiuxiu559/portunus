const { app, BrowserWindow, ipcMain, shell, nativeImage } = require('electron');
const path = require('path');
const { forwardEvents, checkForUpdates, downloadUpdate, quitAndInstall } = require('./updater');
const { createBackend } = require('./sidecar');
const { addressOf, hostFor } = require('./server-address');
const appConfig = require('./app-config');
const clientConfig = require('./client-config');

const isDev = process.env.NODE_ENV === 'development';

// 开发模式下应用跑在 node_modules 的 Electron 默认 bundle 里，Dock/任务栏显示的是
// Electron 官方图标；这里显式换成我们的 logo。打包版由 electron-builder 把
// build/icons 注入 bundle（见 package.json 的 build.*.icon），不走这条路。
// mac 的 Dock 图标须在 app ready 后用 app.dock.setIcon 设；win/linux 的任务栏
// 图标跟随窗口 icon 选项。
const appIcon = app.isPackaged
  ? null
  : (() => {
      const file = process.platform === 'win32' ? 'icons/icon.ico' : 'icons/256x256.png';
      const img = nativeImage.createFromPath(path.join(__dirname, '../../build', file));
      return img.isEmpty() ? null : img;
    })();

// 忽略 electron-updater 的未捕获异常（如 GitHub Releases 返回404）
process.on('uncaughtException', (err) => {
  if (err?.name === 'HttpError' || err?.message?.includes('httpExecutor')) {
    console.warn('[main] updater 非致命错误，已忽略:', err.message);
    return;
  }
  console.error('[main] 未捕获异常:', err);
});

let mainWindow = null;

// 后端守护（sidecar）单例：生命周期状态机。环境（二进制路径 / 数据库目录 / 端口 /
// 监听 host）在 main 进程组装；sidecar 模块本身 electron-free（见 sidecar.js）。
const backend = createBackend({
  binPath: app.isPackaged
    ? path.join(process.resourcesPath, 'bin', process.platform === 'win32' ? 'portunus.exe' : 'portunus')
    : null,
  dataDir: path.join(app.getPath('userData'), 'data'),
  port: addressOf({ isPackaged: app.isPackaged }).port,
  // 监听 host 随 networkMode 变，spawn 时现算（restart 后重读）
  host: () => hostFor(appConfig.getNetworkMode()),
  // 冷启动延迟：供 e2e 脚本（scripts/startup-race-check.mjs）注入放大启动竞态窗口
  delayMs: Number(process.env.PORTUNUS_SIDECAR_DELAY_MS || 0),
});

/**
 * 加载渲染层内容。dev 走 Vite dev server 并开 DevTools；打包走本地 file 产物。
 * 打包模式下本函数由 backend.ready 门控触发（见 createWindow）。
 */
function loadRenderer(win) {
  if (isDev) {
    win.loadURL('http://localhost:5173');
    win.webContents.openDevTools({ mode: 'detach' });
    return;
  }
  // 内容加载完成后再触发更新检查：门控拉长了「页面未加载」的窗口，若仍在
  // createWindow 时机调用，autoUpdater 事件会发到监听器尚未注册的页面而丢失。
  // 仅生产模式检查（dev 无 app-update.yml，调了只会报错）。
  win
    .loadFile(path.join(__dirname, '../../dist/renderer/index.html'))
    .then(() => checkForUpdates())
    .catch(() => {
      // 静默失败：loadFile 失败或更新检查失败都不阻塞主流程
    });
}

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
    ...(appIcon ? { icon: appIcon } : {}),
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
    // 开发模式行为保持不变：立即加载，外部后端起没起都不等
    loadRenderer(mainWindow);
  } else {
    // 打包模式：窗口壳先显示（保留原设计意图），内容等后端就绪（或失败降级）后再加载，
    // 消除「渲染层先发请求、后端尚未监听」的启动竞态红 toast。ready 恒 settle，
    // 失败也放行——让渲染层照常报错而非永久白屏。
    // 捕获窗口引用：gate 未放行期间关窗再 activate 重建时，只加载新窗口、跳过已销毁旧窗口。
    const win = mainWindow;
    backend.ready.then(() => {
      if (!win.isDestroyed()) loadRenderer(win);
    });
  }

  // 页面加载完成后探测 preload 桥是否注入成功（诊断 window.api / window.clientConfig）
  mainWindow.webContents.on('did-finish-load', () => {
    mainWindow.webContents
      .executeJavaScript('({ api: typeof window.api !== "undefined", clientConfig: typeof window.clientConfig !== "undefined" })')
      .then((r) => console.log('[main] preload 桥探测:', JSON.stringify(r)))
      .catch((e) => console.warn('[main] preload 桥探测失败:', e.message));
  });

  // 更新事件转发 dev 下也要注册：否则渲染层点"检查更新"后，
  // checking / error 事件到不了 UI，看起来就是"点了没反应"。
  forwardEvents(mainWindow);
  // 更新检查已移到 loadRenderer 的 loadFile 成功后（见彼处注释）
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
    await backend.restart();
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
  // 开发模式下把 Dock 图标换成我们的 logo（macOS 专属；app.dock 仅 ready 后可用）
  if (process.platform === 'darwin' && appIcon) {
    app.dock?.setIcon(appIcon);
  }

  // 先创建窗口，让用户看到 UI
  createWindow();

  // 后台拉起 Go 后端（打包模式 spawn 子进程；开发模式等外部后端），不阻塞窗口显示。
  // 打包模式：ready settle（就绪或失败降级）后才放行渲染层内容加载；失败也必须放行——
  // 让渲染层照常报错，而不是永久白屏。dev 模式不走门控分支。
  backend.start();
  backend.ready.then(() => {
    if (backend.state === 'ready') {
      console.log('[main] 后端服务已就绪');
    } else {
      console.error('[main] 后端服务启动失败:', backend.error);
    }
  });
});

app.on('window-all-closed', () => {
  // macOS：关窗仅收起窗口、app 留在 Dock，后端继续运行——
  // 后端是 gateway，终端里的 Claude Code 可能仍在调用，不能因关窗断连。
  // 真正退出走 Cmd+Q / Dock 右键退出，before-quit 会停后端。
  if (process.platform !== 'darwin') {
    backend.stop();
    app.quit();
  }
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    createWindow();
  }
});

app.on('before-quit', () => {
  backend.stop();
});