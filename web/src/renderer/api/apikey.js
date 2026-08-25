import request from './request';

/**
 * API Key：对外 /v1 接口鉴权凭据
 * 创建时返回完整 key（仅此一次）；列表/更新返回脱敏 key。
 */

/** API Key 列表（key 脱敏） */
export function listAPIKeys() {
  return request.get('/apikeys');
}

/** 创建 API Key { name }，返回完整 key */
export function createAPIKey(name) {
  return request.post('/apikeys', { name });
}

/** 更新 API Key { name?, enabled? } */
export function updateAPIKey(id, data) {
  return request.put(`/apikeys/${id}`, data);
}

/** 删除 API Key */
export function deleteAPIKey(id) {
  return request.delete(`/apikeys/${id}`);
}
