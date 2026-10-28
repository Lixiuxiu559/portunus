import request from './request';

/** 上游模型按渠道维护；创建只需 channel_id 和 name（上游模型 ID）。 */

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

/** 删除影响预览（只读）：受影响分组、移除成员与激活项清除标记 */
export function getModelDeletionImpact(id, options) {
  return request.get(`/models/${id}/deletion-impact`, options);
}

/** 删除模型 */
export function deleteModel(id) {
  return request.delete(`/models/${id}`);
}
