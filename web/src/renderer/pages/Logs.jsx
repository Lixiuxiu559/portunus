import { useEffect, useState } from 'react';
import { Typography, Spinner, Button } from '@heroui/react';
import { Search, RotateCw } from 'lucide-react';
import { listLogs, getLogStats } from '../api';

export default function Logs() {
  const [logs, setLogs] = useState({ total: 0, data: [] });
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const pageSize = 20;

  const [filters, setFilters] = useState({
    api_key_id: '',
    channel_id: '',
    model_name: '',
    success: '',
    start_time: '',
    end_time: '',
  });

  const fetchLogs = async () => {
    setLoading(true);
    try {
      const params = { page, page_size: pageSize };
      Object.entries(filters).forEach(([k, v]) => {
        if (v !== '' && v !== undefined) params[k] = v;
      });
      const [l, s] = await Promise.all([listLogs(params), getLogStats(params)]);
      setLogs(l || { total: 0, data: [] });
      setStats(s);
    } catch {
      setLogs({ total: 0, data: [] });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchLogs(); }, [page]);

  const totalPages = Math.max(1, Math.ceil((logs.total || 0) / pageSize));

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">调用日志</Typography>
        <Button variant="ghost" size="sm" onPress={fetchLogs} isLoading={loading}>
          <RotateCw className="size-4" />
        </Button>
      </div>

      {/* 统计卡片 */}
      {stats && (
        <div className="grid grid-cols-2 lg:grid-cols-5 gap-3 mb-4">
          <StatCard label="总费用" value={`$${Number(stats.total_cost || 0).toFixed(4)}`} />
          <StatCard label="总请求" value={String(stats.total_requests || 0)} />
          <StatCard label="输入 Token" value={formatNum(stats.input_tokens)} />
          <StatCard label="输出 Token" value={formatNum(stats.output_tokens)} />
          <StatCard label="近期请求" value={String(stats.recent_requests || 0)} />
        </div>
      )}

      {/* 筛选栏 */}
      <div className="flex flex-wrap gap-2 mb-4">
        <input
          className="w-40 rounded-lg border border-separator bg-background px-3 py-2 text-sm"
          placeholder="模型名"
          value={filters.model_name}
          onChange={(e) => setFilters((f) => ({ ...f, model_name: e.target.value }))}
        />
        <select
          className="rounded-lg border border-separator bg-background px-3 py-2 text-sm"
          value={filters.success}
          onChange={(e) => setFilters((f) => ({ ...f, success: e.target.value === 'all' ? '' : e.target.value }))}
        >
          <option value="all">全部</option>
          <option value="true">成功</option>
          <option value="false">失败</option>
        </select>
        <input
          className="w-44 rounded-lg border border-separator bg-background px-3 py-2 text-sm"
          type="datetime-local"
          value={filters.start_time}
          onChange={(e) => setFilters((f) => ({ ...f, start_time: e.target.value }))}
        />
        <input
          className="w-44 rounded-lg border border-separator bg-background px-3 py-2 text-sm"
          type="datetime-local"
          value={filters.end_time}
          onChange={(e) => setFilters((f) => ({ ...f, end_time: e.target.value }))}
        />
        <Button size="sm" variant="primary" onPress={() => { setPage(1); fetchLogs(); }}>
          <Search className="size-4" /> 查询
        </Button>
      </div>

      {/* 日志表格 */}
      {loading ? (
        <div className="flex justify-center py-12"><Spinner /></div>
      ) : !logs?.data?.length ? (
        <div className="flex justify-center py-16 text-muted">
          <Typography>暂无日志</Typography>
        </div>
      ) : (
        <>
          <div className="rounded-xl border border-separator overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-separator bg-accent/5">
                  <th className="text-left px-4 py-3 font-medium">时间</th>
                  <th className="text-left px-4 py-3 font-medium">模型</th>
                  <th className="text-left px-4 py-3 font-medium">分组</th>
                  <th className="text-right px-4 py-3 font-medium">输入 Tokens</th>
                  <th className="text-right px-4 py-3 font-medium">输出 Tokens</th>
                  <th className="text-right px-4 py-3 font-medium">费用</th>
                  <th className="text-center px-4 py-3 font-medium">状态</th>
                </tr>
              </thead>
              <tbody>
                {logs.data.map((l, i) => (
                  <tr key={l.id || i} className="border-b border-separator/50 hover:bg-accent/5">
                    <td className="px-4 py-3 text-muted font-mono text-xs">
                      {l.created_at ? new Date(l.created_at).toLocaleString('zh-CN') : '—'}
                    </td>
                    <td className="px-4 py-3">{l.model_name || '—'}</td>
                    <td className="px-4 py-3">{l.group_name || '—'}</td>
                    <td className="px-4 py-3 text-right font-mono">{formatNum(l.input_token)}</td>
                    <td className="px-4 py-3 text-right font-mono">{formatNum(l.output_token)}</td>
                    <td className="px-4 py-3 text-right font-mono">${Number(l.cost || 0).toFixed(6)}</td>
                    <td className="px-4 py-3 text-center">
                      <span className={`rounded-full px-2 py-0.5 text-xs ${l.success ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
                        {l.success ? '成功' : '失败'}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* 分页 */}
          <div className="flex items-center justify-between mt-3">
            <Typography type="body-sm" className="text-muted">
              共 {logs.total} 条，第 {page}/{totalPages} 页
            </Typography>
            <div className="flex gap-1">
              <Button size="sm" variant="ghost" disabled={page <= 1} onPress={() => setPage((p) => p - 1)}>上一页</Button>
              <Button size="sm" variant="ghost" disabled={page >= totalPages} onPress={() => setPage((p) => p + 1)}>下一页</Button>
            </div>
          </div>
        </>
      )}
    </div>
  );
}

function StatCard({ label, value }) {
  return (
    <div className="rounded-xl border border-separator p-4">
      <Typography type="body-sm" className="text-muted">{label}</Typography>
      <Typography type="body" className="font-semibold mt-1">{value}</Typography>
    </div>
  );
}

function formatNum(n) {
  if (n == null) return '—';
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(n);
}