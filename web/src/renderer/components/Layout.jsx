import { Button, Tabs } from '@heroui/react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import ThemeToggle from './ThemeToggle';
import logo from '../assets/logo.png';
import { useUpdater } from '../hooks/useUpdater';
import useLiquidNav from '../hooks/useLiquidNav';
import '../workspace.css';

export const tabs = [
  { id: 'channels', label: '渠道', path: '/channels' },
  { id: 'models', label: '模型', path: '/models' },
  { id: 'groups', label: '分组', path: '/groups' },
  { id: 'logs', label: '日志', path: '/logs' },
  { id: 'clients', label: '客户端', path: '/clients' },
  { id: 'settings', label: '设置', path: '/settings' },
];

export default function Layout() {
  const location = useLocation();
  const navigate = useNavigate();
  const { downloaded, installing, latestVersion, error, install } = useUpdater();
  const current = tabs.find((tab) => location.pathname.startsWith(tab.path)) || tabs[0];
  const liquid = useLiquidNav(current.id);
  const isDev = window.api?.isDevelopment;
  const appName = window.api?.appName || 'Portunus';

  return (
    <div className="app-shell">
      <a href="#workspace-content" className="app-skip-link" onClick={(event) => { event.preventDefault(); document.getElementById('workspace-content')?.focus(); }}>跳转到内容</a>
      <header className="app-topbar glass">
        <div className="app-topbar-brand">
          <img src={logo} alt="" aria-hidden="true" />
          <span className={`brand-name${isDev ? ' brand-name--dev' : ''}`} title={isDev ? '开发环境 · 13061' : undefined}>{appName}</span>
        </div>
        <nav className="app-top-navigation" aria-label="主导航">
          <Tabs className="nav-tabs w-fit whitespace-nowrap" selectedKey={current.id} onSelectionChange={(key) => {
            const tab = tabs.find((item) => item.id === key);
            if (tab && tab.path !== location.pathname) navigate(tab.path);
          }}>
            <Tabs.ListContainer>
              <Tabs.List aria-label="导航" className="flex-nowrap" ref={liquid.listRef}>
                {tabs.map((tab) => <Tabs.Tab id={tab.id} key={tab.id}>{tab.label}<Tabs.Indicator /></Tabs.Tab>)}
              </Tabs.List>
            </Tabs.ListContainer>
          </Tabs>
        </nav>
        <div className="app-topbar-actions"><ThemeToggle /></div>
      </header>
      <section className="app-workspace" aria-label={appName}>
        {downloaded && <div className="app-update-banner" role="status">
          <span className={error ? 'text-danger' : ''}>{error ? `更新失败：${error}` : `新版本 v${latestVersion} 已下载，重启即可更新`}</span>
          <Button variant="primary" size="sm" onPress={install} isDisabled={installing}>{installing ? '正在重启…' : '重启以更新'}</Button>
        </div>}
        <main id="workspace-content" tabIndex={-1} className="app-main">
          <div key={location.pathname} className="page-in app-page"><Outlet /></div>
        </main>
      </section>
    </div>
  );
}
