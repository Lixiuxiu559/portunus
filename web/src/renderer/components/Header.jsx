import { Tabs } from '@heroui/react';
import logo from '../assets/logo.png';
import ThemeToggle from './ThemeToggle';

export const tabs = [
  { id: 'channels', label: '渠道', path: '/channels' },
  { id: 'models', label: '模型', path: '/models' },
  { id: 'groups', label: '分组', path: '/groups' },
  { id: 'logs', label: '日志', path: '/logs' },
  { id: 'clients', label: '客户端', path: '/clients' },
  { id: 'settings', label: '设置', path: '/settings' },
];

const Header = () => {
  return (
    <header className="grid grid-cols-[auto_1fr_auto] items-center gap-4 border-b border-separator px-6 py-3">
      <img src={logo} alt="Portunus" className="h-10 w-auto" />
      <div className="flex justify-center">
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
      </div>
      <div className="flex justify-end">
        <ThemeToggle />
      </div>
    </header>
  );
};

export default Header;
