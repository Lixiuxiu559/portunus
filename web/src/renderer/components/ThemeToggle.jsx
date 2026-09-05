import { useTheme } from '@heroui/react';

/**
 * 主题切换开关：浅色（太阳）↔ 深色（月亮），复刻 uiverse "old-falcon" 的滑动动画。
 * - resolvedTheme 会解析 system（跟随系统）为实际的 light / dark
 * - 手动切换后即设为明确的 light / dark
 */
export default function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme('system');
  const isDark = resolvedTheme === 'dark';

  return (
    <label className="theme-switch" title={isDark ? '切换到浅色' : '切换到深色'}>
      <input
        type="checkbox"
        className="theme-switch__toggle"
        checked={isDark}
        onChange={(e) => setTheme(e.target.checked ? 'dark' : 'light')}
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