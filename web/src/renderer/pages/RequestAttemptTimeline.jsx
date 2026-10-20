import { useEffect, useRef, useState } from 'react';
import { Button, Spinner } from '@heroui/react';
import { listUpstreamAttempts } from '../api';
import { attemptCoverage, attemptReasonLabels, createAttemptLoader } from '../utils/requestAttempts';
import { copyLogText, logChannelLabel, logErrorSummary } from '../utils/logDetails';
import { toast } from '@heroui/react';

const PAGE_SIZE = 20;

export default function RequestAttemptTimeline({ requestId, channels }) {
  const [page, setPage] = useState(1);
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState({ key: '', status: 'loading', rows: [], total: 0 });
  const loader = useRef(null);
  if (!loader.current) loader.current = createAttemptLoader(listUpstreamAttempts);
  const key = JSON.stringify([requestId, page, retry]);
  useEffect(() => {
    if (!requestId) return undefined;
    const current = loader.current;
    setState({ key, status: 'loading', rows: [], total: 0 });
    current.load({ request_id: requestId, page, page_size: PAGE_SIZE }).then((result) => {
      if (result) setState({ key, status: 'ready', rows: Array.isArray(result.data) ? result.data : [], total: Number(result.total) || 0 });
    }).catch(() => setState({ key, status: 'error', rows: [], total: 0 }));
    return () => current.invalidate();
  }, [requestId, page, key]);
  const copyError = async (row) => {
    try { await copyLogText(logErrorSummary(row), navigator.clipboard); toast.success('已复制'); }
    catch { toast.danger('复制失败，请手动选择文本复制'); }
  };
  const loading = state.key !== key || state.status === 'loading';
  return <section className="request-attempt-section" aria-label="上游尝试链">
    <div className="request-attempt-heading"><h3 className="font-medium">上游尝试链</h3>{requestId && <Button size="sm" variant="secondary" onPress={() => setRetry((value) => value + 1)}>刷新尝试记录</Button>}</div>
    {!requestId ? <p className="text-sm text-muted">未记录请求 ID，保留为独立历史日志，无法关联尝试链。</p>
      : loading ? <div className="request-attempt-loading" role="status"><Spinner size="sm" />加载上游尝试…</div>
        : state.status === 'error' ? <div role="alert"><p>尝试记录加载失败</p><Button size="sm" variant="secondary" onPress={() => setRetry((value) => value + 1)}>重试尝试查询</Button></div>
          : <>
            <p className="text-xs text-muted mb-3" role="status">已采集 {state.total} 条显式 HTTP 尝试。{attemptCoverage(state.rows, page, PAGE_SIZE)}</p>
            <ol className="request-attempt-list">{state.rows.map((row) => <li key={row.id} className="request-attempt-row">
              <div className="request-attempt-heading"><strong>#{row.sequence} · {attemptReasonLabels[row.reason] || row.reason || '原因未记录'}</strong><span>{row.success ? '处理成功' : '处理失败'} · HTTP {row.status || '未收到响应'}</span></div>
              <p className="text-sm">{logChannelLabel(row.channel_id, channels)} · {row.model_name || '未记录模型'}</p>
              <p className="text-xs text-muted">{row.client_protocol || '未记录入口协议'} → {row.upstream_protocol || '未记录上游协议'} · {row.stream ? '流式' : '非流式'} · {row.duration_ms} ms · relay #{row.relay_attempt}</p>
              <p className="text-xs text-muted">预算字段：{row.budget_field || '未识别或未记录'} · {row.started_at ? new Date(row.started_at).toLocaleString('zh-CN') : '时间未记录'}</p>
              {(row.err_kind || row.err_msg) && <details><summary className="text-sm">查看尝试错误</summary><pre>{logErrorSummary(row)}</pre><Button size="sm" variant="secondary" onPress={() => copyError(row)}>复制第 {row.sequence} 次错误</Button></details>}
            </li>)}</ol>
            {state.total > 0 && <div className="request-attempt-pagination"><span className="text-xs text-muted">尝试第 {page}/{Math.max(1, Math.ceil(state.total / PAGE_SIZE))} 页</span><div className="flex gap-2"><Button size="sm" variant="secondary" aria-label="尝试上一页" isDisabled={page <= 1} onPress={() => setPage((value) => value - 1)}>上一页</Button><Button size="sm" variant="secondary" aria-label="尝试下一页" isDisabled={page * PAGE_SIZE >= state.total} onPress={() => setPage((value) => value + 1)}>下一页</Button></div></div>}
            <p className="text-xs text-muted mt-3">处理成功不等于模型正常结束或整个请求最终成功。连接失败也会记录尝试；不包含传输层内部重发、重定向或尚未落库的调用。</p>
          </>}
  </section>;
}
