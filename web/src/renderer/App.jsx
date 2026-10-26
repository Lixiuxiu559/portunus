import { lazy, useEffect } from 'react';
import { HashRouter, Routes, Route, Navigate } from 'react-router-dom';
import Background from './components/Background';
import Layout from './components/Layout';
const pageLoaders = {
  channels: () => import('./pages/Channels'),
  models: () => import('./pages/Models'),
  groups: () => import('./pages/Groups'),
  logs: () => import('./pages/Logs'),
  clients: () => import('./pages/Clients'),
  settings: () => import('./pages/Settings'),
};
const pages = Object.fromEntries(Object.entries(pageLoaders).map(([id, load]) => [id, lazy(load)]));

// 先加载当前地址对应的一页，避免冷启动先显示 Suspense、再等待第二次提交。
export async function preloadInitialPage(hash) {
  const id = hash.split('?')[0].replace(/^#\//, '') || 'channels';
  const page = Object.hasOwn(pageLoaders, id) ? id : 'channels';
  pages[page] = (await pageLoaders[page]()).default;
}

function ReadyPage({ children }) {
  useEffect(() => {
    window.api?.confirmRendererReady?.().catch(() => {});
  }, []);
  return children;
}

export default function App() {
  const { channels: Channels, models: Models, groups: Groups, logs: Logs, clients: Clients, settings: Settings } = pages;
  return (
    <HashRouter>
      <Background />
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/channels" replace />} />
          <Route path="channels" element={<ReadyPage><Channels /></ReadyPage>} />
          <Route path="models" element={<ReadyPage><Models /></ReadyPage>} />
          <Route path="groups" element={<ReadyPage><Groups /></ReadyPage>} />
          <Route path="logs" element={<ReadyPage><Logs /></ReadyPage>} />
          <Route path="clients" element={<ReadyPage><Clients /></ReadyPage>} />
          <Route path="settings" element={<ReadyPage><Settings /></ReadyPage>} />
        </Route>
      </Routes>
    </HashRouter>
  );
}
