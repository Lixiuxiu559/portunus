import { useState } from 'react';
import { Button } from '@heroui/react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { Layers2, Network, Boxes, PlugZap, Activity, Settings2, PanelLeftClose, PanelLeftOpen, ChevronRight } from 'lucide-react';
import ThemeToggle from './ThemeToggle';
import logo from '../assets/logo.png';
import { useUpdater } from '../hooks/useUpdater';
import '../workspace.css';

export const tabs = [
  { id: 'groups', label: '模型分组', path: '/groups', icon: Layers2, section: '工作区' },
  { id: 'channels', label: '上游渠道', path: '/channels', icon: Network, section: '工作区' },
  { id: 'models', label: '上游模型', path: '/models', icon: Boxes, section: '工作区' },
  { id: 'clients', label: '服务接入', path: '/clients', icon: PlugZap, section: '工具' },
  { id: 'logs', label: '调用记录', path: '/logs', icon: Activity, section: '工具' },
  { id: 'settings', label: '设置', path: '/settings', icon: Settings2, section: '应用' },
];

export default function Layout() {
  const location = useLocation();
  const [compact, setCompact] = useState(false);
  const { downloaded, installing, latestVersion, error, install } = useUpdater();
  const current = tabs.find((tab) => location.pathname.startsWith(tab.path)) || tabs[0];
  const isDev = window.api?.isDevelopment;
  const appName = window.api?.appName || 'Portunus';

  return (
    <div className={`app-shell${compact ? ' app-shell--compact' : ''}`}>
      <a href="#workspace-content" className="app-skip-link" onClick={(event) => { event.preventDefault(); document.getElementById('workspace-content')?.focus(); }}>跳转到内容</a>
      <aside className="app-sidebar" aria-label="工作区侧栏">
        <div className="app-sidebar-brand">
          <img src={logo} alt="" aria-hidden="true" />
          <div className="app-brand-copy"><span className="app-brand-name">Portunus</span><span className="app-brand-caption">{isDev ? '开发工作区' : '模型服务工作区'}</span></div>
          {isDev && <span className="app-dev-badge">DEV</span>}
        </div>
        <nav className="app-navigation" aria-label="主导航">
          {['工作区', '工具', '应用'].map((section) => <div className="app-nav-group" key={section}>
            <span className="app-nav-label">{section}</span>
            {tabs.filter((tab) => tab.section === section).map(({ id, path, label, icon: Icon }) => <NavLink key={id} to={path} title={label} aria-label={label} className={({ isActive }) => `app-nav-link${isActive ? ' app-nav-link--active' : ''}`}>
              <Icon size={18} strokeWidth={1.7} aria-hidden="true" /><span>{label}</span>
            </NavLink>)}
          </div>)}
        </nav>
        <div className="app-sidebar-footer"><span className="app-appearance-label">外观</span><ThemeToggle /></div>
      </aside>
      <section className="app-workspace" aria-label={appName}>
        <header className="app-titlebar">
          <button type="button" className="app-sidebar-toggle" aria-label={compact ? '展开侧栏' : '收起侧栏'} aria-expanded={!compact} onClick={() => setCompact((value) => !value)} title={compact ? '展开侧栏' : '收起侧栏'}>
            {compact ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
          </button>
          <div className="app-breadcrumb" aria-label="当前位置"><span>{current.section}</span><ChevronRight size={12} aria-hidden="true" /><span>{current.label}</span></div>
          <span className="app-window-caption">{isDev ? 'Portunus Dev' : 'Portunus'}</span>
        </header>
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
