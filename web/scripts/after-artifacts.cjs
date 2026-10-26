const fs = require('node:fs');
const path = require('node:path');

module.exports = async (context) => {
  const zips = context.artifactPaths.filter((file) => /-mac-(arm64|x64)\.zip$/.test(file));
  if (!zips.length) return [];
  if (process.env.CI && !process.env.PORTUNUS_UPDATE_SIGNING_KEY && !process.env.PORTUNUS_UPDATE_SIGNING_KEY_FILE) {
    throw new Error('macOS 更新包签名缺少 PORTUNUS_UPDATE_SIGNING_KEY Secret');
  }
  const { signUpdate } = await import('./sign-mac-update.mjs');
  const publicPEM = fs.readFileSync(path.resolve(__dirname, '../../cmd/portunus-updater/public-key.pem'));
  const key = process.env.PORTUNUS_UPDATE_SIGNING_KEY || fs.readFileSync(
    process.env.PORTUNUS_UPDATE_SIGNING_KEY_FILE || path.join(require('node:os').homedir(), '.config/portunus/update-signing-key.pem'), 'utf8');
  const signatures = [];
  for (const zip of zips) {
    const match = path.basename(zip).match(/^Portunus-(\d+\.\d+\.\d+)-mac-(arm64|x64)\.zip$/);
    if (!match) throw new Error('macOS 更新包文件名无效');
    const [, version, arch] = match;
    const info = await signUpdate(zip, version, arch, key, publicPEM);
    const file = `${zip}.sig`;
    fs.writeFileSync(file, `${JSON.stringify(info, null, 2)}\n`);
    signatures.push(file);
  }
  return signatures;
};
