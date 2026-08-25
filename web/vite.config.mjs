import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath, URL } from 'node:url';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: './',
  root: 'src/renderer',
  resolve: {
    alias: {
      // `@` 指向 renderer 根目录，便于跨目录引用公共组件
      '@': fileURLToPath(new URL('./src/renderer', import.meta.url)),
    },
  },
  build: {
    outDir: '../../dist/renderer',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      // 管理 API 代理：转发到 portunus 后端
      // 前端源码模块请求（/api/*.js，Accept: */*）不能被代理，需 bypass 回 Vite
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        bypass: (req) => {
          if (req.url.endsWith('.js') || req.url.endsWith('.mjs') || req.url.includes('?')) {
            return req.url;
          }
        },
      },
      // 对外 LLM 接口代理（claude code / codex 等调试用）
      '/v1': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
});
