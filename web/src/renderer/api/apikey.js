import request from './request';

/**
 * API Key：对外 /v1 接口鉴权凭据
 * 命名 Key 用于区分调用方；单 Key 接口保留给旧调用方兼容。
 */

/** 兼容接口：获取默认的有效 API Key。 */
export function getAPIKey() {
  return request.get('/apikey');
}

/** 兼容接口：重新生成默认的有效 API Key。 */
export function regenerateAPIKey() {
  return request.post('/apikey/regenerate');
}
/** 管理列表含已禁用项，不含已删除项。 */
export function listAPIKeys() { return request.get('/apikeys'); }
export function createAPIKey(name) { return request.post('/apikeys', { name }); }
export function renameAPIKey(id, name) { return request.put(`/apikeys/${id}`, { name }); }
export function regenerateNamedAPIKey(id) { return request.post(`/apikeys/${id}/regenerate`); }
export function setAPIKeyStatus(id, revoked) { return request.put(`/apikeys/${id}/status`, { revoked }); }
export function deleteAPIKey(id) { return request.delete(`/apikeys/${id}`); }
/** 历史元数据不含明文密钥，与普通列表按查询参数隔离缓存。 */
export function listHistoricalAPIKeys() { return request.get('/apikeys', { params: { include_deleted: true } }); }
