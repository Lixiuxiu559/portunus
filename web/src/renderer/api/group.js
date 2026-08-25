import request from './request';

/**
 * 分组：对外模型名，聚合多个渠道模型，必配路由策略
 * strategy: manual | round_robin | failover
 * 分组项：{ id, group_id, model_id, priority }
 */

/** 分组列表（含分组项，按 priority 排序） */
export function listGroups() {
  return request.get('/groups');
}

/** 分组详情 */
export function getGroup(id) {
  return request.get(`/groups/${id}`);
}

/** 创建分组；可同时传初始分组项 { name, strategy, items?: [{model_id, priority?}] } */
export function createGroup(data) {
  return request.post('/groups', data);
}

/** 更新分组基础字段 { name?, strategy?, active_item_id? } */
export function updateGroup(id, data) {
  return request.put(`/groups/${id}`, data);
}

/** 删除分组（级联删除全部分组项） */
export function deleteGroup(id) {
  return request.delete(`/groups/${id}`);
}

/** 向分组添加模型 { model_id, priority? } */
export function addGroupItem(groupId, data) {
  return request.post(`/groups/${groupId}/items`, data);
}

/** 更新分组项（priority） */
export function updateGroupItem(groupId, itemId, data) {
  return request.put(`/groups/${groupId}/items/${itemId}`, data);
}

/** 从分组移除模型 */
export function deleteGroupItem(groupId, itemId) {
  return request.delete(`/groups/${groupId}/items/${itemId}`);
}
