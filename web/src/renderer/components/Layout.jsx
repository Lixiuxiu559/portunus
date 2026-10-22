import { useState } from 'react';
import { Button, Modal, Tabs, Typography } from '@heroui/react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import ThemeToggle from './ThemeToggle';
import logo from '../assets/logo.png';
import { useUpdater } from '../hooks/useUpdater';
import useLiquidNav from '../hooks/useLiquidNav';
import { getNavBlock } from '../utils/navGuard';
import '../workspace.css';

export const tabs = [
  { id: 'channels', label: '渠道', path: '/channels' },
  { id: 'models', label: '模型', path: '/models' },
  { id: 'groups', label: '分组', path: '/groups' },
  { id: 'logs', label: '日志', path: '/logs' },
  { id: 'clients', label: '服务接入', path: '/clients' },
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
  // 导航守卫：页面注册了未保存数据时，切页先存下目标，确认后再走
  const [pendingNav, setPendingNav] = useState(null);

  const handleTabChange = (key) => {
    const tab = tabs.find((item) => item.id === key);
    if (!tab || tab.path === location.pathname) return;
    if (getNavBlock()) {
      setPendingNav(tab);
      return;
    }
    navigate(tab.path);
  };

  const confirmLeave = () => {
    if (pendingNav) navigate(pendingNav.path);
    setPendingNav(null);
  };

  return (
    <div className="app-shell">
      <a href="#workspace-content" className="app-skip-link" onClick={(event) => { event.preventDefault(); document.getElementById('workspace-content')?.focus(); }}>跳转到内容</a>
      <header className="app-topbar glass">
        <div className="app-topbar-brand">
          <img src={logo} alt="" aria-hidden="true" />
          <span className={`brand-name${isDev ? ' brand-name--dev' : ''}`} title={isDev ? '开发环境 · 13061' : undefined}>{appName}</span>
        </div>
        <nav className="app-top-navigation" aria-label="主导航">
          <Tabs className="nav-tabs w-fit whitespace-nowrap" selectedKey={current.id} onSelectionChange={handleTabChange}>
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

      {/* 导航守卫确认：页面注册了未保存数据（navGuard）时，切页前拦截 */}
      <Modal.Backdrop isOpen={!!pendingNav} onOpenChange={(open) => !open && setPendingNav(null)}>
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.CloseTrigger />
            <Modal.Header>
              <Modal.Heading>离开当前页面？</Modal.Heading>
            </Modal.Header>
            <Modal.Body>
              <Typography color="muted">{getNavBlock()}，离开将丢弃未保存的内容。</Typography>
            </Modal.Body>
            <Modal.Footer>
              <Button slot="close" variant="secondary">留在此页</Button>
              <Button variant="danger" onPress={confirmLeave}>丢弃并离开</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </div>
  );
}
