import request from './request';

/**
 * 全局设置：当前仅货币计价
 * currency: USD | CNY
 */

/** 获取全局设置 { currency, sync_interval, last_sync_at } */
export function getSettings() {
  return request.get('/settings');
}

/** 设置货币计价 */
export function setCurrency(currency) {
  return request.put('/settings/currency', { currency });
}

/** 设置自动同步间隔（分钟，0 = 关闭） */
export function setSyncInterval(minutes) {
  return request.put('/settings/sync-interval', { sync_interval: minutes });
}

/** 立即同步所有开启自动同步的渠道 */
export function syncNow() {
  return request.post('/settings/sync-now');
}
