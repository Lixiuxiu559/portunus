import axios from 'axios';
import { toast } from '@heroui/react';

/**
 * 全局 axios 实例
 * - 开发模式：baseURL /api（Vite 代理到后端 localhost:3060）
 * - 生产模式：baseURL http://localhost:13060/api（直连 Go sidecar）
 * - timeout: 30s
 *
 * 后端响应约定（管理 API，Gin）：
 * - 成功：直接返回业务数据（数组 / 对象 / { total, data } 等）
 * - 失败：HTTP 状态码 + { error: "消息" }
 */
const isProd = typeof window !== 'undefined' && window.location.protocol === 'file:';
// 生产模式：从 preload 读取配置的后端地址；开发模式：用 Vite proxy
const serverUrl = isProd ? (window.api?.getServerUrl?.() || 'http://localhost:13060') : '';
const BASE_URL = isProd ? `${serverUrl}/api` : '/api';

const request = axios.create({
  baseURL: BASE_URL,
  timeout: 30_000,
  headers: { 'Content-Type': 'application/json' },
});

// ─── 请求拦截器 ───
// 管理 API 暂未接入鉴权；后续有登录态时在此注入 Authorization
request.interceptors.request.use(
  (config) => config,
  (error) => Promise.reject(error),
);

// ─── 响应拦截器 ───
request.interceptors.response.use(
  (response) => {
    // 后端无 R 包装，直接返回业务数据
    return response.data;
  },
  (error) => {
    // 网络层 / 业务错误
    const httpStatus = error.response?.status;
    const serverMsg = error.response?.data?.error;
    const msg = serverMsg || error.message || '网络错误，请检查后端服务';

    toast.danger(msg, {
      description: httpStatus ? `HTTP ${httpStatus}` : '无法连接到服务器',
    });

    const err = new Error(msg);
    err.status = httpStatus;
    console.error('[HTTP Error]', err.message);
    return Promise.reject(err);
  },
);

export default request;
