import { formatQuotaRelativeTime, formatQuotaTime } from './quotaModel';

// 所有查询来源共用这一份金额、单位和查询时间布局。
export default function QuotaCardBalance({ rows, lastSuccessAt }) {
  return <div className="quota-compact-body">
    <div className="quota-compact-values">
      {rows.map((row) => <div className="quota-compact-amount" key={row.id}>
        {row.label && <span>{row.label}</span>}
        <strong>{row.value}{row.unit && <small>{row.unit}</small>}</strong>
      </div>)}
    </div>
    {lastSuccessAt && <time className="quota-compact-time" dateTime={lastSuccessAt} title={`上次查询成功：${formatQuotaTime(lastSuccessAt)}`}>{formatQuotaRelativeTime(lastSuccessAt)}</time>}
  </div>;
}
