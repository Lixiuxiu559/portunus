import { useTheme } from '@heroui/react';
import { Sun, Moon, Monitor } from 'lucide-react';

const themes = [
  { key: 'light', icon: Sun },
  { key: 'dark', icon: Moon },
  { key: 'system', icon: Monitor },
];

// 主题切换：复用 HeroUI useTheme（与 inflow ThemeSwitch 一致）
// - 持久化到 localStorage(heroui-theme)，默认 system
// - 自动订阅系统 prefers-color-scheme 变化
// - 给 <html> 应用 light/dark class + data-theme 属性
export default function ThemeToggle() {
  const { theme, setTheme } = useTheme('system');

  return (
    <div className="flex items-center rounded-full bg-default p-0.5 gap-0.5">
      {themes.map((t) => (
        <button
          key={t.key}
          onClick={() => setTheme(t.key)}
          className={`size-6 flex items-center justify-center rounded-full transition-all cursor-pointer ${
            theme === t.key ? 'bg-background shadow-sm' : 'text-muted hover:text-foreground'
          }`}
        >
          <t.icon className="size-3.5" />
        </button>
      ))}
    </div>
  );
}
