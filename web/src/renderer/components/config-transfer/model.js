export const PAGE_SIZE = 20;
export const ZERO_COUNTS = Object.freeze({ channels: 0, models: 0, groups: 0 });
export const isTerminal = (result) => result?.state === 'committed' || result?.state === 'aborted';
export const isTransferBusy = (stage) => ['selecting', 'exporting', 'decrypting', 'committing', 'checking'].includes(stage);

export function passwordError(value, maximum = 1024) {
  if (!value) return '请输入文件密码';
  // 不修剪空格、不归一化；未配对代理项不能被 TextEncoder 隐式替换。
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(++i);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return '密码包含无效字符';
    } else if (code >= 0xdc00 && code <= 0xdfff) return '密码包含无效字符';
  }
  return new TextEncoder().encode(value).length > maximum ? `密码不能超过 ${maximum} 字节` : '';
}

export function pageOf(items = [], requested = 1, size = PAGE_SIZE) {
  const pages = Math.max(1, Math.ceil(items.length / size));
  const page = Math.min(pages, Math.max(1, requested));
  return { items: items.slice((page - 1) * size, page * size), page, pages, total: items.length };
}

export function renamedItems(data) {
  return [...(data?.channels || []).map((item) => ({ ...item, kind: '渠道' })),
    ...(data?.groups || []).map((item) => ({ ...item, kind: '分组' }))]
    .filter((item) => item.original_name !== item.name);
}

export function previewRelations(preview) {
  const channels = new Map((preview?.channels || []).map((item) => [item.ref, item]));
  const models = new Map((preview?.models || []).map((item) => [item.ref, item]));
  return { channelName: (ref) => channels.get(ref)?.name || '渠道引用缺失',
    member: (item, group) => {
      const model = models.get(item.model_ref);
      return { ...item, modelName: model?.name || '模型引用缺失', channelName: channels.get(model?.channel_ref)?.name || '渠道引用缺失',
        active: item.ref === group.active_item_ref };
    } };
}

// 导航 state 中的 ID 保持十进制字符串；不把整次结果塞进 URL。
export function importedGroupIds(state) {
  if (!Array.isArray(state?.importedGroupIds)) return null;
  return new Set(state.importedGroupIds.filter((id) => typeof id === 'string' && /^[1-9]\d*$/.test(id)));
}
export function filterImportedGroups(groups, ids) {
  return ids ? groups.filter((group) => ids.has(String(group.id))) : groups;
}
