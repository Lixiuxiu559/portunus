const { spawn } = require('child_process');
const path = require('path');
const { app } = require('electron');

const isDev = process.env.NODE_ENV === 'development';
const SERVER_PORT = 3060;

let serverProcess = null;

/**
 * 获取生产模式下的 Go 二进制路径
 * 打包时通过 electron-builder extraResources 复制到 resources/ 目录
 */
function getBinaryPath() {
  const binaryName = process.platform === 'win32' ? 'portunus.exe' : 'portunus';
  return path.join(process.resourcesPath, binaryName);
}

/**
 * 等待服务端就绪（轮询端口）
 */
function waitForServer(url, retries = 30, interval = 500) {
  return new Promise((resolve, reject) => {
    const http = require('http');
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

/**
 * 启动 Go 服务端子进程（仅生产模式）
 */
async function startServer() {
  if (isDev) {
    // 开发模式：假设开发者已手动启动 go run .
    console.log('[sidecar] 开发模式，跳过后端启动，期望服务已在 localhost:3060 运行');
    return waitForServer(`http://localhost:${SERVER_PORT}`);
  }

  const binaryPath = getBinaryPath();
  console.log(`[sidecar] 启动后端: ${binaryPath}`);

  serverProcess = spawn(binaryPath, [], {
    cwd: process.resourcesPath,
    stdio: 'pipe',
    env: { ...process.env, PORT: String(SERVER_PORT) },
  });

  serverProcess.stdout.on('data', (data) => {
    console.log(`[portunus] ${data.toString().trim()}`);
  });

  serverProcess.stderr.on('data', (data) => {
    console.error(`[portunus:err] ${data.toString().trim()}`);
  });

  serverProcess.on('exit', (code) => {
    console.log(`[sidecar] 后端进程退出，code=${code}`);
    serverProcess = null;
  });

  return waitForServer(`http://localhost:${SERVER_PORT}`);
}

/**
 * 优雅关闭 Go 服务端
 */
function stopServer() {
  if (serverProcess) {
    console.log('[sidecar] 正在关闭后端...');
    serverProcess.kill('SIGTERM');
    serverProcess = null;
  }
}

module.exports = { startServer, stopServer, SERVER_PORT, isDev };