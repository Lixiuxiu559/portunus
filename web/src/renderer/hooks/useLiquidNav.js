// 顶部导航液态玻璃胶囊的动效增强（index.css 中 nav-tabs 块）：
// 位置流动由 RAC SharedElement 快照 + CSS transition 完成，这里补两件
// CSS 过渡做不了的事——距离感知的拉伸/回弹（scale），与点击落点的液态涟漪。
import { useEffect, useLayoutEffect, useRef } from 'react';

// 与 index.css 中 indicator 的 transition 时长保持一致
const LIQUID_MS = 620;

export default function useLiquidNav(currentTab) {
  const listRef = useRef(null);
  const prevCenter = useRef(null);

  // 切页后测量新旧 tab 中心距，在新胶囊上播放拉伸动画。
  // tab 本身无过渡，rect 即最终落位；子组件 layoutEffect 先于本 effect 执行，
  // 此时 SharedElement 已设好起始 translate，不影响 tab 测量。
  useLayoutEffect(() => {
    const list = listRef.current;
    const tab = list?.querySelector('.tabs__tab[data-selected="true"]');
    if (!tab) return;
    const rect = tab.getBoundingClientRect();
    const center = rect.left + rect.width / 2;
    const prev = prevCenter.current;
    prevCenter.current = center;
    if (prev === null || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;

    const dist = Math.abs(center - prev);
    if (dist < 2) return;
    requestAnimationFrame(() => {
      const pill = list.querySelector('.tabs__tab[data-selected="true"] .tabs__indicator');
      if (!pill) return;
      // 相邻切换也有基础拉伸，越远拉得越长，封顶克制
      const stretch = Math.min(1.12 + (dist / 480) * 0.16, 1.3);
      const squash = 1 - (stretch - 1) * 0.55;
      const computed = getComputedStyle(pill).scale;
      pill.animate(
        [
          { scale: !computed || computed === 'none' ? '1 1' : computed, easing: 'ease-out' },
          { scale: `${stretch.toFixed(3)} ${squash.toFixed(3)}`, offset: 0.42, easing: 'ease-in-out' },
          { scale: '0.992 1.006', offset: 0.78, easing: 'ease-out' },
          { scale: '1 1' },
        ],
        { duration: LIQUID_MS },
      );
    });
  }, [currentTab]);

  // 点击液态扩散：在指针落点生成一圈微光，动画结束自删。
  // 用原生监听——HeroUI 的 slot 结构里 React 冒泡不经过 Tabs.List，
  // 而 DOM 冒泡（事件挂 list、元素挂外层圆角容器借其裁剪）实测可靠。
  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const host = list.closest('.tabs__list-container') ?? list;
    const onDown = (e) => {
      if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return;
      const rect = host.getBoundingClientRect();
      const drop = document.createElement('span');
      drop.className = 'nav-liquid-ripple';
      drop.style.left = `${e.clientX - rect.left}px`;
      drop.style.top = `${e.clientY - rect.top}px`;
      drop.addEventListener('animationend', () => drop.remove());
      host.appendChild(drop);
    };
    list.addEventListener('pointerdown', onDown);
    return () => list.removeEventListener('pointerdown', onDown);
  }, []);

  return { listRef };
}
