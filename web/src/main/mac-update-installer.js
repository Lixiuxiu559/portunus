const { execFile, spawn } = require('node:child_process');
const { promisify } = require('node:util');
const fs = require('node:fs/promises');
const path = require('node:path');
const run = promisify(execFile);

async function fetchSignature(url, fetcher = fetch) {
  const signatureURL = new URL(url);
  if (signatureURL.protocol !== 'https:') throw new Error('更新签名必须通过 HTTPS 下载');
  signatureURL.pathname += '.sig';
  const response = await fetcher(signatureURL, { signal: AbortSignal.timeout(30000) });
  if (!response.ok || new URL(response.url).protocol !== 'https:') throw new Error('更新签名下载失败');
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.length;
      if (size > 16384) throw new Error('更新签名文件过大');
      chunks.push(Buffer.from(value));
    }
  } finally { await reader.cancel(); }
  return Buffer.concat(chunks);
}

function launchInstaller(root, parentPID = process.pid) {
  return new Promise((resolve, reject) => {
    const child = spawn(path.join(root, 'installer'), ['--install', root, '--parent', String(parentPID)], {
      detached: true, stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';
    const timer = setTimeout(() => fail(new Error('更新程序准备超时')), 60000);
    const fail = (error) => {
      clearTimeout(timer);
      child.kill();
      reject(new Error(`${error.message}；诊断目录: ${root}`));
    };
    child.once('error', fail);
    child.once('exit', (code) => fail(new Error(`更新程序在替换前退出 (${code})`)));
    child.stdout.on('data', (chunk) => {
      output += chunk;
      if (output.includes('{"ready":true}')) {
        clearTimeout(timer);
        child.removeAllListeners('error');
        child.removeAllListeners('exit');
        child.stdout.destroy(); child.stderr.destroy();
        child.unref();
        resolve();
      }
    });
    // helper 将详细诊断写入事务目录，消费 stderr 避免管道阻塞。
    child.stderr.resume();
  });
}

function createMacUpdater({ MacUpdater, app, resourcesPath = process.resourcesPath, fetcher = fetch }) {
  class PortunusMacUpdater extends MacUpdater {
    supportsInstallOnQuit = false;
    installRoot = null;
    async updateDownloaded(fileInfo, event) {
      const signature = await fetchSignature(fileInfo.url, fetcher);
      const signatureFile = `${event.downloadedFile}.sig`;
      await fs.writeFile(signatureFile, signature, { mode: 0o600 });
      const helper = path.join(resourcesPath, 'bin/portunus-updater');
      const target = path.dirname(path.dirname(path.dirname(app.getPath('exe'))));
      const { stdout } = await run(helper, ['--prepare', '--archive', event.downloadedFile, '--signature', signatureFile,
        '--target', target, '--version', event.version, '--current', app.getVersion()], { timeout: 180000, maxBuffer: 1024 * 1024 });
      this.installRoot = JSON.parse(stdout).root;
      this.dispatchUpdateDownloaded(event);
    }
    async quitAndInstall() {
      if (!this.installRoot) throw new Error('更新尚未准备完成');
      await launchInstaller(this.installRoot);
      app.quit();
    }
  }
  return new PortunusMacUpdater();
}

module.exports = { createMacUpdater, fetchSignature, launchInstaller };
