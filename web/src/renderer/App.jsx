import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Background from './components/Background';
import Layout from './components/Layout';
import Login from './pages/Login';
import Channels from './pages/Channels';
import Models from './pages/Models';
import Groups from './pages/Groups';
import Logs from './pages/Logs';
import Settings from './pages/Settings';

export default function App() {
  return (
    <BrowserRouter>
      <Background />
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/channels" replace />} />
          <Route path="channels" element={<Channels />} />
          <Route path="models" element={<Models />} />
          <Route path="groups" element={<Groups />} />
          <Route path="logs" element={<Logs />} />
          <Route path="settings" element={<Settings />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
