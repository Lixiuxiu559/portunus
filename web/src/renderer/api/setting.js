import request from './request';

/**
 * 全局维护设置：模型同步间隔、日志保留等
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
