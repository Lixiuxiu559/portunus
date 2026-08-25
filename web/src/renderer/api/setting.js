import request from './request';

/**
 * 全局设置：当前仅货币计价
 * currency: USD | CNY
 */

/** 获取全局设置 { currency } */
export function getSettings() {
  return request.get('/settings');
}

/** 设置货币计价 */
export function setCurrency(currency) {
  return request.put('/settings/currency', { currency });
}
