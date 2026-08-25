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
      <header className="grid grid-cols-[auto_1fr_auto] items-center gap-4 border-b border-separator px-6 py-3">
        <img src={logo} alt="Portunus" className="h-10 w-auto" />
        <div className="flex justify-center">
          <Tabs
            className="w-fit"
            selectedKey={currentTab}
            onSelectionChange={handleTabChange}
          >
            <Tabs.ListContainer>
              <Tabs.List aria-label="导航">
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
        <div className="flex justify-end">
          <ThemeToggle />
        </div>
      </header>
      <main className="flex-1 overflow-auto p-6">
        <div className="mx-auto max-w-5xl">
          <Outlet />
        </div>
      </main>
    </div>
  );
}
