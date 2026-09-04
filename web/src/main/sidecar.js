const { app } = require('electron');

const isDev = process.env.NODE_ENV === 'development';
const SERVER_PORT = 3061;
/*
 * 说明：Go 后端不再打包进应用（不随安装包分发），
 * 生产模式下 app 直接连接本机已运行的 Go 后端（默认 3061 端口）。
 * Go 后端需由用户在外部单独启动，例如：
 *   go run . / go build -o portunus . 然后运行 ./portunus
 */

/**
 * 等待服务端就绪（轮询 /api/ping）
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

async function startServer() {
  if (isDev) {
    console.log('[sidecar] 开发模式，等待后端就绪...');
    return waitForServer(`http://localhost:${SERVER_PORT}`);
  }
  // 生产模式：后端由用户独立部署（远程服务器或本地单独启动）
  // app 不再等待/连接后端，由渲染进程根据配置的 serverUrl 直接请求
  console.log('[sidecar] 生产模式，后端由外部部署，跳过连接检测');
}

// Go 后端由用户外部启动，不归 app 管理，因此无需杀进程
function stopServer() {
  // 无内置子进程，无操作
}

module.exports = { startServer, stopServer, SERVER_PORT, isDev };
