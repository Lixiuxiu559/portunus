const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { acknowledgeUpdate, confirmRendererReady } = require('./mac-update-health');
const { fetchSignature, createMacUpdater } = require('./mac-update-installer');

test('更新确认必须匹配目标应用、版本和 token；不允许任意路径写入', (t) => {
  const parent = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'portunus-ack-')));
  t.after(() => fs.rmSync(parent, { recursive: true, force: true }));
  const root = path.join(parent, '.portunus-update-test');
  fs.mkdirSync(root, { mode: 0o700 });
  const target = path.join(parent, 'Portunus.app');
  const token = 'a'.repeat(64);
  fs.writeFileSync(path.join(root, 'job.json'), JSON.stringify({ target, version: '0.6.0', token }));
  const argv = [`--portunus-update-root=${root}`, `--portunus-update-token=${token}`];
  const options = { executable: path.join(target, 'Contents/MacOS/Portunus'), version: '0.6.0', argv };
  assert.equal(acknowledgeUpdate({ ...options, version: '0.5.0' }), false);
  assert.equal(acknowledgeUpdate({ ...options, executable: '/Applications/Other.app/Contents/MacOS/Other' }), false);
  assert.equal(acknowledgeUpdate({ ...options, argv: [argv[0], '--portunus-update-token=wrong'] }), false);
  assert.equal(fs.existsSync(path.join(root, 'healthy')), false);
  const rendererFile = path.join(target, 'Contents/Resources/index.html');
  const frame = { url: require('node:url').pathToFileURL(rendererFile).href };
  const contents = { mainFrame: frame };
  const window = { isDestroyed: () => false, webContents: contents };
  const confirmation = { ...options, rendererFile, backendReady: true, window, event: { sender: contents, senderFrame: frame } };
  assert.equal(confirmRendererReady({ ...confirmation, backendReady: false }), false);
  assert.equal(confirmRendererReady({ ...confirmation, event: { sender: {}, senderFrame: frame } }), false);
  assert.equal(confirmRendererReady({ ...confirmation, event: { sender: contents, senderFrame: { ...frame } } }), false);
  frame.url = 'https://other.example/';
  assert.equal(confirmRendererReady(confirmation), false);
  assert.equal(fs.existsSync(path.join(root, 'healthy')), false);
  frame.url = require('node:url').pathToFileURL(rendererFile).href;
  assert.equal(confirmRendererReady(confirmation), true);
  assert.equal(fs.readFileSync(path.join(root, 'healthy'), 'utf8'), token);
});

test('签名只经 HTTPS 下载，并限制文件大小；不允许错误状态或降级重定向', async () => {
  await assert.rejects(fetchSignature('http://example.com/update.zip'), /HTTPS/);
  let requested;
  const response = (size) => ({ ok: true, url: 'https://cdn.example.com/update.zip.sig', body: new ReadableStream({
    start(controller) { controller.enqueue(new Uint8Array(size)); controller.close(); },
  }) });
  assert.equal((await fetchSignature('https://example.com/a.zip?q=1', async (url) => {
    requested = url.href; return response(64);
  })).length, 64);
  assert.equal(requested, 'https://example.com/a.zip.sig?q=1');
  await assert.rejects(fetchSignature('https://example.com/a.zip', async () => response(16385)), /过大/);
  await assert.rejects(fetchSignature('https://example.com/a.zip', async () => ({ ...response(1), url: 'http://example.com/a.sig' })), /失败/);
});

test('macOS 适配器不调用 Squirrel 安装入口；准备完成前不通知 downloaded', async () => {
  class FakeMacUpdater extends EventEmitter {
    dispatchUpdateDownloaded() { throw new Error('must not reach downloaded'); }
  }
  const updater = createMacUpdater({ MacUpdater: FakeMacUpdater, app: {}, fetcher: async () => { throw new Error('signature missing'); } });
  assert.equal(updater.supportsInstallOnQuit, false);
  await assert.rejects(updater.updateDownloaded({ url: 'https://example.com/a.zip' }, {}), /signature missing/);
  await assert.rejects(updater.quitAndInstall(), /尚未准备完成/);
});

test('发布签名脚本只接受与内置公钥匹配的私钥，签名绑定版本架构与内容', async (t) => {
  const { generateKeyPairSync, verify, createHash } = require('node:crypto');
  const { signUpdate } = await import('../../scripts/sign-mac-update.mjs');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'portunus-sign-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const archive = path.join(root, 'release.zip');
  fs.writeFileSync(archive, 'release');
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const priv = privateKey.export({ type: 'pkcs8', format: 'pem' });
  const pub = publicKey.export({ type: 'spki', format: 'pem' });
  const info = await signUpdate(archive, '0.6.0', 'arm64', priv, pub);
  assert.equal(info.sha512, createHash('sha512').update('release').digest('base64'));
  const payload = `portunus-mac-update-v1\ncom.portunus.app\n0.6.0\narm64\n7\n${info.sha512}\n`;
  assert.equal(verify(null, Buffer.from(payload), publicKey, Buffer.from(info.signature, 'base64')), true);
  const wrong = generateKeyPairSync('ed25519').publicKey.export({ type: 'spki', format: 'pem' });
  await assert.rejects(signUpdate(archive, '0.6.0', 'arm64', priv, wrong), /不匹配/);
});
