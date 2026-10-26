// 独立于 Apple Developer ID 的 Ed25519 更新签名。私钥不进入仓库或安装包。
import { createHash, createPrivateKey, createPublicKey, sign } from 'node:crypto';
import { createReadStream, readFileSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const repo = resolve(import.meta.dirname, '../..');
export async function signUpdate(archive, version, arch, privatePEM, publicPEM) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version) || !['arm64', 'x64'].includes(arch)) throw new Error('正式版本或架构无效');
  const key = createPrivateKey(privatePEM);
  if (key.asymmetricKeyType !== 'ed25519' || !createPublicKey(key).equals(createPublicKey(publicPEM))) throw new Error('签名私钥与应用内置公钥不匹配');
  const hash = createHash('sha512');
  for await (const chunk of createReadStream(archive)) hash.update(chunk);
  const info = { version, arch, size: statSync(archive).size, sha512: hash.digest('base64') };
  const payload = `portunus-mac-update-v1\ncom.portunus.app\n${version}\n${arch}\n${info.size}\n${info.sha512}\n`;
  return { ...info, signature: sign(null, Buffer.from(payload), key).toString('base64') };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [archive, version, arch = 'arm64'] = process.argv.slice(2);
  if (!archive || !version) throw new Error('用法: node scripts/sign-mac-update.mjs <ZIP路径> <版本> [arm64|x64]');
  const key = process.env.PORTUNUS_UPDATE_SIGNING_KEY || readFileSync(
    process.env.PORTUNUS_UPDATE_SIGNING_KEY_FILE || join(process.env.HOME, '.config/portunus/update-signing-key.pem'), 'utf8');
  const pub = readFileSync(join(repo, 'cmd/portunus-updater/public-key.pem'));
  const info = await signUpdate(archive, version, arch, key, pub);
  writeFileSync(`${archive}.sig`, `${JSON.stringify(info, null, 2)}\n`);
  console.log(`已签名: ${archive}.sig`);
}
