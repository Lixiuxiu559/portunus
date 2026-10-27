import { formatCompactQuotaAmount } from './quotaAmount.js';
import { newAPIAccountOverview } from './newAPIQuotaModel.js';

// 后端已归一化普通厂商响应；卡片适配只选择展示项，不计算余额或合计。
function standardCard(snapshot) {
  const items = snapshot?.items || [];
  return {
    heading: items.length ? items.every((item) => item.kind === 'balance' && item.unit?.kind === 'money') ? '账户余额' : '可用额度' : '余额查询',
    rows: items.slice(0, 2).map((item) => ({
      id: item.id, label: items.length > 1 ? item.label : undefined,
      value: item.limit_state === 'unlimited' ? '不限总额度' : formatCompactQuotaAmount(item.remaining, item.unit),
      unit: item.remaining != null && item.limit_state !== 'unlimited' ? item.unit?.code : undefined,
    })),
    moreCount: items.length > 2 ? items.length : 0,
  };
}

const cardAdapters = {
  'newapi.account': (snapshot, now) => {
    const overview = newAPIAccountOverview(snapshot, now);
    return { heading: '账户余额', rows: [{ id: 'account_total', value: overview.value, unit: overview.unit }], notice: overview.notice, moreCount: 0 };
  },
};

export function quotaCardPresentation(adapterID, snapshot, now = Date.now()) {
  if (!snapshot) return standardCard(null);
  return (cardAdapters[adapterID] || standardCard)(snapshot, now);
}
