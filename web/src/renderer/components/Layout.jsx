import { Button, Tabs } from '@heroui/react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import ThemeToggle from './ThemeToggle';
import useLiquidNav from '../hooks/useLiquidNav';
import logo from '../assets/logo.png';
import { useUpdater } from '../hooks/useUpdater';

export const tabs = [
  { id: 'groups', label: '模型分组', path: '/groups' },
  { id: 'channels', label: '上游渠道', path: '/channels' },
  { id: 'models', label: '上游模型', path: '/models' },
  { id: 'clients', label: '服务接入', path: '/clients' },
  { id: 'logs', label: '调用记录', path: '/logs' },
  { id: 'settings', label: '设置', path: '/settings' },
];

export default function Layout() {
  const location = useLocation();
  const navigate = useNavigate();
  const { downloaded, installing, latestVersion, error, install } = useUpdater();

  const currentTab = tabs.find((t) => location.pathname.startsWith(t.path))?.id || 'groups';

  // 液态玻璃导航：距离感知拉伸 + 点击涟漪（位置流动由 RAC SharedElement + CSS 完成）
  const liquid = useLiquidNav(currentTab);

  const handleTabChange = (key) => {
    const tab = tabs.find((t) => t.id === key);
    if (!tab || tab.path === location.pathname) return;
    navigate(tab.path);
  };

  return (
    <div className="relative z-10 flex flex-col h-screen bg-background/70 text-foreground">
      <header
        className="glass relative flex items-center border-b border-foreground/10 h-14 pl-[88px] pr-6"
        style={{ WebkitAppRegion: 'drag' }}
      >
        {/* traffic-light 留空区：左侧 pl-[88px] 让 logo 与 macOS 红黄绿灯之间留出间距 */}
        <div className="flex items-center gap-2.5">
          <img src={logo} alt="" aria-hidden="true" className="h-10 w-auto pointer-events-none" />
          <span className={`brand-name${window.api?.isDevelopment ? " brand-name--dev" : ""}`} title={window.api?.isDevelopment ? "开发环境 · 13061" : undefined}>{window.api?.appName || "Portunus"}</span>
        </div>
        <div className="absolute left-1/2 -translate-x-1/2">
          <Tabs
            className="nav-tabs w-fit whitespace-nowrap"
            selectedKey={currentTab}
            onSelectionChange={handleTabChange}
          >
            <Tabs.ListContainer>
              <Tabs.List aria-label="导航" className="flex-nowrap" ref={liquid.listRef}>
                {tabs.map((tab) => (
                  <Tabs.Tab id={tab.id} key={tab.id}>
                    {tab.label}
                    <Tabs.Indicator />
                  </Tabs.Tab>
                ))}
              </Tabs.List>
            </Tabs.ListContainer>
          </Tabs>
        </div>
        <div className="ml-auto flex items-center">
          <ThemeToggle />
        </div>
      </header>
      {downloaded && (
        <div className="flex shrink-0 items-center justify-between gap-4 border-b border-accent/20 bg-accent/10 px-6 py-2" role="status">
          <span className={`text-sm ${error ? 'text-danger' : 'text-foreground'}`}>
            {error ? `更新失败：${error}` : `新版本 v${latestVersion} 已下载，重启即可更新`}
          </span>
          <Button variant="primary" size="sm" onPress={install} isDisabled={installing}>
            {installing ? '正在重启…' : '重启以更新'}
          </Button>
        </div>
      )}
      <main className="flex-1 min-h-0 p-6">
        {/* key 换页时重建容器，触发 page-in 上浮淡入过渡 */}
        <div key={location.pathname} className="page-in mx-auto max-w-7xl h-full flex flex-col">
          <Outlet />
        </div>
      </main>


    </div>
  );
}
