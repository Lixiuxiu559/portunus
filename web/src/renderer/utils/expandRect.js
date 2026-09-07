/**
 * 卡片展开编辑器的起点捕获：点编辑按钮时记录卡片当前的可视矩形与圆角，
 * 供 ExpandEditor 做 FLIP——面板从卡片原位同尺寸生长，保证空间连续性。
 */
export function captureCardRect(el) {
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width === 0 || r.height === 0) return null;
  const cs = getComputedStyle(el);
  return {
    left: r.left,
    top: r.top,
    width: r.width,
    height: r.height,
    borderRadius: cs.borderRadius,
  };
}
