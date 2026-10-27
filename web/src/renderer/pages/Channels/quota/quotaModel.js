import { cloneConfig, customEditors, customPayload, defaultCustom } from './customQuotaModel.js';
import { formatDateTime, formatRelativeTime } from '../../../utils/dateTime.js';

export function quotaDraft(source, adapter) {
  const options = cloneConfig(source?.options || (adapter?.id === 'custom.balance' ? { custom: defaultCustom() } : {}));
  return {
    name: source?.name ?? adapter?.name ?? '', adapter_id: source?.adapter_id ?? adapter?.id ?? '',
    enabled: source?.enabled ?? false, query_base_url: source?.query_base_url ?? adapter?.default_base_url ?? '',
    credential_mode: source?.credential_mode ?? adapter?.credential_modes?.[0] ?? 'inherit_channel_key',
    options, customEditors: options.custom ? customEditors(options.custom) : undefined, auto_refresh_minutes: source?.auto_refresh_minutes ?? 0,
    token: '', clearToken: false,
  };
}

export function switchQuotaAdapter(draft, previous, adapter) {
  const name = !draft.name.trim() || draft.name === previous?.name ? adapter.name : draft.name;
  return { ...quotaDraft(null, adapter), name };
}

export function quotaPayload(draft, source, query = false) {
  const options = cloneConfig(draft.options);
  if (draft.adapter_id === 'custom.balance') options.custom = customPayload(options.custom, draft.customEditors);
  const payload = {
    name: draft.name.trim(), adapter_id: draft.adapter_id, schema_version: 1,
    enabled: query || draft.enabled, query_base_url: draft.query_base_url.trim(),
    credential_mode: draft.credential_mode, options: Object.fromEntries(Object.entries(options).filter(([, value]) => value !== '')),
    auto_refresh_minutes: Number(draft.auto_refresh_minutes),
    ...(source ? { expected_revision: source.config_revision } : {}),
  };
  if (draft.credential_mode === 'dedicated') {
    if (draft.clearToken) payload.credentials = { clear: true };
    else if (draft.token.trim()) payload.credentials = { token: draft.token.trim() };
  }
  return payload;
}

export const quotaStatus = {
  disabled: '查询已关闭', never: '尚未查询', fresh: '查询成功', stale: '上次查询值',
  unavailable: '查询暂不可用', expired: '更新失败', invalidated: '配置已变更，需重新查询',
};
export const visibleSnapshot = (view) => ['fresh', 'stale'].includes(view?.status) ? view.snapshot : null;
export function quotaValue(item) {
  if (item.limit_state === 'unlimited') return '无限';
  if (item.remaining !== undefined && item.remaining !== null) return `${item.remaining}${item.unit?.code ? ` ${item.unit.code}` : ''}`;
  return '未知';
}
export function formatQuotaTime(value) {
  if (!value) return '尚无记录';
  return formatDateTime(value, '时间未知');
}

// 只表达上次读取的时间，不把缓存新鲜度说成上游余额过期。
export function formatQuotaRelativeTime(value, now = Date.now()) {
  return formatRelativeTime(value, now, '尚未查询');
}

// 每轮请求、草稿修改与关闭都递增代次，取消未必能阻止已到达的响应。
export function createRequestGate() {
  let generation = 0;
  let controller;
  return {
    cancel() { generation += 1; controller?.abort(); },
    start() {
      generation += 1; controller?.abort(); controller = new AbortController();
      const current = generation;
      return { signal: controller.signal, current: () => current === generation && !controller.signal.aborted };
    },
  };
}

// 只读缓存轮询；失败后继续调度，隐藏/卸载后不会被迟到响应重新启动。
export function createCachePoller({ load, visible, interval, cancel, timers = globalThis }) {
  let active = true;
  let timer;
  const schedule = () => {
    timers.clearTimeout(timer);
    if (!active || !visible()) return;
    timer = timers.setTimeout(run, interval());
  };
  const run = async () => {
    if (!active || !visible()) return;
    try { await load(); } catch { /* 读取函数负责显示错误，下一轮仍读取缓存。 */ }
    finally { schedule(); }
  };
  return {
    start: schedule,
    visibilityChanged() {
      timers.clearTimeout(timer);
      if (visible()) run(); else cancel();
    },
    stop() { active = false; timers.clearTimeout(timer); },
  };
}

const quotaRoot = (value = '') => value.trim().replace(/\/+$/, '');
export const canKeepQuotaToken = (source, draft) => Boolean(source?.has_token && source.adapter_id === draft.adapter_id && (draft.adapter_id !== 'custom.balance' || quotaRoot(source.query_base_url) === quotaRoot(draft.query_base_url)));
