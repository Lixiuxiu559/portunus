import { isSupportedGroupStrategy } from './groupService.js';

/** 分组选择保留完整模型快照，翻页、搜索与切渠道不丢失已选成员。 */
export function toggleGroupModel(selected, model) {
  const id = Number(model.id);
  return selected.some((item) => Number(item.id) === id)
    ? selected.filter((item) => Number(item.id) !== id)
    : [...selected, model];
}

export function moveGroupModel(selected, modelId, offset) {
  const index = selected.findIndex((item) => Number(item.id) === Number(modelId));
  const target = index + offset;
  if (index < 0 || target < 0 || target >= selected.length) return selected;
  const next = [...selected];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

export function groupCreatePayload({ name, strategy, selected, activeModelId }) {
  const ids = selected.map((model) => Number(model.id));
  if (!name.trim() || !isSupportedGroupStrategy(strategy)
    || ids.length === 0 || ids.some((id) => !Number.isSafeInteger(id) || id <= 0)
    || new Set(ids).size !== ids.length) throw new Error('请填写分组名称并选择有效成员');
  const result = { name: name.trim(), strategy, items: ids.map((model_id, priority) => ({ model_id, priority })) };
  if (strategy === 'manual') {
    if (!ids.includes(Number(activeModelId))) throw new Error('请选择当前手动成员');
    result.active_model_id = Number(activeModelId);
  }
  return result;
}

/** 新查询或弹窗关闭会使旧结果失效；旧请求失败也不能覆盖新查询状态。 */
export function createModelPageLoader(fetchModels) {
  let version = 0;
  return {
    invalidate() { version += 1; },
    async load(params) {
      const current = ++version;
      try {
        const response = await fetchModels(params);
        if (current !== version) return null;
        return { models: Array.isArray(response?.data) ? response.data : [], total: Number(response?.total) || 0 };
      } catch (error) {
        if (current !== version) return null;
        throw error;
      }
    },
  };
}
