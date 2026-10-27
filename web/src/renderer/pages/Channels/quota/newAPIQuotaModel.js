import { formatQuotaAmount as formatNewAPIAmount, formatCompactQuotaAmount as formatNewAPICompactAmount } from './quotaAmount.js';
export { formatNewAPIAmount, formatNewAPICompactAmount };

export function newAPIValue(item, compact = false) {
  if (!item) return '未获取';
  if (item.limit_state === 'unlimited') return '不限总额度';
  if (item.remaining === undefined || item.remaining === null) return '未知';
  return compact ? formatNewAPICompactAmount(item.remaining, item.unit) : formatNewAPIAmount(item.remaining);
}

export const newAPIBillingLabels = {
  subscription_first: '订阅优先', wallet_first: '钱包优先',
  subscription_only: '仅用订阅', wallet_only: '仅用钱包',
};

export function activeNewAPISubscriptions(snapshot, now = Date.now()) {
  return (snapshot?.items || []).filter((item) => item.scope === 'plan' && item.account_status === 'active'
    && Number.isFinite(Date.parse(item.expires_at)) && Date.parse(item.expires_at) > now);
}

export function newAPISectionMessage(section, label) {
  if (section?.status === 'ok') return '';
  if (section?.status === 'unsupported') return `站点暂不支持${label}查询`;
  if (section?.failure?.category === 'permission') return `${label}查询权限不足`;
  if (section?.failure?.category === 'authentication') return `${label}查询凭据无效`;
  if (section?.failure?.category === 'rate_limited') return `${label}查询被限流`;
  return `${label}暂未获取`;
}

export function newAPIHasPartialFailure(snapshot) {
  const info = snapshot?.newapi;
  return Boolean(info && (info.wallet?.status !== 'ok' || info.subscriptions?.status !== 'ok'
    || (info.plans && info.plans.status !== 'ok')));
}

export function newAPIAccountOverview(snapshot, now = Date.now()) {
  const info = snapshot?.newapi;
  const total = info?.account_summary;
  if (!total) return { value: '待刷新', notice: '刷新后获取总额度' };
  if (total.expires_at && Date.parse(total.expires_at) <= now) {
    return { value: '需刷新', notice: '部分订阅已到期，请刷新查询' };
  }
  const incomplete = info.wallet?.status !== 'ok' || info.subscriptions?.status !== 'ok';
  return {
    value: total.limit_state === 'unlimited' ? '含不限额订阅' : newAPIValue(total, true),
    unit: total.limit_state === 'unlimited' ? undefined : total.unit?.code,
    notice: incomplete ? '部分额度未获取，合计不完整' : undefined,
  };
}

// 合计来自后端；旧缓存和到期后的合计不在浏览器中重新计算。
export function newAPISubscriptionOverview(snapshot, now = Date.now()) {
  const info = snapshot?.newapi;
  if (info?.subscriptions?.status !== 'ok') return { value: '未获取', count: null };
  const count = activeNewAPISubscriptions(snapshot, now).length;
  const summary = info.subscription_summary;
  if (!summary) return { value: '待刷新', count, notice: '刷新后获取订阅汇总' };
  if (summary.expires_at && Date.parse(summary.expires_at) <= now) {
    return { value: '需刷新', count, notice: '部分订阅已到期，请刷新查询' };
  }
  if (summary.unlimited_count > 0) return {
    value: summary.unlimited_count === summary.count ? '不限总额度' : '含不限额订阅', count: summary.count,
  };
  if (summary.remaining == null) return { value: '未知', count: summary.count };
  return { value: formatNewAPICompactAmount(summary.remaining, summary.unit), unit: summary.unit?.code, count: summary.count };
}

export function newAPINextReset(items, now = Date.now()) {
  const dates = items.map((item) => item.resets_at).filter((value) => Number.isFinite(Date.parse(value)) && Date.parse(value) > now);
  return dates.sort((a, b) => Date.parse(a) - Date.parse(b))[0] || null;
}

export function newAPIUsagePercent(value) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null;
  if (value > 99.99 && value < 100) return '>99.99%';
  return `${Math.max(0, value).toLocaleString('zh-CN', { maximumFractionDigits: 2 })}%`;
}
