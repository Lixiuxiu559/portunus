import React from 'react';
import ReactDOM from 'react-dom/client';
import { ToastProvider } from '@heroui/react';
import App from './App';
import './index.css';

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <ToastProvider placement="top end" />
    <App />
  </React.StrictMode>,
);
