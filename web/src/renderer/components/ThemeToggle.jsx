import { flushSync } from 'react-dom';
import { useTheme } from '@heroui/react';

/**
 * 主题切换开关：浅色（太阳）↔ 深色（月亮），复刻 uiverse "old-falcon" 的滑动动画。
 * - resolvedTheme 会解析 system（跟随系统）为实际的 light / dark
 * - 手动切换后即设为明确的 light / dark
 * - 浏览器支持 View Transitions 时，新主题从点击位置圆形扩散铺满页面；
 *   减弱动态（prefers-reduced-motion）时不播扩散动画
 */
export default function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme('system');
  const isDark = resolvedTheme === 'dark';

  const handleChange = (e) => {
    const next = e.target.checked ? 'dark' : 'light';

    // 减弱动态或环境不支持 View Transitions 时直接切换，不做扩散
    if (
      window.matchMedia('(prefers-reduced-motion: reduce)').matches ||
      typeof document.startViewTransition !== 'function'
    ) {
      setTheme(next);
      return;
    }

    // 点击 label（可见开关图形）转发给隐藏 input 时事件可能无坐标，
    // undefined/NaN/0 一律回退到开关中心
    const rect = e.currentTarget.getBoundingClientRect();
    const hasPoint =
      Number.isFinite(e.clientX) &&
      Number.isFinite(e.clientY) &&
      (e.clientX !== 0 || e.clientY !== 0);
    const x = hasPoint ? e.clientX : rect.left + rect.width / 2;
    const y = hasPoint ? e.clientY : rect.top + rect.height / 2;

    // HeroUI 为 toast 动画在 base.css 里全局禁了 root 参与（:root { view-transition-name: none }），
    // 整页扩散需要 root 快照，这里临时用 inline style 恢复，结束后归还
    const root = document.documentElement;
    root.style.viewTransitionName = 'root';

    const transition = document.startViewTransition(() => {
      // flushSync 让主题类在捕获新快照前同步落到 <html> 上
      flushSync(() => setTheme(next));
    });

    transition.ready
      .then(() => {
        // 扩散半径取触点到最远角的对角线，保证圆形铺满整页
        const endRadius = Math.hypot(
          Math.max(x, window.innerWidth - x),
          Math.max(y, window.innerHeight - y),
        );
        document.documentElement.animate(
          {
            clipPath: [
              `circle(0px at ${x}px ${y}px)`,
              `circle(${endRadius}px at ${x}px ${y}px)`,
            ],
          },
          {
            duration: 700,
            easing: 'ease-in-out',
            pseudoElement: '::view-transition-new(root)',
          },
        );
      })
      .catch(() => {}); // 被下一次切换 skip 时 ready 会 reject，忽略即可

    // skip 时 finished 依然 resolve，finally 保证归还；不依赖动画结束事件做状态正确性
    transition.finished.finally(() => {
      root.style.viewTransitionName = '';
    });
  };

  return (
    <label className="theme-switch" title={isDark ? '切换到浅色' : '切换到深色'}>
      <input
        type="checkbox"
        className="theme-switch__toggle"
        checked={isDark}
        onChange={handleChange}
        aria-label="深色模式"
      />
      <span className="theme-switch__icon" aria-hidden="true">
        {Array.from({ length: 9 }, (_, i) => (
          <span key={i} className="theme-switch__icon-part" />
        ))}
      </span>
    </label>
  );
}