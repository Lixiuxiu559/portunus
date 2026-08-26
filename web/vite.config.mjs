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
      // 前端源码模块请求（/api/*.js，含 ?t= 时间戳）不能被代理，需 bypass 回 Vite
      '/api': {
        target: 'http://localhost:3060',
        changeOrigin: true,
        bypass: (req) => {
          // 只看路径（去掉查询串）：只有 .js/.mjs 源码模块才 bypass，
          // 带查询串的 API 请求（如 /api/models?name=xxx）必须代理到后端
          const pathname = req.url.split('?')[0];
          if (pathname.endsWith('.js') || pathname.endsWith('.mjs')) {
            return req.url;
          }
        },
      },
      // 对外 LLM 接口代理（claude code / codex 等调试用）
      '/v1': {
        target: 'http://localhost:3060',
        changeOrigin: true,
      },
    },
  },
});
