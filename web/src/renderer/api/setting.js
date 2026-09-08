import request from './request';

/**
 * 全局设置：同步间隔、日志保留等（模型计价货币是模型自身属性，不在全局设置）
 */

/** 获取全局设置 { sync_interval, last_sync_at, log_retention_days } */
export function getSettings() {
  return request.get('/settings');
}

/** 设置自动同步间隔（分钟，0 = 关闭） */
export function setSyncInterval(minutes) {
  return request.put('/settings/sync-interval', { sync_interval: minutes });
}

/** 立即同步所有开启自动同步的渠道 */
export function syncNow() {
  return request.post('/settings/sync-now');
}
