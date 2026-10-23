#!/usr/bin/env python3
"""生成 macOS 菜单栏 Template 图标（圆角底板 + 透明镂空螃蟹）。

输入  build/logo.png        —— 裸 logo 源图（1024×1024 RGBA，透明背景）
输出  src/main/icons/trayTemplate.png     —— 18×18（@1x）
      src/main/icons/trayTemplate@2x.png  —— 36×36（@2x）

非透明像素一律纯黑，底板与镂空形状全由 alpha 表达；
文件名含 "Template" 时 macOS/Electron 自动按模板渲染（浅色菜单栏显黑、深色显白），
与 wifi / 电池等系统图标风格统一。仅依赖标准库，复用 make-icon.py 的 PNG 编解码。
logo 更新后重跑本脚本即可。
"""

import importlib.util
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
ROOT = SCRIPTS.parent                      # web/
SRC = ROOT / 'build' / 'logo.png'
OUT = ROOT / 'src' / 'main' / 'icons'

SPEC = importlib.util.spec_from_file_location('make_icon', SCRIPTS / 'make-icon.py')
make_icon = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(make_icon)

# 裁剪阈值：低于此 alpha 的像素视为透明雾，不进包围盒（源图四周有 alpha≈1 的雾）
CROP_THRESHOLD = 10


def blacken():
    """裁剪到实体螃蟹，保留 alpha；按 alpha 阈值裁剪，
    滤掉 logo 源图四周那圈近透明雾（alpha≈1 的像素把包围盒撑大），
    让螃蟹按实际轮廓缩放，而不受源图透明边缘影响。"""
    w, h, px = make_icon.png_decode(SRC)
    assert (w, h) == (1024, 1024), f'logo 源图应为 1024×1024，实际 {w}×{h}'
    minx, miny, maxx, maxy = w, h, -1, -1
    for y in range(h):
        row = y * w * 4
        for x in range(w):
            if px[row + x * 4 + 3] >= CROP_THRESHOLD:
                if x < minx: minx = x
                if x > maxx: maxx = x
                if y < miny: miny = y
                if y > maxy: maxy = y
    assert maxx >= 0, f'logo 源图无 alpha>={CROP_THRESHOLD} 的内容'
    tw, th = maxx - minx + 1, maxy - miny + 1
    out = bytearray(tw * th * 4)
    for y in range(th):
        src = ((miny + y) * w + minx) * 4
        out[y * tw * 4:(y + 1) * tw * 4] = px[src:src + tw * 4]
    return tw, th, out


def resize_alpha(w, h, px, tw, th):
    """任意比例盒式重采样：颜色恒黑，仅按面积平均 alpha（保留形状灰度层次）。"""
    out = bytearray(tw * th * 4)
    for ty in range(th):
        sy0, sy1 = ty * h / th, (ty + 1) * h / th
        for tx in range(tw):
            sx0, sx1 = tx * w / tw, (tx + 1) * w / tw
            a_sum, n = 0, 0
            for sy in range(int(sy0), int(sy1) + 1):
                if sy >= h: continue
                fy0, fy1 = max(sy0, sy), min(sy1, sy + 1)
                fy = fy1 - fy0
                if fy <= 0: continue
                row = sy * w * 4
                for sx in range(int(sx0), int(sx1) + 1):
                    if sx >= w: continue
                    fx0, fx1 = max(sx0, sx), min(sx1, sx + 1)
                    fx = fx1 - fx0
                    if fx <= 0: continue
                    a_sum += px[row + sx * 4 + 3] * fx * fy
                    n += fx * fy
            o = (ty * tw + tx) * 4
            out[o] = out[o + 1] = out[o + 2] = 0
            out[o + 3] = round(a_sum / n)
    return out


def template_canvas(w, h, px):
    """在统一的 8 倍母版上生成底板并扣除螃蟹，供两种屏幕倍率缩小。

    18pt 画布、16pt 底板、4pt 圆角、13pt 螃蟹。内容尺寸取偶数，
    保证上下留白严格相等，避免整数除法将多出的像素留在底部。
    """
    canvas, inset, radius, crab_width = 144, 8, 32, 104
    scale = crab_width / max(w, h)
    tw, th = max(2, round(w * scale / 2) * 2), max(2, round(h * scale / 2) * 2)
    # 去掉近透明雾，并将接近不透明的主体变为完全镂空；保留边缘抗锯齿。
    px = bytearray(px)
    for i in range(3, len(px), 4):
        if px[i] < CROP_THRESHOLD:
            px[i] = 0
        elif px[i] >= 240:
            px[i] = 255
    img = resize_alpha(w, h, px, tw, th)
    out = bytearray(canvas * canvas * 4)
    ox, oy = (canvas - tw) // 2, (canvas - th) // 2
    for y in range(inset, canvas - inset):
        for x in range(inset, canvas - inset):
            # 像素中心到最近圆角圆心的距离；缩小时统一抗锯齿。
            dx = max(inset + radius - (x + 0.5), 0, x + 0.5 - (canvas - inset - radius))
            dy = max(inset + radius - (y + 0.5), 0, y + 0.5 - (canvas - inset - radius))
            if dx * dx + dy * dy <= radius * radius:
                out[(y * canvas + x) * 4 + 3] = 255
    for y in range(th):
        for x in range(tw):
            d = ((oy + y) * canvas + ox + x) * 4 + 3
            out[d] = round(out[d] * (255 - img[(y * tw + x) * 4 + 3]) / 255)
    return canvas, canvas, out


def main():
    w, h, px = blacken()
    OUT.mkdir(parents=True, exist_ok=True)
    cw, ch, cpx = template_canvas(w, h, px)
    for size, suffix in ((18, ''), (36, '@2x')):
        pixels = resize_alpha(cw, ch, cpx, size, size)
        (OUT / f'trayTemplate{suffix}.png').write_bytes(make_icon.png_encode(size, size, pixels))
    print(f'[make-tray-icon] 已生成 {OUT}/trayTemplate.png (18) 与 trayTemplate@2x.png (36)，'
          f'圆角底板与镂空螃蟹已居中（裁剪后源 {w}×{h}）')


if __name__ == '__main__':
    main()
