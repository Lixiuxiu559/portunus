import request from './request';
import { RETRY_MAX_ATTEMPTS } from './retryPolicy';

/**
 * 渠道：上游供应商连接配置
 * 创建/更新字段：name, type(openai/openai_responses/anthropic/gemini),
 *   base_url, key, auto_sync?
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

/** 删除影响预览（只读）：受影响分组、模型数量与余额来源提示 */
export function getChannelDeletionImpact(id, options) {
  return request.get(`/channels/${id}/deletion-impact`, options);
}

/** 删除渠道 */
export function deleteChannel(id) {
  return request.delete(`/channels/${id}`);
}

/** 同步渠道模型 */
export function syncChannel(id) {
  return request.post(`/channels/${id}/sync`);
}

/** 预览上游模型列表（不创建渠道） */
export function previewModels(data, options) {
  return request.post('/channels/preview-models', data, options);
}

/** 后端维护的厂商与协议配置档目录 */
export function listUpstreamPresets(options) {
  return request.get('/upstream-presets', options);
}

/** 纯本地解析：不调用上游 */
export function resolveChannel(data, options) {
  return request.post('/channels/resolve', data, options);
}

/** 单模型主动验证。禁止全局网络重发，避免重复消耗上游额度。 */
export function testChannel(data, options) {
  return request.post('/channels/test', data, { ...options, __retryCount: RETRY_MAX_ATTEMPTS });
}
