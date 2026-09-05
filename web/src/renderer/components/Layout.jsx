import { useState } from 'react';
import { Tabs, Modal, Button, Typography } from '@heroui/react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import ThemeToggle from './ThemeToggle';
import { getNavBlock } from '../utils/navGuard';
import logo from '../assets/logo.png';

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
  // 导航守卫：页面注册了未保存数据时，切页先存下目标，确认后再走
  const [pendingNav, setPendingNav] = useState(null);

  const currentTab = tabs.find((t) => location.pathname.startsWith(t.path))?.id || 'channels';

  const handleTabChange = (key) => {
    const tab = tabs.find((t) => t.id === key);
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
    <div className="relative z-10 flex flex-col h-screen bg-background/70 text-foreground">
      <header
        className="glass relative flex items-center border-b border-foreground/10 h-14 pl-[88px] pr-6"
        style={{ WebkitAppRegion: 'drag' }}
      >
        {/* traffic-light 留空区：左侧 pl-[88px] 让 logo 与 macOS 红黄绿灯之间留出间距 */}
        <div className="flex items-center gap-2.5">
          <img src={logo} alt="" aria-hidden="true" className="h-10 w-auto pointer-events-none" />
          <span className="brand-name">Portunus</span>
        </div>
        <div className="absolute left-1/2 -translate-x-1/2">
          <Tabs
            className="nav-tabs w-fit whitespace-nowrap"
            selectedKey={currentTab}
            onSelectionChange={handleTabChange}
          >
            <Tabs.ListContainer>
              <Tabs.List aria-label="导航" className="flex-nowrap">
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
      <main className="flex-1 min-h-0 p-6">
        {/* key 换页时重建容器，触发 page-in 上浮淡入过渡 */}
        <div key={location.pathname} className="page-in mx-auto max-w-7xl h-full flex flex-col">
          <Outlet />
        </div>
      </main>

      {/* 导航守卫确认：任一页面注册了未保存数据（navGuard），切页前拦截 */}
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
              <Button slot="close" variant="secondary">
                留在此页
              </Button>
              <Button variant="danger" onPress={confirmLeave}>
                丢弃并离开
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </div>
  );
}
