import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Typography, Button, Card, TextField, Input, Select, ListBox, Label } from '@heroui/react';
import { Search, RotateCw } from 'lucide-react';
import DataTable from '../components/DataTable';
import { buildLogParams } from '../utils/logQuery';
import { listHistoricalAPIKeys } from '../api/apikey';
import { apiKeyLabel, logAPIKeyLabel } from '../utils/apiKeys';
import { listLogs, getLogStats, listChannels } from '../api';
import { errKindLabels, logChannelLabel } from '../utils/logDetails';
import LogDetailModal from './LogDetailModal';
import { getCachedResponse } from '../api/responseCache';
import './logs.css';

const successOptions = [
  { id: 'all', label: '全部' },
  { id: 'true', label: '成功' },
  { id: 'false', label: '失败' },
];

export default function Logs() {
  const [searchParams] = useSearchParams();
  const linkedGroup = searchParams.get('group') || '';
  const linkedKey = searchParams.get('api_key_id') || 'all';
  const linkedSelection = `${linkedGroup}/${linkedKey}`;
  const lastLinkedSelection = useRef(linkedSelection);
  const requestSequence = useRef(0);
  const [cached] = useState(() => {
    const params = buildLogParams({ group_name: linkedGroup, api_key_id: linkedKey, channel_id: 'all', success: 'all' }, 1, 20);
    const logs = getCachedResponse('/logs', params);
    const stats = getCachedResponse('/logs/stat', params);
    return logs !== undefined && stats !== undefined ? { logs, stats } : null;
  });
  const [logs, setLogs] = useState(cached?.logs ?? { total: 0, data: [] });
  const [stats, setStats] = useState(cached?.stats ?? null);
  const [channels, setChannels] = useState(() => getCachedResponse('/channels') ?? []);
  const [channelError, setChannelError] = useState(false);
  const [apiKeys, setApiKeys] = useState(() => getCachedResponse('/apikeys', { include_deleted: true }) ?? []);
  const [keyError, setKeyError] = useState(false);
  const fetchKeys = async () => {
    try { const keys = await listHistoricalAPIKeys(); setApiKeys(Array.isArray(keys) ? keys : []); setKeyError(false); }
    catch { setKeyError(true); }
  };
  useEffect(() => { fetchKeys(); }, []);
  const [detail, setDetail] = useState(null);
  const fetchChannels = async () => {
    try { const result = await listChannels(); setChannels(Array.isArray(result) ? result : []); setChannelError(false); }
    catch { setChannelError(true); }
  };
  useEffect(() => { fetchChannels(); }, []);
  const [loading, setLoading] = useState(!cached);
  const [refreshing, setRefreshing] = useState(false);
  // 首次成功后，即使结果为空，后续查询也保留内容而不退回首次加载态。
  const hasLoaded = useRef(!!cached);
  const [showRefreshing, setShowRefreshing] = useState(false);
  const [page, setPage] = useState(1);
  const pageSize = 20;
  // 首次加载失败标记：区分"真没数据"和"加载失败"，后者给重试入口
  const [loadError, setLoadError] = useState(false);

  const [filters, setFilters] = useState({
    group_name: linkedGroup,
    api_key_id: linkedKey,
    model_name: '',
    channel_id: 'all',
    success: 'all',
  });

  const [query, setQuery] = useState(filters);

  useEffect(() => {
    if (lastLinkedSelection.current === linkedSelection) return;
    lastLinkedSelection.current = linkedSelection;
    setFilters((f) => ({ ...f, group_name: linkedGroup, api_key_id: linkedKey }));
    setQuery((q) => ({ ...q, group_name: linkedGroup, api_key_id: linkedKey }));
    setPage(1);
  }, [linkedGroup, linkedKey, linkedSelection]);

  // targetPage 显式传页码，避免 setPage 与请求闭包页码不一致的双请求竞态
  const fetchLogs = async (targetPage = page) => {
    // 首次成功前保留 loading（包括失败重试）；之后仅显示延迟刷新反馈。
    const setRefreshState = hasLoaded.current ? setRefreshing : setLoading;
    setRefreshState(true);
    const sequence = ++requestSequence.current;
    try {
      // 时间参数必须是带时区偏移的 RFC3339（见 utils/logQuery.js 的契约说明）
      const params = buildLogParams(query, targetPage, pageSize);
      const [l, s] = await Promise.all([listLogs(params), getLogStats(params)]);
      if (sequence !== requestSequence.current) return;
      setLogs(l || { total: 0, data: [] });
      setStats(s);
      hasLoaded.current = true;
      setLoadError(false);
    } catch {
      // 失败且没有可展示的旧数据时进入错误态，提供重试入口。
      if (sequence === requestSequence.current && (logs.data?.length || 0) === 0) setLoadError(true);
    } finally {
      if (sequence === requestSequence.current) {
        setLoading(false); setRefreshing(false); setShowRefreshing(false);
      }
    }
  };

  // 连续刷新超过 200ms 才显示反馈；快速完成时不闪遮罩。
  useEffect(() => {
    if (!refreshing) { setShowRefreshing(false); return undefined; }
    const timer = setTimeout(() => setShowRefreshing(true), 200);
    return () => clearTimeout(timer);
  }, [refreshing]);

  // 离开页面后，未决请求不能再提交数据或加载状态。
  useEffect(() => () => { requestSequence.current += 1; }, []);

  useEffect(() => { fetchLogs(); }, [page, query]);

  const channelIds = [...new Set([...channels.map((item) => String(item.id)), ...(logs.data || []).map((item) => (item.latest_log || item).channel_id).filter((id) => id > 0).map(String), ...(filters.channel_id !== 'all' ? [filters.channel_id] : [])])];

  const keyIds = [...new Set([...apiKeys.map((key) => String(key.id)), ...(logs.data || []).map((row) => row.api_key_id).filter((id) => id > 0).map(String), ...(filters.api_key_id !== 'all' ? [filters.api_key_id] : [])])];

  const totalPages = Math.max(1, Math.ceil((logs.total || 0) / pageSize));

  const columns = [
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      isRowHeader: true,
      width: '108px',
      render: (val) => {
        if (!val) return <span className="block text-center text-xs text-muted">—</span>;
        const date = new Date(val);
        return (
          <span className="block whitespace-nowrap text-center font-mono text-xs">
            <span className="block text-muted">{date.toLocaleDateString('zh-CN')}</span>
            <span className="block">{date.toLocaleTimeString('zh-CN', { hour12: false })}</span>
          </span>
        );
      },
    },
    {
      title: '状态',
      dataIndex: 'success',
      key: 'success',
      width: '88px',
      render: (val, record) => (
        <span className="flex w-full flex-col items-center gap-0.5">
          <span className={`inline-block rounded-full px-2 py-0.5 text-xs ${
            val ? 'bg-success/15 text-success' : 'bg-danger/15 text-danger'
          }`}>
            {val ? '成功' : '失败'}
          </span>
          {!val && record?.err_kind && (
            <span
              className="block max-w-full truncate text-xs text-muted"
              title={record?.err_msg || record.err_kind}
            >
              {errKindLabels[record.err_kind] || record.err_kind}
            </span>
          )}
        </span>
      ),
    },
    {
      title: '实际模型 / 渠道',
      dataIndex: 'model_name',
      key: 'model_name',
      width: '216px',
      render: (val, record) => <span className="block text-center"><span className="block truncate" title={String(val || '')}>{val || '未记录模型'}</span><span className="block truncate text-xs text-muted" title={logChannelLabel(record.channel_id, channels)}>{logChannelLabel(record.channel_id, channels)}</span></span>,
    },
    { title: '详情', dataIndex: 'id', key: 'detail', width: '78px', render: (id, record) => <Button size="sm" variant="secondary" aria-label={`查看日志 ${id} 详情`} onPress={() => setDetail({ record })}>详情</Button> },
    {
      title: '分组',
      dataIndex: 'group_name',
      key: 'group_name',
      width: '150px',
      render: (val) => (val ? (
        <span className="block truncate text-center" title={String(val)}>{val}</span>
      ) : '—'),
    },
    { title: 'API Key', dataIndex: 'api_key_id', key: 'api_key_id', width: '150px', render: (id, record) => <span className="block truncate text-center" title={logAPIKeyLabel(record)}>{logAPIKeyLabel(record)}</span> },
    {
      title: '输入 Tokens',
      dataIndex: 'input_token',
      key: 'input_token',
      width: '112px',
      render: (val, record) => {
        const cacheRead = record?.cache_read_token || 0;
        const cacheWrite = record?.cache_write_token || 0;
        // 主数字展示总输入（未命中 + 缓存读 + 缓存写，还原上游 prompt_tokens），
        // 缓存命中明细放小字；input_token 为非缓存输入。
        const total = val == null ? null : val + cacheRead + cacheWrite;
        return (
          <span className="block text-center font-mono">
            {formatNum(total)}
            {cacheRead > 0 && (
              <span className="block text-xs text-muted">缓存读 {formatNum(cacheRead)}</span>
            )}
            {cacheWrite > 0 && (
              <span className="block text-xs text-muted">缓存写 {formatNum(cacheWrite)}</span>
            )}
          </span>
        );
      },
    },
    {
      title: '输出 Tokens',
      dataIndex: 'output_token',
      key: 'output_token',
      width: '96px',
      render: (val) => (
        <span className="text-center font-mono block">{formatNum(val)}</span>
      ),
    },
    {
      title: '耗时 / 首包',
      dataIndex: 'duration_ms',
      key: 'duration_ms',
      width: '168px',
      render: (val, record) => {
        // stream 字段由后端按客户端请求记录；旧数据（迁移前）回退按首包推断
        const isStream = record?.stream || record?.first_token_ms > 0;
        return (
          <span className="flex items-center justify-center gap-1">
            <span
              className="inline-block rounded-full px-2 py-0.5 text-xs font-mono bg-accent/15 text-accent"
              title={record?.request_id ? `req ${record.request_id}` : undefined}
            >
              {formatMs(val)}
            </span>
            {isStream && (
              <span className="inline-block rounded-full px-2 py-0.5 text-xs font-mono bg-default/40 text-muted">
                {formatMs(record.first_token_ms)}
              </span>
            )}
            <span className={`inline-block rounded-full px-2 py-0.5 text-xs ${isStream ? 'bg-success/15 text-success' : 'bg-default/40 text-muted'}`}>
              {isStream ? '流' : '非流'}
            </span>
          </span>
        );
      },
    },
  ];

  const hasActiveFilters = query.api_key_id !== 'all' || query.channel_id !== 'all' || Boolean(query.group_name) || Boolean(query.model_name) || query.success !== 'all';

  return (
    <div data-page-ready={!loading} className="logs-page flex flex-col flex-1 min-h-0">
      <h1 className="sr-only">调用记录</h1>
      {/* 统计按当前查询范围汇总，不把日志行数当作独立客户端请求数。 */}
      {stats ? (
        <div className="logs-stats">
          <StatCard label="记录数" value={String(stats.total_requests || 0)} />
          <StatCard label="非缓存输入 Token" value={formatNum(stats.input_tokens)} detail={`缓存读 ${formatNum(stats.cache_read_tokens)} · 缓存写 ${formatNum(stats.cache_write_tokens)}`} />
          <StatCard label="输出 Token" value={formatNum(stats.output_tokens)} />
          <StatCard label="近期记录" value={String(stats.recent_requests || 0)} />
        </div>
      ) : loading ? <div className="logs-stats" aria-hidden="true">{[0, 1, 2, 3].map((i) => <div key={i} className="skeleton-stat"><span className="skeleton-block skeleton-title" /><span className="skeleton-block skeleton-line" /></div>)}</div> : null}

      {/* 筛选条件与统计使用同一查询范围。 */}
      <div className="logs-filters">
        <Select aria-label="日志 API Key" selectedKey={filters.api_key_id} onSelectionChange={(value) => setFilters((current) => ({ ...current, api_key_id: value || 'all' }))} className="logs-filter-key">
          <Label>API Key</Label><Select.Trigger className="logs-control"><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover className="dialog-options"><ListBox>
            <ListBox.Item id="all" textValue="全部 API Key">全部 API Key<ListBox.ItemIndicator /></ListBox.Item>
            {keyIds.map((id) => <ListBox.Item key={id} id={id} textValue={apiKeyLabel(id, apiKeys)}>{apiKeyLabel(id, apiKeys)}<ListBox.ItemIndicator /></ListBox.Item>)}
          </ListBox></Select.Popover>
        </Select>
        {keyError && <Button size="sm" variant="secondary" onPress={fetchKeys}>重试 Key 名称</Button>}
        <Select aria-label="日志渠道" selectedKey={filters.channel_id} onSelectionChange={(value) => setFilters((current) => ({ ...current, channel_id: value || 'all' }))} className="logs-filter-channel">
          <Label>渠道</Label><Select.Trigger className="logs-control"><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover className="dialog-options"><ListBox>
            <ListBox.Item id="all" textValue="全部渠道">全部渠道<ListBox.ItemIndicator /></ListBox.Item>
            {channelIds.map((id) => <ListBox.Item key={id} id={id} textValue={logChannelLabel(id, channels)}>{logChannelLabel(id, channels)}<ListBox.ItemIndicator /></ListBox.Item>)}
          </ListBox></Select.Popover>
        </Select>
        {channelError && <Button size="sm" variant="secondary" onPress={fetchChannels}>重试渠道名称</Button>}
        <Select
          name="success"
          selectedKey={filters.success}
          onSelectionChange={(v) => setFilters((f) => ({ ...f, success: v }))}
          className="logs-filter-status"
        >
          <Label>状态</Label>
          <Select.Trigger className="logs-control">
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox>
              {successOptions.map((o) => (
                <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
                  {o.label}
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>

        <TextField name="group_name" value={filters.group_name} onChange={(v) => setFilters((f) => ({ ...f, group_name: v }))} className="logs-filter-field">
          <Label>模型分组</Label>
          <Input className="logs-control" placeholder="按分组名称查询" />
        </TextField>
        <TextField
          name="model_name"
          value={filters.model_name}
          onChange={(v) => setFilters((f) => ({ ...f, model_name: v }))}
          className="logs-filter-field"
        >
          <Label>模型名</Label>
          <Input className="logs-control" placeholder="筛选模型" />
        </TextField>
        <div className="logs-filter-actions">
          <Button
            size="md"
            variant="secondary"
            aria-label="刷新调用记录"
            onPress={() => fetchLogs()}
            isPending={showRefreshing}
            className="logs-refresh-action"
          >
            <RotateCw className="size-4" />
          </Button>
          {/* 查询：已在第 1 页直接带筛选刷新；否则回第 1 页由 effect 以新页码请求，避免双请求竞态 */}
          <Button
            size="md"
            variant="primary"
            className="logs-filter-action"
            onPress={() => {
              setPage(1);
              setQuery({ ...filters });
            }}
          >
            <Search className="size-4" /> 查询
          </Button>
        </div>
      </div>
      <div className="logs-query-summary" aria-label="已生效筛选">
        <span className="logs-query-caption">当前查询</span>
        <div className="logs-query-tags">
          {query.api_key_id !== 'all' && <span>API Key：{apiKeyLabel(query.api_key_id, apiKeys)}</span>}
          {query.channel_id !== 'all' && <span>渠道：{logChannelLabel(query.channel_id, channels)}</span>}
          {query.group_name && <span>分组：{query.group_name}</span>}
          {query.model_name && <span>模型：{query.model_name}</span>}
          {query.success !== 'all' && <span>状态：{query.success === 'true' ? '成功' : '失败'}</span>}
          {!hasActiveFilters && <span className="logs-query-all">全部记录</span>}
        </div>
        <Button size="sm" variant="tertiary" isDisabled={!hasActiveFilters} onPress={() => {
          const cleared = { api_key_id: 'all', group_name: '', model_name: '', channel_id: 'all', success: 'all' };
          setFilters(cleared); setQuery(cleared); setPage(1);
        }}>清空筛选</Button>
      </div>

      {/* 日志表格 */}
      <DataTable
        className="logs-table"
        ariaLabel="调用记录"
        dataSource={logs.data || []}
        columns={['created_at', 'success', 'api_key_id', 'group_name', 'model_name', 'input_token', 'output_token', 'duration_ms', 'detail'].map((key) => columns.find((column) => column.key === key))}
        loading={loading}
        refreshing={showRefreshing}
        emptyText={hasActiveFilters ? '没有符合条件的调用记录，可调整或清除筛选' : '暂无调用记录'}
        errorText={loadError ? '调用记录加载失败，请检查后端服务后重试' : undefined}
        onRetry={() => fetchLogs()}
      />

      <LogDetailModal record={detail?.record} association={detail?.association} channels={channels} onClose={() => setDetail(null)} />

      {/* 分页 */}
      <div className="logs-pagination">
        <Typography type="body-sm" className="text-muted">
          共 {logs.total} 条，第 {page}/{totalPages} 页
        </Typography>
        <div className="flex gap-1">
          <Button size="sm" variant="secondary" isDisabled={page <= 1} onPress={() => setPage((p) => p - 1)}>上一页</Button>
          <Button size="sm" variant="secondary" isDisabled={page >= totalPages} onPress={() => setPage((p) => p + 1)}>下一页</Button>
        </div>
      </div>
    </div>
  );
}

function StatCard({ label, value, detail }) {
  return (
    <Card className="logs-stat-card gap-1 px-4 py-2 rounded-2xl">
      <Card.Header className="p-0">
        <Typography type="body-sm" className="text-muted">{label}</Typography>
      </Card.Header>
      <Card.Content className="flex items-baseline gap-2 p-0">
        <Typography type="body" className="font-semibold">{value}</Typography>
        {detail && <Typography type="body-xs" className="text-muted">{detail}</Typography>}
      </Card.Content>
    </Card>
  );
}

function formatNum(n) {
  if (n == null) return '—';
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
  return String(n);
}

function formatMs(ms) {
  if (ms == null || ms <= 0) return '—';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}
