import NewAPIQuota from './NewAPIQuota';
import { quotaValue, formatQuotaTime } from './quotaModel';

export default function QuotaItems({ snapshot, details = false }) {
  if (snapshot?.newapi) return <NewAPIQuota snapshot={snapshot} />;
  return <div className="quota-items">{(snapshot?.items || []).map((item) => <div key={item.id} className="quota-item">
    <div className="flex flex-wrap items-baseline justify-between gap-2">
      <span>{item.label}</span><strong className="font-medium tabular-nums">{quotaValue(item)}</strong>
    </div>
    <div className="flex flex-wrap gap-x-3 text-xs text-muted">
      <span>{{ account: '账户', key: 'API Key', api_key: 'API Key', subscription: '套餐', plan: '套餐' }[item.scope] || item.scope} · {{ balance: '余额', quota: '额度', window: '窗口', spending_limit: '消费限额', usage_window: '周期用量' }[item.kind] || item.kind}</span>
      {item.used != null && <span>已用 {item.used} {item.unit?.code}</span>}
      {item.total != null && <span>总量 {item.total} {item.unit?.code}</span>}
      {item.used_percent != null && <span>已用 {item.used_percent}%</span>}
      {item.resets_at && <span>重置于 {formatQuotaTime(item.resets_at)}</span>}
      {item.account_status && <span>账户状态：{item.account_status}</span>}
    </div>
    {details && item.original && <div className="text-xs text-muted mt-1">
      原始额度：{item.original.remaining != null ? `剩余 ${item.original.remaining} ` : ''}
      {item.original.used != null ? `已用 ${item.original.used} ` : ''}
      {item.original.total != null ? `总量 ${item.original.total} ` : ''}
      {item.original.unit?.code || '未知单位'}
    </div>}
  </div>)}</div>;
}
