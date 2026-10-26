const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');

// 只确认与当前运行 bundle、版本和随机 token 一致的私有更新事务。
function acknowledgeUpdate({ executable, version, argv = process.argv }) {
  const root = argv.find((arg) => arg.startsWith('--portunus-update-root='))?.split('=').slice(1).join('=');
  const token = argv.find((arg) => arg.startsWith('--portunus-update-token='))?.split('=').slice(1).join('=');
  if (!root || !token) return false;
  try {
    const target = path.dirname(path.dirname(path.dirname(executable)));
    if (!path.isAbsolute(root) || fs.realpathSync(root) !== root || path.dirname(root) !== path.dirname(target)
      || !path.basename(root).startsWith('.portunus-update-') || (fs.statSync(root).mode & 0o777) !== 0o700) return false;
    const job = JSON.parse(fs.readFileSync(path.join(root, 'job.json'), 'utf8'));
    if (!/^[a-f0-9]{64}$/.test(token) || job.token !== token || job.target !== target || job.version !== version) return false;
    fs.writeFileSync(path.join(root, 'healthy'), token, { mode: 0o600, flag: 'wx' });
    return true;
  } catch (error) {
    console.warn('[updater] 无法确认更新启动:', error.message);
    return false;
  }
}

function confirmRendererReady({ event, window, backendReady, rendererFile, executable, version, argv }) {
  if (!window || window.isDestroyed() || !backendReady || event.sender !== window.webContents
    || event.senderFrame !== window.webContents.mainFrame
    || event.senderFrame.url.split('#')[0] !== pathToFileURL(rendererFile).href) return false;
  return acknowledgeUpdate({ executable, version, argv });
}

module.exports = { acknowledgeUpdate, confirmRendererReady };
