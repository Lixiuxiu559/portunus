export const strategyOptions = [
  { id: 'manual', label: '手动选择' },
  { id: 'failover', label: '故障转移' },
];

export const strategyLabels = Object.fromEntries(strategyOptions.map(({ id, label }) => [id, label]));

export function isSupportedGroupStrategy(strategy) {
  return strategyOptions.some(({ id }) => id === strategy);
}

// 配置状态只描述本地分组，不代表上游在线或已经验证能力。
export function groupConfiguration(group) {
  if (!isSupportedGroupStrategy(group.strategy)) return { complete: false, label: '配置不完整：请选择有效路由策略' };
  const items = group.items || [];
  if (!items.length) return { complete: false, label: '配置不完整：没有成员' };
  if (group.strategy === 'manual' && !items.some((item) => item.id === group.active_item_id)) {
    return { complete: false, label: '配置不完整：请选择当前模型' };
  }
  return { complete: true, label: '已配置 · 调用状态未验证' };
}

export function groupServicePath(path, name) {
  return `${path}?${new URLSearchParams({ group: name })}`;
}

export const strategyDescriptions = {
  manual: '只调用选定的当前模型；可点击成员切换。',
  failover: '从上到下按优先级尝试；可切换的失败会尝试后续成员。',
};
