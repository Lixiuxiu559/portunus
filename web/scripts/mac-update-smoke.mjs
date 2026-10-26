// 在临时目录运行真实 Electron + Go 后端 + HTTPS 下载 + 原生 helper。
// 不启动或修改 /Applications/Portunus.app，不使用正式/Dev 数据和端口。
import { createServer } from 'node:https';
import { createServer as tcpServer } from 'node:net';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createReadStream, existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync, statSync, renameSync } from 'node:fs';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { signUpdate } from './sign-mac-update.mjs';

if (process.platform !== 'darwin') throw new Error('本验收仅支持 macOS');
const scenarios = process.argv.slice(2);
if (scenarios.some((name) => !['success', 'tamper', 'rollback'].includes(name))) throw new Error('场景仅支持 success tamper rollback');
const web = resolve(import.meta.dirname, '..');
const repo = resolve(web, '..');
const root = realpathSync(await mkdtemp(join(tmpdir(), 'portunus-mac-update-smoke-')));
const publicPEM = readFileSync(join(repo, 'cmd/portunus-updater/public-key.pem'));
const privatePEM = process.env.PORTUNUS_UPDATE_SIGNING_KEY || readFileSync(
  process.env.PORTUNUS_UPDATE_SIGNING_KEY_FILE || join(process.env.HOME, '.config/portunus/update-signing-key.pem'));
const delay = (ms) => new Promise((r) => setTimeout(r, ms));
function run(bin, args) {
  const result = spawnSync(bin, args, { encoding: 'utf8', maxBuffer: 1024 * 1024 });
  if (result.status !== 0) throw new Error(`${bin}: ${result.stderr || result.stdout}`);
}
async function waitFor(check, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { const result = check(); if (result) return result; await delay(100); }
  throw new Error(`等待验收结果超时；诊断目录 ${root}`);
}
async function freePort() {
  const server = tcpServer(); await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const port = server.address().port; await new Promise((r) => server.close(r)); return port;
}
const config = join(root, 'openssl.cnf');
writeFileSync(config, '[req]\nprompt=no\ndistinguished_name=dn\nx509_extensions=v3\n[dn]\nCN=127.0.0.1\n[v3]\nsubjectAltName=IP:127.0.0.1\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,digitalSignature,keyEncipherment,keyCertSign\n');
run('/usr/bin/openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', join(root, 'tls.key'), '-out', join(root, 'tls.crt'), '-days', '1', '-config', config]);
const routes = new Map();
const requests = [];
const server = createServer({ key: readFileSync(join(root, 'tls.key')), cert: readFileSync(join(root, 'tls.crt')) }, (req, res) => {
  const name = new URL(req.url, 'https://localhost').pathname;
  requests.push(name);
  const file = routes.get(name);
  if (!file) { res.writeHead(404); res.end(); return; }
  res.setHeader('Content-Length', statSync(file).size);
  createReadStream(file).pipe(res);
});
await new Promise((r) => server.listen(0, '127.0.0.1', r));
const base = `https://127.0.0.1:${server.address().port}`;

const fixtureMain = `
const {app,BrowserWindow,session,ipcMain}=require('electron');
const fs=require('node:fs'),path=require('node:path');
const dir=process.env.PORTUNUS_SMOKE_CASE;
app.setPath('userData',path.join(dir,'userData'));
app.on('certificate-error',(event,contents,url,error,certificate,callback)=>{
 if(new URL(url).hostname==='127.0.0.1'){event.preventDefault();callback(true)}else callback(false);
});
const {createBackend}=require(${JSON.stringify(join(web, 'src/main/sidecar.js'))});
const backend=createBackend({binPath:path.join(process.resourcesPath,'bin/portunus'),dataDir:path.join(dir,'data'),cwd:dir,
 port:Number(process.env.PORTUNUS_SMOKE_PORT),host:()=> '127.0.0.1'});
let quitting=false,quitPromise;
app.on('before-quit',event=>{if(!quitting){event.preventDefault();quitPromise??=backend.stopAndWait().then(()=>{quitting=true;app.quit()})}});
process.on('SIGTERM',()=>app.quit());
let win;
ipcMain.handle('update:renderer-ready',event=>require(${JSON.stringify(join(web, 'src/main/mac-update-health.js'))}).confirmRendererReady({
 event,window:win,backendReady:backend.state==='ready',rendererFile:path.join(process.resourcesPath,'app/index.html'),executable:app.getPath('exe'),version:app.getVersion()
}));
app.whenReady().then(async()=>{
 session.defaultSession.setCertificateVerifyProc((request,callback)=>callback(request.hostname==='127.0.0.1'?0:-3));
 session.fromPartition('electron-updater',{cache:false}).setCertificateVerifyProc((request,callback)=>callback(request.hostname==='127.0.0.1'?0:-3));
 await backend.start(); await backend.ready;
 if(backend.state!=='ready')throw new Error(backend.error);
 win=new BrowserWindow({show:false,webPreferences:{preload:path.join(process.resourcesPath,'app/preload.cjs'),sandbox:false}});
 await win.loadFile(path.join(process.resourcesPath,'app/index.html'));
 fs.writeFileSync(path.join(dir,'started-'+app.getVersion()+'.json'),JSON.stringify({pid:process.pid,version:app.getVersion()}));
 if(app.getVersion()==='0.6.0')return;
 if(fs.existsSync(path.join(dir,'initiated')))return;
 fs.writeFileSync(path.join(dir,'initiated'),'yes');
 const {MacUpdater}=require(${JSON.stringify(join(web, 'node_modules/electron-updater'))});
 const {createMacUpdater}=require(${JSON.stringify(join(web, 'src/main/mac-update-installer.js'))});
 const {createUpdateService}=require(${JSON.stringify(join(web, 'src/main/update-service.js'))});
 const updater=createMacUpdater({MacUpdater,app});updater.disableDifferentialDownload=true;updater.logger=console;
 Object.defineProperty(updater.app,'baseCachePath',{get:()=>path.join(dir,'cache')});
 const source={provider:'generic',url:process.env.PORTUNUS_SMOKE_URL,useMultipleRangeRequest:false};
 const service=createUpdateService(updater,console,{primarySource:source,fallbackSource:source});
 try{
  await service.checkForUpdates();await service.downloadUpdate();
  const files=await service.downloadUpdate();
  const prepared=JSON.parse(require('node:child_process').execFileSync(path.join(process.resourcesPath,'bin/portunus-updater'),[
   '--prepare','--archive',files[0],'--signature',files[0]+'.sig','--target',path.dirname(path.dirname(path.dirname(app.getPath('exe')))),
   '--version','0.6.0','--current',app.getVersion()
  ],{encoding:'utf8'}));
  if(prepared.root!==updater.installRoot)throw new Error('重复启动未复用准备包');
  fs.writeFileSync(path.join(dir,'reuse.json'),JSON.stringify({reused:true}));
  fs.writeFileSync(path.join(dir,'transaction.json'),JSON.stringify({root:updater.installRoot}));
  await service.quitAndInstall();
 }catch(error){fs.writeFileSync(path.join(dir,'rejected.json'),JSON.stringify({error:error.message}));}
}).catch(error=>{fs.writeFileSync(path.join(dir,'fixture-error.json'),JSON.stringify({error:error.stack}));app.quit()});
`;

function makeApp(target, version) {
  run('/usr/bin/ditto', [join(web, 'node_modules/electron/dist/Electron.app'), target]);
  const plist = join(target, 'Contents/Info.plist');
  renameSync(join(target, 'Contents/MacOS/Electron'), join(target, 'Contents/MacOS/Portunus'));
  for (const [key, value] of Object.entries({ CFBundleIdentifier: 'com.portunus.app', CFBundleShortVersionString: version, CFBundleVersion: version, CFBundleExecutable: 'Portunus' })) {
    run('/usr/libexec/PlistBuddy', ['-c', `Set :${key} ${value}`, plist]);
  }
  const resources = join(target, 'Contents/Resources');
  mkdirSync(join(resources, 'app')); mkdirSync(join(resources, 'bin'));
  writeFileSync(join(resources, 'app/package.json'), JSON.stringify({ name: 'portunus-update-smoke', version, main: 'main.cjs' }));
  writeFileSync(join(resources, 'app/main.cjs'), fixtureMain);
  writeFileSync(join(resources, 'app/preload.cjs'), `const {contextBridge,ipcRenderer}=require('electron');contextBridge.exposeInMainWorld('fixture',{confirm:process.env.PORTUNUS_SMOKE_SCENARIO!=='rollback',ready:()=>ipcRenderer.invoke('update:renderer-ready')});`);
  writeFileSync(join(resources, 'app/index.html'), '<!doctype html><h1>Portunus update smoke</h1><script>if(window.fixture.confirm)window.fixture.ready()</script>');
  writeFileSync(join(resources, 'app-update.yml'), 'updaterCacheDirName: portunus-smoke\n');
  for (const name of ['portunus', 'portunus-updater']) run('/bin/cp', [join(web, 'bin/mac', name), join(resources, 'bin', name)]);
  run('/usr/bin/codesign', ['--force', '--deep', '--sign', '-', '--timestamp=none', target]);
}
const template = join(root, 'template/Portunus.app'); mkdirSync(join(root, 'template'));
makeApp(template, '0.6.0');
const zipName = `Portunus-0.6.0-mac-${process.arch === 'arm64' ? 'arm64' : 'x64'}.zip`;
const archive = join(root, zipName);
run('/usr/bin/ditto', ['-c', '-k', '--keepParent', '--norsrc', template, archive]);
const info = await signUpdate(archive, '0.6.0', process.arch === 'arm64' ? 'arm64' : 'x64', privatePEM, publicPEM);
writeFileSync(`${archive}.sig`, JSON.stringify(info));
const results = [];
try {
  for (const scenario of scenarios.length ? scenarios : ['success', 'tamper', 'rollback']) {
    console.log(`验证 ${scenario}；目录 ${root}`);
    const dir = join(root, scenario); mkdirSync(dir);
    const target = join(dir, 'Portunus.app'); makeApp(target, '0.5.0');
    // 显式模拟首次安装已放行；后续新版的放行由 helper 完成。
    run('/usr/bin/xattr', ['-w', 'com.apple.quarantine', '0083;00000000;PortunusSmoke;', target]);
    run('/usr/bin/xattr', ['-dr', 'com.apple.quarantine', target]);
    writeFileSync(join(dir, 'user-data-sentinel'), 'must remain unchanged');
    let served = archive;
    if (scenario === 'tamper') {
      served = join(dir, zipName); const bytes = readFileSync(archive); bytes[100] ^= 1; writeFileSync(served, bytes);
    }
    const digest = createHash('sha512').update(readFileSync(served)).digest('base64');
    const manifest = join(dir, 'latest-mac.yml');
    writeFileSync(manifest, JSON.stringify({ version: '0.6.0', files: [{ url: zipName, sha512: digest, size: statSync(served).size }], path: zipName, sha512: digest, releaseDate: new Date().toISOString() }));
    routes.set(`/${scenario}/latest-mac.yml`, manifest);
    routes.set(`/${scenario}/${zipName}`, served);
    routes.set(`/${scenario}/${zipName}.sig`, `${archive}.sig`);
    const child = spawn(join(target, 'Contents/MacOS/Portunus'), [], { detached: true, stdio: ['ignore', 'pipe', 'pipe'], env: {
      ...process.env, NODE_EXTRA_CA_CERTS: join(root, 'tls.crt'), PORTUNUS_SMOKE_CASE: dir,
      PORTUNUS_SMOKE_SCENARIO: scenario, PORTUNUS_SMOKE_PORT: String(await freePort()), PORTUNUS_SMOKE_URL: `${base}/${scenario}/`,
    } });
    const log = [];
    child.stdout.on('data', (x) => log.push(x.toString())); child.stderr.on('data', (x) => log.push(x.toString()));
    try {
      await waitFor(() => {
        if (existsSync(join(dir, 'fixture-error.json'))) throw new Error(readFileSync(join(dir, 'fixture-error.json'), 'utf8'));
        if (scenario !== 'tamper' && existsSync(join(dir, 'rejected.json'))) throw new Error(readFileSync(join(dir, 'rejected.json'), 'utf8'));
        return existsSync(join(dir, scenario === 'tamper' ? 'rejected.json' : 'transaction.json'));
      }, 90000);
      if (scenario === 'tamper') {
        const rejected = JSON.parse(readFileSync(join(dir, 'rejected.json')));
        if (!rejected.error.includes('校验失败')) throw new Error(`unexpected rejection: ${rejected.error}`);
        if (child.exitCode !== null) throw new Error('校验失败却退出旧应用');
        results.push({ scenario, status: 'rejected-before-quit', error: rejected.error });
      } else {
        const transaction = JSON.parse(readFileSync(join(dir, 'transaction.json'))).root;
        const result = await waitFor(() => existsSync(join(transaction, 'result.json')) && JSON.parse(readFileSync(join(transaction, 'result.json'))), 125000);
        if ((scenario === 'success' && result.status !== 'installed') || (scenario === 'rollback' && !result.error?.includes('已恢复旧版'))) throw new Error(JSON.stringify(result));
        const version = spawnSync('/usr/libexec/PlistBuddy', ['-c', 'Print :CFBundleShortVersionString', join(target, 'Contents/Info.plist')], { encoding: 'utf8' }).stdout.trim();
        if (version !== (scenario === 'success' ? '0.6.0' : '0.5.0')) throw new Error('安装版本不符');
        const attrs = spawnSync('/usr/bin/xattr', ['-lr', target], { encoding: 'utf8' }).stdout;
        if (attrs.includes('com.apple.quarantine')) throw new Error('新版仍带隔离标记');
        results.push({ scenario, ...result, version, transaction });
      }
      if (readFileSync(join(dir, 'user-data-sentinel'), 'utf8') !== 'must remain unchanged') throw new Error('用户数据改变');
    } finally {
      writeFileSync(join(dir, 'fixture.log'), log.join(''));
      const pids = new Set([child.pid]);
      for (const version of ['0.5.0', '0.6.0']) {
        const file = join(dir, `started-${version}.json`); if (existsSync(file)) pids.add(JSON.parse(readFileSync(file)).pid);
      }
      for (const pid of pids) { try { process.kill(-pid, 'SIGTERM'); } catch {} }
      await delay(1000);
    }
  }
  writeFileSync(join(root, 'verification.json'), JSON.stringify({ results, requests }, null, 2));
  console.log(JSON.stringify({ root, results, requests }, null, 2));
} finally { server.close(); }
