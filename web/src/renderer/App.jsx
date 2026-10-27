import { lazy, useEffect, useState } from 'react';
import { HashRouter, Routes, Route, Navigate } from 'react-router-dom';
import Background from './components/Background';
import Layout from './components/Layout';
import { pageIds, loadPage, getLoadedPage, warmPages } from './pageResources';
const pages = Object.fromEntries(pageIds.map((id) => [id, lazy(() => loadPage(id))]));

// 先加载当前地址对应的一页，避免冷启动先显示 Suspense、再等待第二次提交。
export async function preloadInitialPage(hash) {
  const id = hash.split('?')[0].replace(/^#\//, '') || 'channels';
  await loadPage(pageIds.includes(id) ? id : 'channels');
}

function RoutePage({ id }) {
  // 已预加载的页面直接渲染，避免 React.lazy 首次读取 Promise 时再进入 Suspense。
  // 挂载后保持组件身份，后台预加载完成不能导致编辑中的页面重新挂载。
  const [Page] = useState(() => getLoadedPage(id) || pages[id]);
  return <ReadyPage><Page /></ReadyPage>;
}

function ReadyPage({ children }) {
  useEffect(() => {
    window.api?.confirmRendererReady?.().catch(() => {});
  }, []);
  return children;
}

export default function App() {
  useEffect(() => warmPages(), []);
  return (
    <HashRouter>
      <Background />
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/channels" replace />} />
          {pageIds.map((id) => <Route key={id} path={id} element={<RoutePage id={id} />} />)}
        </Route>
      </Routes>
    </HashRouter>
  );
}
