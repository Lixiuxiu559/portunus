import { Tabs } from '@heroui/react';
import { Outlet, useLocation, useNavigate } from 'react-router-dom';
import { tabs } from './Header';
import ThemeToggle from './ThemeToggle';
import logo from '../assets/logo.png';

export default function Layout() {
  const location = useLocation();
  const navigate = useNavigate();

  const currentTab = tabs.find((t) => location.pathname.startsWith(t.path))?.id || 'channels';

  const handleTabChange = (key) => {
    const tab = tabs.find((t) => t.id === key);
    if (tab) {
      navigate(tab.path);
    }
  };

  return (
    <div className="relative z-10 flex flex-col h-screen bg-background/70 text-foreground">
      <header
        className="relative grid items-center gap-4 border-b border-separator h-14"
        style={{ WebkitAppRegion: 'drag' }}
      >
        {/* traffic-light 留空区：给 macOS 红黄绿灯腾出空间 */}
        <div className="absolute left-0 top-0 h-full w-[72px]" />
        {/* header 内容：整行居中，与 Web 版区分 */}
        <div className="flex items-center justify-center gap-4 h-full pl-[72px] pr-6">
          <img src={logo} alt="Portunus" className="h-10 w-auto pointer-events-none" />
          <div style={{ WebkitAppRegion: 'no-drag' }}>
            <Tabs
              className="w-fit whitespace-nowrap"
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
          <div className="absolute right-6 top-1/2 -translate-y-1/2" style={{ WebkitAppRegion: 'no-drag' }}>
            <ThemeToggle />
          </div>
        </div>
      </header>
      <main className="flex-1 min-h-0 p-6">
        <div className="mx-auto max-w-7xl h-full flex flex-col">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
