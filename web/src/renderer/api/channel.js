import request from './request';

/**
 * 渠道：上游供应商连接配置
 * 创建/更新字段：name, type(openai/openai_responses/anthropic/gemini),
 *   base_url, key, enabled?, auto_sync?
 * 响应中 key 已脱敏（仅创建时返回明文）。
 */

/** 渠道列表 */
export function listChannels() {
  return request.get('/channels');
}

/** 渠道详情 */
export function getChannel(id) {
  return request.get(`/channels/${id}`);
}

/** 创建渠道 */
export function createChannel(data) {
  return request.post('/channels', data);
}

/** 更新渠道（仅传需变更的字段） */
export function updateChannel(id, data) {
  return request.put(`/channels/${id}`, data);
}

/** 删除渠道 */
export function deleteChannel(id) {
  return request.delete(`/channels/${id}`);
}

/** 同步渠道模型 */
export function syncChannel(id) {
  return request.post(`/channels/${id}/sync`);
}
