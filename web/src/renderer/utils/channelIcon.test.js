import test from 'node:test';
import assert from 'node:assert/strict';
import { filterIcons, isCustomIcon, MAX_ICON_LENGTH, MAX_UPLOAD_BYTES, PNG_PREFIX, resolveIcon, validateUpload } from './channelIcon.js';

const known = ['openai', 'deepseek'];
function header(width, height) {
  const data = Buffer.alloc(24);
  data.write('\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR', 0, 'latin1');
  data.writeUInt32BE(width, 16); data.writeUInt32BE(height, 20);
  return PNG_PREFIX + data.toString('base64');
}

test('渠道品牌独立于协议，手动选择优先，清除后恢复厂商，历史渠道沿用协议', () => {
  assert.deepEqual(resolveIcon({ providerId: 'deepseek', type: 'openai' }, known), { kind: 'builtin', value: 'deepseek' });
  assert.deepEqual(resolveIcon({ providerId: 'deepseek', type: 'openai', icon: 'openai' }, known), { kind: 'builtin', value: 'openai' });
  assert.deepEqual(resolveIcon({ providerId: 'deepseek', type: 'openai', icon: '' }, known), { kind: 'builtin', value: 'deepseek' });
  assert.deepEqual(resolveIcon({ type: 'anthropic' }, known), { kind: 'protocol', value: 'anthropic' });
  assert.deepEqual(resolveIcon({ providerId: 'unknown', type: 'openai', icon: 'future-icon' }, known), { kind: 'protocol', value: 'openai' });
  assert.equal(resolveIcon({ providerId: 'deepseek', icon: header(96, 96) }, known).kind, 'custom');
});

test('渲染拒绝外链、伪造签名、非法编码、巨大尺寸和超长PNG，失败可兜底', () => {
  for (const icon of [undefined, '', 'https://example.test/icon.png', 'data:image/svg+xml;base64,abc',
    PNG_PREFIX + 'invalid!', PNG_PREFIX + Buffer.alloc(24).toString('base64'), header(0, 96), header(257, 96),
    header(96, 4096), header(96, 96) + 'a'.repeat(MAX_ICON_LENGTH)]) {
    assert.equal(isCustomIcon(icon), false);
  }
  assert.equal(isCustomIcon(header(256, 256)), true);
  assert.deepEqual(resolveIcon({ icon: header(9999, 1), providerId: 'deepseek' }, known), { kind: 'builtin', value: 'deepseek' });
});

test('搜索支持品牌、中文关键词、大小写与空白', () => {
  const entries = [{ id: 'deepseek', name: 'DeepSeek', keywords: '深度求索' }, { id: 'siliconflow', name: 'SiliconFlow', keywords: '硅基流动' }];
  assert.equal(filterIcons(entries, ' DEEPSEEK ')[0].id, 'deepseek');
  assert.equal(filterIcons(entries, '硅基')[0].id, 'siliconflow');
  assert.equal(filterIcons(entries, '').length, 2);
  assert.deepEqual(filterIcons(entries, 'no-match'), []);
});

test('上传允许常见图片及空MIME的SVG，拒绝不支持类型和超限文件', () => {
  for (const type of ['image/png', 'image/jpeg', 'image/webp', 'image/svg+xml']) {
    assert.equal(validateUpload({ type, name: 'icon', size: MAX_UPLOAD_BYTES }), type);
  }
  assert.equal(validateUpload({ type: '', name: 'ICON.SVG', size: 100 }), 'image/svg+xml');
  assert.throws(() => validateUpload({ type: 'text/html', name: 'fake.png', size: 10 }), /请选择/);
  assert.throws(() => validateUpload({ type: 'image/gif', name: 'icon.gif', size: 10 }), /请选择/);
  assert.throws(() => validateUpload({ type: 'image/png', name: 'icon.png', size: MAX_UPLOAD_BYTES + 1 }), /2 MB/);
});
