import request from './request';

/**
 * 模型：归渠道，带价格（输入/输出/缓存读/缓存写，每 1M token）
 * 创建字段：channel_id, name, input_price?, output_price?,
 *   cache_read_price?, cache_write_price?, enabled?（价格不传则用内置默认价）
 * 更新字段均为可选。
 */

/** 模型列表；channelId > 0 时仅返回该渠道下的模型 */
export function listModels(params) {
  // params: { channel_id }
  return request.get('/models', { params });
}

/** 模型详情 */
export function getModel(id) {
  return request.get(`/models/${id}`);
}

/** 创建模型 */
export function createModel(data) {
  return request.post('/models', data);
}

/** 更新模型（仅传需变更的字段） */
export function updateModel(id, data) {
  return request.put(`/models/${id}`, data);
}

/** 删除模型 */
export function deleteModel(id) {
  return request.delete(`/models/${id}`);
}
