#!/usr/bin/env python3
"""生成 Portunus 桌面端应用图标（方形底板 + 居中 logo）。

输入  build/logo.png        —— 裸 logo 源图（1024×1024 RGBA，透明背景）
输出  build/icon.png        —— 1024 母版（mac 风格：824 圆角方板 + 柔投影）
      build/icons/*.png     —— 各尺寸 PNG（16~1024，供 linux / 开发模式用）
      build/icons/icon.icns —— macOS（iconutil 合成）
      build/icons/icon.ico  —— Windows（≤64px 用 BMP 条目，128/256 内嵌 PNG）

设计规范（Apple HIG Big Sur 网格）：1024 画布、824×824 圆角方板、圆角 185、
底板用 UI 深色主题同源色（oklch hue 265 暗蓝灰渐变），螃蟹约 72% 板宽居中。
Windows 版底板放大到 900 减小透明边距，避免任务栏小尺寸下显得过小。

仅依赖 Python 标准库 + macOS 自带 sips / iconutil。logo 更新后重跑本脚本即可。
"""

import io
import math
import struct
import subprocess
import sys
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent   # web/
BUILD = ROOT / 'build'
SRC = BUILD / 'logo.png'                        # 裸 logo 源（保持不被底板覆盖）

CANVAS = 1024
# mac / win 两版底板参数：(板边长, 圆角, 阴影像素, 螃蟹占板宽比例)
VARIANTS = {
    'mac': dict(tile=824, radius=185, shadow_blur=36, shadow_dy=26, crab_ratio=0.72),
    'win': dict(tile=900, radius=200, shadow_blur=24, shadow_dy=16, crab_ratio=0.70),
}

# ── UI 深色主题同源色（见 src/renderer/index.css html.dark 块）──────────
def oklch(l, c, h):
    """oklch → sRGB (0~255 三元组)，标准 Oklab 矩阵换算。"""
    hr = h * math.pi / 180.0
    a, b = c * math.cos(hr), c * math.sin(hr)
    l_ = l + 0.3963377774 * a + 0.2158037573 * b
    m_ = l - 0.1055613458 * a - 0.0638541728 * b
    s_ = l - 0.0894841775 * a - 1.2914855480 * b
    l_, m_, s_ = l_ ** 3, m_ ** 3, s_ ** 3
    r = +4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_
    g = -1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_
    bb = -0.0041960863 * l_ - 0.7034186147 * m_ + 1.7076147010 * s_
    def gamma(u):
        u = min(1.0, max(0.0, u))
        return 12.92 * u if u <= 0.0031308 else 1.055 * u ** (1 / 2.4) - 0.055
    return tuple(round(gamma(v) * 255) for v in (r, g, bb))

BG_TOP = oklch(0.285, 0.028, 265)    # 底板渐变亮端（左上）
BG_BOT = oklch(0.150, 0.020, 265)    # 底板渐变暗端（右下）
EDGE   = oklch(0.620, 0.020, 265)    # 底板描边（半透明）
GLOW   = oklch(0.740, 0.150, 48)     # 中心氛围光（品牌橙，极低强度）


# ── PNG 编解码（标准库实现，只支持 8bit RGBA 非隔行）──────────────────
def png_decode(path):
    data = Path(path).read_bytes()
    assert data[:8] == b'\x89PNG\r\n\x1a\n', '不是 PNG 文件'
    pos, idat, meta = 8, b'', {}
    while pos < len(data):
        ln, typ = struct.unpack('>I4s', data[pos:pos + 8])
        pos += 8
        chunk = data[pos:pos + ln]
        pos += ln + 4
        if typ == b'IHDR':
            w, h, bd, ct, _c, _f, inter = struct.unpack('>IIBBBBB', chunk)
            assert (bd, ct, inter) == (8, 6, 0), f'不支持的 PNG 格式: bd={bd} ct={ct}'
            meta = (w, h)
        elif typ == b'IDAT':
            idat += chunk
        elif typ == b'IEND':
            break
    w, h = meta
    raw, stride = zlib.decompress(idat), w * 4
    px, prev, p = bytearray(h * stride), bytearray(stride), 0
    for y in range(h):
        f = raw[p]; p += 1
        line = bytearray(raw[p:p + stride]); p += stride
        if f == 1:
            for i in range(4, stride):
                line[i] = (line[i] + line[i - 4]) & 255
        elif f == 2:
            for i in range(stride):
                line[i] = (line[i] + prev[i]) & 255
        elif f == 3:
            for i in range(stride):
                a = line[i - 4] if i >= 4 else 0
                line[i] = (line[i] + ((a + prev[i]) >> 1)) & 255
        elif f == 4:
            for i in range(stride):
                a = line[i - 4] if i >= 4 else 0
                b, c = prev[i], prev[i - 4] if i >= 4 else 0
                pp = a + b - c
                pa, pb, pc = abs(pp - a), abs(pp - b), abs(pp - c)
                pr = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[i] = (line[i] + pr) & 255
        px[y * stride:(y + 1) * stride] = line
        prev = line
    return w, h, px


def png_encode(w, h, px):
    raw = bytearray()
    stride = w * 4
    for y in range(h):
        raw.append(0)  # 每行 filter type 0
        raw += px[y * stride:(y + 1) * stride]
    comp = zlib.compress(bytes(raw), 9)
    def chunk(typ, payload):
        c = struct.pack('>I', len(payload)) + typ + payload
        return c + struct.pack('>I', zlib.crc32(typ + payload) & 0xFFFFFFFF)
    out = b'\x89PNG\r\n\x1a\n'
    out += chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 6, 0, 0, 0))
    out += chunk(b'IDAT', comp)
    out += chunk(b'IEND', b'')
    return bytes(out)


# ── 像素级合成 ────────────────────────────────────────────────────────
def roundrect_sdf(x, y, half, r):
    """圆角方形 SDF：>0 在外部，<0 在内部。x/y 相对板中心。"""
    dx, dy = abs(x) - (half - r), abs(y) - (half - r)
    ox, oy = max(dx, 0.0), max(dy, 0.0)
    inner = min(max(dx, dy), 0.0)
    return (ox * ox + oy * oy) ** 0.5 + inner - r


def clean_specks(w, h, px):
    """清掉 logo 里的纯红杂点（255,0,0 系）：r 很高且 g/b 极低、又不是蟹壳配色。"""
    n = 0
    for i in range(0, w * h * 4, 4):
        r, g, b = px[i], px[i + 1], px[i + 2]
        if r > 230 and g < 45 and b < 45 and px[i + 3] > 0:
            px[i + 3] = 0
            n += 1
    return n


def crab_bbox(w, h, px):
    """透明背景 logo 的内容包围盒。"""
    x0, y0, x1, y1 = w, h, -1, -1
    for y in range(h):
        row = y * w * 4
        for x in range(w):
            if px[row + x * 4 + 3] > 8:
                if x < x0: x0 = x
                if x > x1: x1 = x
                if y < y0: y0 = y
                if y > y1: y1 = y
    return x0, y0, x1, y1


def scale_crab(w, h, px, tw, th):
    """面积平均缩放（抗锯齿优于双线性），返回 (tw, th, 新像素)。"""
    out = bytearray(tw * th * 4)
    sx, sy = w / tw, h / th
    for dy in range(th):
        fy0, fy1 = dy * sy, (dy + 1) * sy
        for dx in range(tw):
            fx0, fx1 = dx * sx, (dx + 1) * sx
            r = g = b = a = 0.0
            n = 0.0
            iy = int(fy0)
            while iy < fy1:
                cover_y = min(fy1, iy + 1) - fy0
                row = iy * w * 4
                ix = int(fx0)
                while ix < fx1:
                    cover = cover_y * (min(fx1, ix + 1) - fx0)
                    i = row + ix * 4
                    pa = px[i + 3]
                    if pa:
                        wa = cover * pa
                        r += px[i] * wa; g += px[i + 1] * wa; b += px[i + 2] * wa
                        a += wa
                    n += cover
                    ix += 1
                iy += 1
            o = (dy * tw + dx) * 4
            if a > 0:
                out[o] = min(255, round(r / a))
                out[o + 1] = min(255, round(g / a))
                out[o + 2] = min(255, round(b / a))
                out[o + 3] = min(255, round(a / n))
    return tw, th, out


def blur_small(w, h, px, radius):
    """近似高斯：3 次盒模糊（小图上做，够投影用）。px 为单通道浮点。"""
    for _ in range(3):
        for orient in (True, False):
            n, m = (h, w) if orient else (w, h)
            for k in range(n):
                line = [px[(k * w + j) if orient else (j * w + k)] for j in range(m)]
                acc, out = 0.0, [0.0] * m
                for j in range(m):
                    acc += line[j]
                    if j >= 2 * radius + 1:
                        acc -= line[j - 2 * radius - 1]
                    out[max(0, j - radius)] = acc / min(m, j + 1, 2 * radius + 1) \
                        if j < 2 * radius + 1 else acc / (2 * radius + 1)
                for j in range(m):
                    if orient:
                        px[k * w + j] = out[j]
                    else:
                        px[j * w + k] = out[j]


def compose(crab, v):
    """合成一版 1024 母版。crab = (w,h,px)。"""
    tile, radius = v['tile'], v['radius']
    canvas = bytearray(CANVAS * CANVAS * 4)
    half, cx, cy = tile / 2, CANVAS / 2, CANVAS / 2
    maxr = tile * 0.62           # 氛围光有效半径

    # 1) 底板：对角渐变 + 中心微光 + 抗锯齿边缘
    for y in range(CANVAS):
        for x in range(CANVAS):
            d = roundrect_sdf(x + 0.5 - cx, y + 0.5 - cy, half, radius)
            if d >= 0.5:
                continue
            t = ((x + y) / (2 * CANVAS)) ** 1.0
            r = BG_TOP[0] + (BG_BOT[0] - BG_TOP[0]) * t
            g = BG_TOP[1] + (BG_BOT[1] - BG_TOP[1]) * t
            b = BG_TOP[2] + (BG_BOT[2] - BG_TOP[2]) * t
            # 中心氛围光：品牌橙，仅提亮，不抢戏
            dist = ((x + 0.5 - cx) ** 2 + (y + 0.5 - cy) ** 2) ** 0.5
            if dist < maxr:
                k = 0.055 * (1 - dist / maxr)
                r += (GLOW[0] - r) * k; g += (GLOW[1] - g) * k; b += (GLOW[2] - b) * k
            # 顶部内侧高光（Apple 质感）
            hl = max(0.0, 1.0 - (y - (cy - half)) / (tile * 0.28))
            if hl > 0:
                k = 0.10 * hl
                r += (255 - r) * k; g += (255 - g) * k; b += (255 - b) * k
            # 半透明描边：贴边 2px
            cov = 1.0 if d <= -0.5 else 0.5 - d
            if d > -2.0:
                k = 0.38 * cov
                r += (EDGE[0] - r) * k; g += (EDGE[1] - g) * k; b += (EDGE[2] - b) * k
            o = (y * CANVAS + x) * 4
            canvas[o], canvas[o + 1], canvas[o + 2] = round(r), round(g), round(b)
            canvas[o + 3] = round(255 * cov)

    # 2) 底板投影（降采样 → 盒模糊 → 贴回）
    small = CANVAS // 4
    sa = [0.0] * (small * small)
    for y in range(small):
        for x in range(small):
            sa[y * small + x] = canvas[(y * 4 * CANVAS + x * 4) * 4 + 3] / 255.0
    blur_small(small, small, sa, max(1, v['shadow_blur'] // 4))
    dy = v['shadow_dy']
    for y in range(CANVAS):
        ys = min(small - 1, max(0, (y - dy) // 4))
        for x in range(CANVAS):
            sh = sa[ys * small + min(small - 1, x // 4)] * 0.32
            if sh <= 0:
                continue
            o = (y * CANVAS + x) * 4
            if canvas[o + 3] >= 255:
                continue
            a = sh * (1 - canvas[o + 3] / 255.0)
            canvas[o] = round(canvas[o] * (1 - a))
            canvas[o + 1] = round(canvas[o + 1] * (1 - a))
            canvas[o + 2] = round(canvas[o + 2] * (1 - a))
            canvas[o + 3] = round(canvas[o + 3] + 255 * a)

    # 3) 螃蟹：按板宽比例缩放、内容包围盒居中（光学上略微下移）
    cw, ch, cpx = crab
    bb_x0, bb_y0, bb_x1, bb_y1 = crab_bbox(cw, ch, cpx)
    target = round(tile * v['crab_ratio'])
    scale = target / (bb_x1 - bb_x0 + 1)
    tw, th = round(cw * scale), round(ch * scale)
    sw, sh2, spx = scale_crab(cw, ch, cpx, tw, th)
    ox = round(cx - (bb_x0 + bb_x1 + 1) * scale / 2)
    oy = round(cy - (bb_y0 + bb_y1 + 1) * scale / 2) + 6
    for y in range(max(0, oy), min(CANVAS, oy + sh2)):
        for x in range(max(0, ox), min(CANVAS, ox + sw)):
            sp, cp = (y * CANVAS + x) * 4, ((y - oy) * sw + (x - ox)) * 4
            sa_, ca = spx[cp + 3] / 255.0, canvas[sp + 3] / 255.0
            if sa_ <= 0:
                continue
            oa = sa_ + ca * (1 - sa_)
            for k in range(3):
                canvas[sp + k] = round((spx[cp + k] * sa_ + canvas[sp + k] * ca * (1 - sa_)) / oa)
            canvas[sp + 3] = round(oa * 255)
    return canvas


# ── 产物导出 ──────────────────────────────────────────────────────────
SIZES = [16, 24, 32, 48, 64, 128, 256, 512, 1024]

def write_png(path, w, h, px):
    Path(path).write_bytes(png_encode(w, h, px))

def sips_resize(src, dst, size):
    subprocess.run(['sips', '-z', str(size), str(size), str(src), '--out', str(dst)],
                   check=True, capture_output=True)

def make_icns(master):
    iconset = BUILD / 'AppIcon.iconset'
    iconset.mkdir(exist_ok=True)
    pairs = [(16, 'icon_16x16.png'), (32, 'icon_16x16@2x.png'), (32, 'icon_32x32.png'),
             (64, 'icon_32x32@2x.png'), (128, 'icon_128x128.png'), (256, 'icon_128x128@2x.png'),
             (256, 'icon_256x256.png'), (512, 'icon_256x256@2x.png'),
             (512, 'icon_512x512.png'), (1024, 'icon_512x512@2x.png')]
    tmp = BUILD / '.iconset-tmp.png'
    for size, name in pairs:
        write_png(tmp, CANVAS, CANVAS, master)
        sips_resize(tmp, iconset / name, size)
    tmp.unlink()
    subprocess.run(['iconutil', '-c', 'icns', str(iconset), '-o', str(BUILD / 'icons/icon.icns')],
                   check=True)
    import shutil
    shutil.rmtree(iconset)

def make_ico(master):
    """Windows .ico：≤64px 用 BMP(DIB) 条目，128/256 内嵌 PNG。"""
    entries, images = [], bytearray()
    header_count = 7
    dir_size = 6 + 16 * header_count
    for size in (16, 24, 32, 48, 64, 128, 256):
        tmp = BUILD / '.ico-tmp.png'
        write_png(tmp, CANVAS, CANVAS, master)
        sips_resize(tmp, tmp, size)
        w, h, px = png_decode(tmp)
        tmp.unlink()
        if size <= 64:
            xor = bytearray()
            for y in range(h - 1, -1, -1):        # DIB 自下而上，BGRA
                row = y * w * 4
                for x in range(w):
                    i = row + x * 4
                    xor += bytes((px[i + 2], px[i + 1], px[i], px[i + 3]))
            and_mask = bytes(((w + 31) // 32) * 4 * h)   # 全 0：透明度走 alpha 通道
            dib = struct.pack('<IiiHHIIiiII', 40, w, h * 2, 1, 32, 0,
                              len(xor) + len(and_mask), 0, 0, 0, 0) + bytes(xor) + and_mask
            data = bytes(dib)
        else:
            data = png_encode(w, h, px)           # Vista+ 支持 PNG 条目（≥128）
        entries.append((size % 256, len(data)))
        images += data
    ico = struct.pack('<HHH', 0, 1, header_count)
    offset = dir_size
    for w_, ln in entries:
        ico += struct.pack('<BBBBHHII', w_, w_, 0, 0, 1, 32, ln, offset)
        offset += ln
    (BUILD / 'icons/icon.ico').write_bytes(ico + bytes(images))


def main():
    w, h, px = png_decode(SRC)
    assert (w, h) == (CANVAS, CANVAS), f'logo 源图需 {CANVAS}×{CANVAS}'
    n = clean_specks(w, h, px)
    print(f'已清理纯红杂点像素: {n}')

    masters = {}
    for name, v in VARIANTS.items():
        masters[name] = compose((w, h, px), v)
        if name == 'mac':
            write_png(BUILD / 'icon.png', CANVAS, CANVAS, masters[name])

    icons = BUILD / 'icons'
    icons.mkdir(exist_ok=True)
    for size in SIZES:  # PNG 尺寸集（linux / 开发模式 Dock 用 mac 版）
        tmp = BUILD / '.size-tmp.png'
        write_png(tmp, CANVAS, CANVAS, masters['mac'])
        sips_resize(tmp, icons / f'{size}x{size}.png', size)
        tmp.unlink()

    make_icns(masters['mac'])
    make_ico(masters['win'])
    print('完成：build/icon.png + build/icons/{*.png,icon.icns,icon.ico}')


if __name__ == '__main__':
    sys.exit(main())
