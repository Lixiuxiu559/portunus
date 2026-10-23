export const PNG_PREFIX = 'data:image/png;base64,';
export const MAX_ICON_LENGTH = 64 * 1024;
export const MAX_UPLOAD_BYTES = 2 * 1024 * 1024;
export const ICON_ACCEPT = 'image/png,image/jpeg,image/webp,image/svg+xml';

// 只允许本地重新编码的 PNG；渲染时也检查头部，兼容直接导入的历史数据。
export function isCustomIcon(value) {
  if (typeof value !== 'string' || !value.startsWith(PNG_PREFIX) || value.length > MAX_ICON_LENGTH) return false;
  try {
    const header = atob(value.slice(PNG_PREFIX.length, PNG_PREFIX.length + 32));
    if (header.length !== 24 || !header.startsWith('\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR')) return false;
    const bytes = Uint8Array.from(header, (char) => char.charCodeAt(0));
    const view = new DataView(bytes.buffer);
    const width = view.getUint32(16), height = view.getUint32(20);
    return width > 0 && height > 0 && width <= 256 && height <= 256;
  } catch { return false; }
}

export function resolveIcon({ icon, providerId, type }, knownIcons) {
  if (isCustomIcon(icon)) return { kind: 'custom', value: icon };
  if (knownIcons.includes(icon)) return { kind: 'builtin', value: icon };
  if (knownIcons.includes(providerId)) return { kind: 'builtin', value: providerId };
  return { kind: 'protocol', value: type };
}

export function filterIcons(icons, query) {
  const search = query.trim().toLowerCase();
  return icons.filter((entry) => `${entry.id} ${entry.name} ${entry.keywords}`.toLowerCase().includes(search));
}

export function validateUpload(file) {
  const allowed = ICON_ACCEPT.split(',');
  const extensionTypes = { png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', webp: 'image/webp', svg: 'image/svg+xml' };
  const mime = file.type || extensionTypes[file.name.split('.').pop()?.toLowerCase()];
  if (!allowed.includes(mime)) throw new Error('请选择 PNG、JPEG、WebP 或 SVG 图片');
  if (file.size > MAX_UPLOAD_BYTES) throw new Error('图片不能超过 2 MB');
  return mime;
}

function readImage(file, mime) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error('无法读取图片，请重新选择'));
    reader.onload = () => {
      const img = new Image();
      const timer = setTimeout(() => { img.src = ''; reject(new Error('图片解码超时，请重新选择')); }, 10000);
      img.onload = () => { clearTimeout(timer); resolve(img); };
      img.onerror = () => { clearTimeout(timer); reject(new Error('图片无法解码，请选择其他图片')); };
      img.src = `data:${mime};base64,${String(reader.result).split(',')[1]}`;
    };
    reader.readAsDataURL(file);
  });
}

// 原始 SVG 只经图片解码，不注入页面；最终保存固定 96×96 PNG。
export async function normalizeIconFile(file) {
  const img = await readImage(file, validateUpload(file));
  const width = img.naturalWidth, height = img.naturalHeight;
  if (!width || !height || width > 4096 || height > 4096) throw new Error('图片尺寸需在 1 至 4096 像素之间');
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 96;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('当前环境无法处理图片');
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = 'high';
  try {
    // 近正方形且四角不透明的图片铺满；透明 Logo 缩小居中，保持视觉留白。
    ctx.drawImage(img, 0, 0, 96, 96);
    const tile = width / height >= 0.9 && width / height <= 1.1
      && [[1, 1], [94, 1], [1, 94], [94, 94]].every(([x, y]) => ctx.getImageData(x, y, 1, 1).data[3] >= 250);
    ctx.clearRect(0, 0, 96, 96);
    const scale = (tile ? 96 : 66) / Math.max(width, height);
    const w = width * scale, h = height * scale;
    ctx.drawImage(img, (96 - w) / 2, (96 - h) / 2, w, h);
    const data = canvas.toDataURL('image/png');
    if (!isCustomIcon(data)) throw new Error('图片转换失败');
    return data;
  } catch { throw new Error('图片转换失败，请选择其他图片'); }
}
