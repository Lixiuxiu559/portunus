// 渠道草稿规则独立于 React 和网络，创建与编辑复用。
export function createDraft(channel) {
  return {
    name: channel?.name || '', type: channel?.type || '',
    provider_id: channel?.provider_id || 'generic', profile_id: channel?.profile_id || '',
    adapter_id: channel?.adapter_id || 'generic', base_url: channel?.base_url || '', key: '',
    connection_options: {
      ...channel?.connection_options,
      // 历史行的零值表示既有 API 发现语义，不能覆盖成空模式并关闭自动同步。
      schema_version: channel?.connection_options?.schema_version || 1,
      model_discovery: channel?.connection_options?.model_discovery || 'api',
    },
    auto_sync: channel?.auto_sync ?? false, thinking_compat: channel?.thinking_compat ?? true,
    preset_base_url: '',
  };
}

export function applyProfile(draft, preset, profile, replaceAddress = false) {
  const replace = replaceAddress || !draft.base_url.trim() || draft.base_url === draft.preset_base_url;
  const discovery = profile.model_discovery || 'api';
  return {
    ...draft, provider_id: preset.id, profile_id: profile.id, type: profile.type,
    adapter_id: profile.adapter_id, base_url: replace ? profile.base_url : draft.base_url,
    preset_base_url: profile.base_url,
    connection_options: { schema_version: 1, model_discovery: discovery },
    auto_sync: discovery === 'manual' ? false : draft.auto_sync,
  };
}

export function connectionIdentity(draft) {
  return JSON.stringify([draft.type, draft.provider_id, draft.profile_id, draft.adapter_id,
    draft.base_url, draft.key, draft.connection_options, draft.thinking_compat]);
}

export function normalizeModels(names) {
  return [...new Set(names.map((name) => name.trim()).filter(Boolean))];
}

export function connectionPayload(draft, channelId) {
  const payload = {
    type: draft.type, provider_id: draft.provider_id, profile_id: draft.profile_id,
    adapter_id: draft.adapter_id, base_url: draft.base_url.trim(),
    connection_options: draft.connection_options, thinking_compat: draft.thinking_compat,
  };
  if (draft.key.trim()) payload.key = draft.key.trim();
  if (channelId) payload.channel_id = channelId;
  return payload;
}

export function savePayload(draft, selected) {
  return {
    ...connectionPayload(draft), name: draft.name.trim(), models: normalizeModels(selected),
    auto_sync: draft.connection_options.model_discovery === 'api' && draft.auto_sync,
  };
}

// generation 在同步修改草稿时递增，而非等待 effect 才使旧请求失效。
export function isCurrentRequest(request, generation, latest) {
  return request.generation === generation && request.id === latest;
}
