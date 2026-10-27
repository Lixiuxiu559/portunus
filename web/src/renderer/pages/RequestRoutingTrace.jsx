import { useEffect, useRef, useState } from 'react';
import { Button, Spinner } from '@heroui/react';
import { getRoutingTrace } from '../api';
import { createAttemptLoader } from '../utils/requestAttempts';
import { logChannelLabel } from '../utils/logDetails';
import { formatDateTime } from '../utils/dateTime';
import { routingCoverage, routingEventDetails, routingFinalCandidate, routingLabel } from '../utils/routingTrace';

export default function RequestRoutingTrace({ requestId, channels }) {
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState({ key: '', status: 'loading', trace: null });
  const loader = useRef(null);
  if (!loader.current) loader.current = createAttemptLoader(getRoutingTrace);
  const key = JSON.stringify([requestId, retry]);
  useEffect(() => {
    if (!requestId) return undefined;
    const current = loader.current;
    setState({ key, status: 'loading', trace: null });
    current.load({ request_id: requestId }).then((result) => {
      if (result) setState({ key, status: 'ready', trace: result.data ?? null });
    }).catch(() => setState({ key, status: 'error', trace: null }));
    return () => current.invalidate();
  }, [requestId, key]);
  const trace = state.trace;
  const loading = state.key !== key || state.status === 'loading';
  const refresh = <Button size="sm" variant="secondary" onPress={() => setRetry((value) => value + 1)}>刷新切换诊断</Button>;
  return <section className="request-attempt-section" aria-label="网关切换诊断">
    <div className="request-attempt-heading"><h3 className="font-medium">网关切换诊断</h3>{requestId && refresh}</div>
    {!requestId ? <p className="text-sm text-muted">未记录请求 ID，无法查询切换诊断。</p>
      : loading ? <div className="request-attempt-loading" role="status"><Spinner size="sm" />加载切换诊断…</div>
        : state.status === 'error' ? <div role="alert"><p>切换诊断加载失败</p><Button size="sm" variant="secondary" onPress={() => setRetry((value) => value + 1)}>重试诊断查询</Button></div>
          : <>
            <p className="text-xs text-muted mb-3" role="status">{routingCoverage(trace)}</p>
            {trace && <>
              <dl className="log-detail-fields">
                <dt>路由策略 / 候选</dt><dd>{routingLabel('strategy', trace.strategy)} / {trace.candidate_count ?? '未记录'} 个</dd>
                <dt>最后选中候选</dt><dd>{routingFinalCandidate(trace, channels)}</dd>
                <dt>网关结束结果</dt><dd>{routingLabel('outcome', trace.outcome)}</dd>
                <dt>最终停止原因</dt><dd>{routingLabel('stop', trace.stop_reason)}{trace.stop_err_kind && ` · ${routingLabel('reason', trace.stop_err_kind)}（${trace.stop_err_kind}）`}</dd>
                <dt>网关状态 / 交付</dt><dd>{trace.status > 0 ? trace.status : '未记录状态'} / {trace.delivery_started === true ? '已开始交付' : trace.delivery_started === false ? '未开始交付' : '未记录'}</dd>
                <dt>显式 HTTP 尝试</dt><dd>{trace.http_attempts ?? '未记录'} 次</dd>
                <dt>开始 / 结束</dt><dd>{formatDateTime(trace.started_at)} / {formatDateTime(trace.finished_at)}</dd>
              </dl>
              {trace.schema_version !== 1 && <p className="text-xs text-muted mt-3">诊断版本 {String(trace.schema_version ?? '未记录')}，部分事件可能尚未识别，保留原值展示。</p>}
              <ol className="request-attempt-list request-routing-events">{(Array.isArray(trace.events) ? trace.events : []).map((event, index) => <li key={`${event.sequence}:${index}`} className="request-attempt-row">
                <div className="request-attempt-heading"><strong>#{event.sequence} · {routingLabel('event', event.event)}</strong><span>{routingLabel('reason', event.reason)}</span></div>
                <p className="text-sm">候选 #{event.candidate || '未记录'} · {logChannelLabel(event.channel_id, channels)} · 模型 ID {event.model_id || '未记录'}</p>
                <p className="text-xs text-muted">{routingEventDetails(event)}</p>
              </li>)}</ol>
              <p className="text-xs text-muted mt-3">诊断仅描述网关路由结束时的观察，不作为成功率或传输层实际发送次数统计。选中候选不表示已发起 HTTP 尝试或调用成功。交付表示开始尝试写入模型响应，不包含前置拒绝的错误信封，也不证明客户端收到响应。探测忙时的重试建议不是租约到期时间。</p>
            </>}
          </>}
  </section>;
}
