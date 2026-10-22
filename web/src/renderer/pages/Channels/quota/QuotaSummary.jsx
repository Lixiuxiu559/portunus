import { RefreshCw, Settings2, Wallet } from 'lucide-react';
import QuotaItems from './QuotaItems';
import { quotaStatus, visibleSnapshot, formatQuotaTime, formatQuotaRelativeTime, quotaValue } from './quotaModel';

export function QuotaSourceState({ view, details = false }) {
  const snapshot = visibleSnapshot(view);
  return <div className="flex flex-col gap-2">
    <div className="text-xs text-muted" role="status">
      {view?.in_progress ? '正在更新…' : quotaStatus[view?.status] || '状态未知'}
      {view?.last_success_at && <span title={formatQuotaTime(view.last_success_at)}> · 上次查询 {formatQuotaRelativeTime(view.last_success_at)}</span>}
    </div>
    {snapshot && <QuotaItems snapshot={snapshot} details={details} />}
    {view?.failure && view.failure.category !== 'cancelled' && <p role="alert" className="text-xs text-danger">{snapshot ? '本次更新失败：' : ''}{view.failure.message}</p>}
    {details && view?.next_allowed_at && <p className="text-xs text-muted">可再次查询：{formatQuotaTime(view.next_allowed_at)}</p>}
    {details && !snapshot && view?.last_snapshot && <details className="text-xs text-muted">
      <summary>查看历史查询记录（不代表当前可用额度）</summary><QuotaItems snapshot={view.last_snapshot} details />
    </details>}
  </div>;
}

function failureLabel(failure) {
  return { authentication: '凭据无效', permission: '无查询权限', configuration: '请检查查询设置', rate_limited: '查询过于频繁' }[failure?.category] || '更新失败';
}

function CompactSource({ source, onConfigure, onRefresh, pending, refreshError }) {
  const view = source.state;
  const snapshot = visibleSnapshot(view);
  const items = snapshot?.items || [];
  const failure = refreshError || (view?.failure?.category === 'cancelled' ? null : view?.failure);
  const busy = Boolean(pending || view?.in_progress);
  const cooling = view?.next_allowed_at && new Date(view.next_allowed_at).getTime() > Date.now();
  const refreshTitle = !source.enabled ? '先在余额查询设置中启用此来源' : cooling ? `可再次查询：${formatQuotaTime(view.next_allowed_at)}` : busy ? '正在更新余额' : '刷新余额';
  const summary = source.enabled ? quotaStatus[view?.status] || '尚未查询' : '查询已关闭';
  const heading = items.length ? items.every((item) => item.kind === 'balance' && item.unit?.kind === 'money') ? '账户余额' : '可用额度' : '余额查询';
  return <section className="quota-compact-source" aria-label={source.name}>
    <div className="quota-compact-heading">
      <span className="quota-compact-name" title={source.name}>{heading}</span>
      <div className="quota-compact-actions">
        <button type="button" className="quota-icon-button" aria-label={`刷新 ${source.name}`} title={refreshTitle}
          disabled={!source.enabled || busy || Boolean(cooling)} onClick={() => { onRefresh?.(source)?.catch(() => {}); }}>
          <RefreshCw size={13} className={busy ? 'quota-refresh-spinning' : ''} aria-hidden="true" />
        </button>
        <button type="button" className="quota-icon-button" title="余额查询设置" aria-label="余额查询设置" onClick={onConfigure}><Settings2 size={14} aria-hidden="true" /></button>
      </div>
    </div>
    {!!items.length && <div className="quota-compact-values">
      {items.slice(0, 2).map((item) => <div className="quota-compact-amount" key={item.id} title={`${item.label}：${quotaValue(item)}`}>
        {items.length > 1 && <span>{item.label}</span>}<strong>{quotaValue(item)}</strong>
      </div>)}
    </div>}
    <div className="quota-compact-meta" role="status">
      {failure ? <span className="quota-compact-failure" title={failure.message || String(failure)}>{failureLabel(failure)}</span> : !items.length && <span>{busy ? '正在查询…' : summary}</span>}
      {view?.last_success_at && <span title={`上次查询成功：${formatQuotaTime(view.last_success_at)}`}>{failure ? '上次查询 ' : ''}{formatQuotaRelativeTime(view.last_success_at)}</span>}
      {items.length > 2 && <button type="button" className="quota-more-items" onClick={onConfigure}>查看全部 {items.length} 项</button>}
    </div>
  </section>;
}

/**
 * 渠道卡片上的余额摘要：渠道级 1:1，只读取第一个（也是唯一）查询来源。
 * 接口仍以数组返回，便于与后端保持一致并兼容旧响应。
 */
export default function QuotaSummary({ sources = [], error, loading, onConfigure, onRefresh, pending = {}, refreshErrors = {} }) {
  const source = sources[0] || null;
  return <div className="quota-card-summary">
    {source && <CompactSource source={source}
      onConfigure={onConfigure} onRefresh={onRefresh} pending={pending[source.id]} refreshError={refreshErrors[source.id]} />}
    {error && <p role="alert" className="quota-summary-error">余额暂未载入</p>}
    {!source && <button type="button" className="quota-empty-link" onClick={onConfigure} disabled={loading}>
      <Wallet size={14} aria-hidden="true" /><span>{loading ? '正在读取余额…' : '查询余额'}</span><Settings2 size={13} aria-hidden="true" />
    </button>}
  </div>;
}
