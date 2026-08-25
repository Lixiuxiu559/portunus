import { Typography } from '@heroui/react';
import logo from '../assets/logo.png';

export default function Login() {
  return (
    <div className="flex flex-col items-center justify-center h-screen bg-background">
      <img src={logo} alt="Portunus" className="h-20 w-auto mb-8" />
      <Typography type="h1">Portunus</Typography>
      <Typography type="p" className="text-muted mt-2">
        LLM API 聚合服务管理后台
      </Typography>
    </div>
  );
}
