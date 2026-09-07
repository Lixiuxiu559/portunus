// sidecar 子进程管理：打包模式下拉起 Go 后端，开发模式下等外部后端。
//
// 打包模式（app.isPackaged）：
//   Go 后端二进制随安装包分发在 extraResources 的 bin/ 下，由本模块 spawn 拉起，
//   通过 PORTUNUS_* 环境变量把「数据库落 userData」「监听地址按 networkMode」
//   「端口 13060」注入，避免后端读 CWD 下的相对路径（打包后 CWD 不可控）。
//
// 开发模式：后端由开发者用 go run / go build 单独启动，这里只轮询等它就绪。

const { app } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const http = require('http');
const appConfig = require('./app-config');

const isPackaged = app.isPackaged;
// 端口按模式错开：开发模式走外部 go run 后端（3060），
// 打包正式版的内置后端用 13060——两者同时跑（边开发边用正式版）不抢端口。
const SERVER_PORT_DEV = 3060;
const SERVER_PORT_PROD = 13060;
const SERVER_PORT = isPackaged ? SERVER_PORT_PROD : SERVER_PORT_DEV;

let child = null;

/**
 * 轮询 /api/ping 直到后端就绪。
 */
function waitForServer(url, retries = 60, interval = 500) {
  return new Promise((resolve, reject) => {
    let attempts = 0;
    const check = () => {
      attempts++;
      http
        .get(`${url}/api/ping`, (res) => {
          if (res.statusCode === 200) {
            resolve();
          } else if (attempts < retries) {
            setTimeout(check, interval);
          } else {
            reject(new Error('服务端启动超时'));
          }
        })
        .on('error', () => {
          if (attempts < retries) {
            setTimeout(check, interval);
          } else {
            reject(new Error('服务端启动超时'));
          }
        });
    };
    check();
  });
}

/** 打包后 Go 二进制路径（extraResources: bin/）。开发模式不适用，返回 null。 */
function backendBinPath() {
  if (!isPackaged) return null;
  const name = process.platform === 'win32' ? 'portunus.exe' : 'portunus';
  return path.join(process.resourcesPath, 'bin', name);
}

/** 按 networkMode 解析后端监听地址。 */
function hostFor(networkMode) {
  return networkMode === 'lan' ? '0.0.0.0' : '127.0.0.1';
}

/** 拉起后端子进程（仅打包模式）；返回就绪 Promise。 */
function spawnBackend() {
  const bin = backendBinPath();
  if (!bin) {
    return Promise.reject(new Error('后端二进制不存在'));
  }
  if (!fs.existsSync(bin)) {
    return Promise.reject(new Error(`后端二进制缺失: ${bin}`));
  }

  const dataDir = path.join(app.getPath('userData'), 'data');
  fs.mkdirSync(dataDir, { recursive: true });

  const env = {
    ...process.env,
    // 监听范围随用户设置；端口按模式固定（开发 3060 / 打包 13060），渲染层与外部客户端工具都按它连
    PORTUNUS_SERVER_HOST: hostFor(appConfig.getNetworkMode()),
    PORTUNUS_SERVER_PORT: String(SERVER_PORT),
    // 数据库落 userData（升级时 app 目录会被整体替换，userData 不会丢）
    PORTUNUS_DATABASE_PATH: path.join(dataDir, 'portunus.db'),
    PORTUNUS_DATABASE_LOG_PATH: path.join(dataDir, 'portunus-log.db'),
  };

  child = spawn(bin, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  child.stdout.on('data', (d) => console.log('[backend]', d.toString().trimEnd()));
  child.stderr.on('data', (d) => console.error('[backend]', d.toString().trimEnd()));
  child.on('exit', (code) => {
    console.log(`[sidecar] 后端退出，code=${code}`);
    child = null;
  });

  return waitForServer(`http://127.0.0.1:${SERVER_PORT}`);
}

/** 停止后端子进程。 */
function stopServer() {
  if (child) {
    child.kill();
    child = null;
  }
}

/** 重启后端子进程（网络模式切换后调用），返回就绪 Promise。 */
function restartServer() {
  stopServer();
  // 等旧进程端口释放，再拉起新进程
  return new Promise((resolve) => setTimeout(resolve, 300)).then(spawnBackend);
}

/**
 * 启动后端，返回就绪 Promise（不阻塞窗口显示）。
 * 打包模式：spawn 随包二进制；开发模式：等外部后端。
 */
async function startServer() {
  if (isPackaged) {
    // 测试钩子（仅测试用，勿在生产环境设置）：延迟拉起后端，放大「窗口先加载、
    // 后端后就绪」的启动竞态窗口，供 scripts/startup-race-check.mjs 确定性复现。
    // 默认 0 无行为变化；只作用冷启动，不影响 restartServer。
    const delayMs = Number(process.env.PORTUNUS_SIDECAR_DELAY_MS || 0);
    if (delayMs > 0) {
      console.log(`[sidecar] 测试钩子：延迟 ${delayMs}ms 再启动后端`);
      await new Promise((resolve) => setTimeout(resolve, delayMs));
    }
    return spawnBackend();
  }
  console.log('[sidecar] 开发模式：等待外部后端就绪...');
  return waitForServer(`http://localhost:${SERVER_PORT}`);
}

module.exports = { startServer, stopServer, restartServer, SERVER_PORT, isPackaged };