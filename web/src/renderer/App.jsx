import { HashRouter, Routes, Route, Navigate } from 'react-router-dom';
import Background from './components/Background';
import Layout from './components/Layout';
import Channels from './pages/Channels';
import Models from './pages/Models';
import Groups from './pages/Groups';
import Logs from './pages/Logs';
import Clients from './pages/Clients';
import Settings from './pages/Settings';

export default function App() {
  return (
    <HashRouter>
      <Background />
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/channels" replace />} />
          <Route path="channels" element={<Channels />} />
          <Route path="models" element={<Models />} />
          <Route path="groups" element={<Groups />} />
          <Route path="logs" element={<Logs />} />
          <Route path="clients" element={<Clients />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </HashRouter>
  );
}
