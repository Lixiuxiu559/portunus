import request from './request';

/**
 * API Key：对外 /v1 接口鉴权凭据
 * 单 key 场景：仅「读」与「重新生成」两个动作，返回完整 key 便于复制。
 */

/** 获取唯一 API Key */
export function getAPIKey() {
  return request.get('/apikey');
}

/** 重新生成 API Key，返回新 key 完整明文 */
export function regenerateAPIKey() {
  return request.post('/apikey/regenerate');
}