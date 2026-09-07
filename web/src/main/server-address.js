// 服务地址唯一来源（纯模块，无副作用）——「监听地址」与「内部连接地址」分离：
// - host：后端监听的网卡地址，随 networkMode 变：lan → 0.0.0.0，local → 127.0.0.1；
// - url：内部（渲染层 / 本机客户端 CLI）连接地址，恒回环 127.0.0.1，与 host 解耦；
// - baseURL：管理 API 前缀，renderer 直接当 axios baseURL 用。
//
// 端口按模式固定：开发 3060（外部 go run），打包 13060（sidecar 子进程）。
// 见 CONTEXT.md「服务地址 server-address」词条。

const DEV_PORT = 3060;
const PROD_PORT = 13060;

/** networkMode → 后端监听网卡地址。未知值回退 local（回环）。 */
function hostFor(networkMode) {
  return networkMode === 'lan' ? '0.0.0.0' : '127.0.0.1';
}

/** 依「是否打包」与「监听模式」计算完整地址。 */
function addressOf({ isPackaged, networkMode = 'local' }) {
  const port = isPackaged ? PROD_PORT : DEV_PORT;
  const host = hostFor(networkMode);
  const url = `http://127.0.0.1:${port}`;
  return { host, port, url, baseURL: `${url}/api` };
}

module.exports = { addressOf, hostFor, DEV_PORT, PROD_PORT };
