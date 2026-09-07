/**
 * 弹窗空间连续性：让弹窗"从触发它的按钮方向"缩放展开。
 * - click 捕获阶段记录最近触发元素的位置（键盘 Enter 激活同样覆盖，键盘无坐标但元素 rect 可用）
 * - MutationObserver 捕获 HeroUI 弹窗容器挂载，把 transform-origin 设为
 *   触发点相对弹窗的百分比坐标；回调在微任务阶段完成，早于首帧绘制，
 *   动画从第一帧起就用新 origin，不会中途跳变
 * - 零侵入：各 Modal 组件无需任何改动
 */
let lastTriggerRect = null;

const clamp = (v, min, max) => Math.min(max, Math.max(min, v));

function applyOrigin(container) {
  if (!lastTriggerRect) {
    container.style.transformOrigin = '';
    return;
  }
  const rect = container.getBoundingClientRect();
  if (rect.width === 0 || rect.height === 0) return;
  const x = ((lastTriggerRect.left + lastTriggerRect.width / 2 - rect.left) / rect.width) * 100;
  const y = ((lastTriggerRect.top + lastTriggerRect.height / 2 - rect.top) / rect.height) * 100;
  // 适度夹住极端偏移，触发点过远时轨迹不至于怪异
  container.style.transformOrigin = `${clamp(x, -30, 130).toFixed(1)}% ${clamp(y, -60, 160).toFixed(1)}%`;
}

export function initModalOrigin() {
  document.addEventListener(
    'click',
    (e) => {
      const trigger = e.target.closest?.('button, [role="button"], a, label');
      lastTriggerRect = trigger ? trigger.getBoundingClientRect() : null;
    },
    true,
  );

  const observer = new MutationObserver(() => {
    const container = document.querySelector('.modal__container');
    if (container) applyOrigin(container);
  });
  observer.observe(document.body, { childList: true, subtree: true });
}
