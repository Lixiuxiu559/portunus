import React from 'react';
import ReactDOM from 'react-dom/client';
import { ToastProvider } from '@heroui/react';
import App, { preloadInitialPage } from './App';
import { initModalOrigin } from './utils/modalOrigin';
import './index.css';

document.title = window.api?.appName || 'Portunus';
initModalOrigin();

const root = document.getElementById('root');
preloadInitialPage(window.location.hash).then(() => {
  ReactDOM.createRoot(root).render(
    <React.StrictMode>
      <ToastProvider placement="top end" />
      <App />
    </React.StrictMode>,
  );
}).catch((error) => {
  root.textContent = '页面资源加载失败，请重新打开应用。';
  root.setAttribute('role', 'alert');
  console.error('[Startup]', error);
});
