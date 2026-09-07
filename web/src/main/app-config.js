// 应用级配置（Electron 主进程）：userData/config.json。
// 与后端自己的 config.json（后端 CWD 下）不同，这份是 Electron 壳的本地设置，
// 由 sidecar 在拉起 Go 后端时转换成 PORTUNUS_* 环境变量注入。
//
// 字段：
//   networkMode: "local" | "lan" — 后端监听范围，默认 local（127.0.0.1，只本机）

const fs = require('fs');
const path = require('path');
const { app } = require('electron');

const DEFAULTS = { networkMode: 'local' };

function configPath() {
  return path.join(app.getPath('userData'), 'config.json');
}

/** 读取配置；文件不存在或损坏时回退默认值。 */
function read() {
  try {
    const raw = fs.readFileSync(configPath(), 'utf-8');
    const parsed = JSON.parse(raw);
    return { ...DEFAULTS, ...(parsed && typeof parsed === 'object' ? parsed : {}) };
  } catch {
    return { ...DEFAULTS };
  }
}

/** 当前监听范围："local" | "lan"。 */
function getNetworkMode() {
  return read().networkMode === 'lan' ? 'lan' : 'local';
}

/** 写入监听范围并原子落盘，返回最新配置。 */
function setNetworkMode(mode) {
  const cfg = read();
  cfg.networkMode = mode === 'lan' ? 'lan' : 'local';
  const target = configPath();
  fs.mkdirSync(path.dirname(target), { recursive: true });
  // 原子写：临时文件 + rename，避免半写损坏配置。
  const tmp = `${target}.tmp.${process.pid}`;
  fs.writeFileSync(tmp, JSON.stringify(cfg, null, 2), 'utf-8');
  fs.renameSync(tmp, target);
  return cfg;
}

/** 注册 config IPC 通道到主进程（仅纯读 get-network-mode；set 因需重启后端编排留 main.js）。 */
function register(ipcMain) {
  ipcMain.handle('config:get-network-mode', () => getNetworkMode());
}

module.exports = { register, read, getNetworkMode, setNetworkMode };