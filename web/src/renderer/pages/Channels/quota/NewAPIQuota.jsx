import { Wallet, Layers3, CalendarClock } from 'lucide-react';
import { formatQuotaTime } from './quotaModel';
import { activeNewAPISubscriptions, formatNewAPIAmount, newAPIBillingLabels,
  newAPISectionMessage, newAPIUsagePercent, newAPIValue } from './newAPIQuotaModel';

function SectionNotice({ section, label }) {
  const message = newAPISectionMessage(section, label);
  return message ? <p className="newapi-query-notice" role="status" title={section?.failure?.message}>{message}</p> : null;
}

function SubscriptionCard({ item }) {
  const percent = newAPIUsagePercent(item.used_percent);
  const resetDue = item.resets_at && Date.parse(item.resets_at) <= Date.now();
  return <article className="newapi-subscription-card">
    <div className="newapi-subscription-heading"><div><h4>{item.label}</h4><span className="newapi-subscription-id">订阅 #{item.subscription_id || item.id.replace('subscription:', '')}</span></div><span className="newapi-active-tag">生效中</span></div>
    <div className="newapi-subscription-remaining"><span>本期剩余</span><strong>{newAPIValue(item)}{item.limit_state !== 'unlimited' && <small>{item.unit?.code}</small>}</strong></div>
    {percent !== null && item.limit_state !== 'unlimited' && <div className="newapi-usage"><div className="newapi-usage-track" aria-hidden="true"><div style={{ width: `${Math.min(100, Math.max(0, item.used_percent))}%` }} /></div><span>已用 {percent}</span></div>}
    <dl className="newapi-subscription-stats">
      {item.used != null && <div><dt>本期已用</dt><dd>{formatNewAPIAmount(item.used)} {item.unit?.code}</dd></div>}
      {item.total != null && item.limit_state !== 'unlimited' && <div><dt>本期总额</dt><dd>{formatNewAPIAmount(item.total)} {item.unit?.code}</dd></div>}
    </dl>
    <div className="newapi-subscription-dates">
      {item.resets_at && <p className={resetDue ? 'newapi-reset-due' : ''}><CalendarClock size={13} aria-hidden="true" /><span>{resetDue ? '重置时间已到，请刷新查询' : `下次重置 ${formatQuotaTime(item.resets_at)}`}</span></p>}
      {item.expires_at && <p><span>到期时间 {formatQuotaTime(item.expires_at)}</span></p>}
    </div>
    {item.original && <details className="newapi-original"><summary>原始额度</summary><p>剩余 {item.original.remaining == null ? '未知' : formatNewAPIAmount(item.original.remaining)} · 已用 {item.original.used == null ? '未知' : formatNewAPIAmount(item.original.used)} · 总量 {item.original.total == null ? '未知' : formatNewAPIAmount(item.original.total)} {item.original.unit?.code}</p></details>}
  </article>;
}

export default function NewAPIQuota({ snapshot }) {
  const info = snapshot.newapi;
  const wallet = (snapshot.items || []).find((item) => item.id === 'account_quota');
  const subscriptions = activeNewAPISubscriptions(snapshot);
  return <div className="newapi-account-overview">
    <div className="newapi-overview-header"><span>账户概况</span><span className="newapi-billing-tag">{newAPIBillingLabels[info.billing_preference] || '计费偏好未获取'}</span></div>
    <section className="newapi-wallet-panel" aria-label="钱包额度"><div className="newapi-wallet-title"><Wallet size={16} aria-hidden="true" /><span>钱包额度</span></div><strong>{newAPIValue(wallet)}{wallet?.unit?.code && <small>{wallet.unit.code}</small>}</strong>
      {wallet?.used != null && <p>账户累计消耗 {formatNewAPIAmount(wallet.used)} {wallet.unit?.code}</p>}
      <SectionNotice section={info.wallet} label="钱包" />
      {wallet?.original && <details className="newapi-original"><summary>原始额度</summary><p>剩余 {formatNewAPIAmount(wallet.original.remaining)} · 累计消耗 {formatNewAPIAmount(wallet.original.used)} {wallet.original.unit?.code}</p></details>}
    </section>
    <section aria-label="生效订阅" className="newapi-subscriptions-section">
      <div className="newapi-subscriptions-title"><h3><Layers3 size={16} aria-hidden="true" />生效订阅{info.subscriptions?.status === 'ok' && <span>{subscriptions.length}</span>}</h3><p>各份套餐独立展示，不与钱包额度合计</p></div>
      <SectionNotice section={info.subscriptions} label="订阅" />
      {info.subscriptions?.status === 'ok' && !subscriptions.length && <div className="newapi-no-subscriptions">当前没有生效中的订阅</div>}
      {!!subscriptions.length && <div className="newapi-subscription-grid">{subscriptions.map((item) => <SubscriptionCard key={item.id} item={item} />)}</div>}
      {!!subscriptions.length && info.plans && info.plans.status !== 'ok' && <p className="newapi-query-notice">套餐名称暂未获取，已按套餐编号展示；额度查询结果保留</p>}
    </section>
    <p className="newapi-observation-note">额度是上次查询结果，不代表调用一定成功；实际扣除顺序以站点计费规则为准。不限总额度也不代表无限制调用。</p>
  </div>;
}
