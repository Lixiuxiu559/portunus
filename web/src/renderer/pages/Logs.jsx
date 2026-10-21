import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Typography, Button, Card, TextField, Input, Select, ListBox, Label,
  DateRangePicker, DateField, RangeCalendar,
} from '@heroui/react';
import { Search, RotateCw, X } from 'lucide-react';
import { today, getLocalTimeZone, CalendarDateTime } from '@internationalized/date';
import DataTable from '../components/DataTable';
import { buildLogParams } from '../utils/logQuery';
import { listLogs, listLogRequests, getLogStats, listChannels } from '../api';
import { errKindLabels, logChannelLabel } from '../utils/logDetails';
import LogDetailModal from './LogDetailModal';
import './logs.css';

const successOptions = [
  { id: 'all', label: '全部' },
  { id: 'true', label: '成功' },
  { id: 'false', label: '失败' },
];

// 客户端入口协议（与后端 protocol.Provider 取值一致）。DSH 走 openai、
// Claude Code 走 anthropic，两者共用同一 APIKey——只有按此筛选才能把它们的
// 缓存命中率分开比较（存量日志该字段为空，表示未记录）。
const clientProtocolOptions = [
  { id: 'all', label: '全部' },
  { id: 'openai', label: 'OpenAI' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'openai_responses', label: 'Responses' },
  { id: 'gemini', label: 'Gemini' },
];

export default function Logs() {
  const [searchParams] = useSearchParams();
  const linkedGroup = searchParams.get('group') || '';
  const lastLinkedGroup = useRef(linkedGroup);
  const requestSequence = useRef(0);
  const [logs, setLogs] = useState({ total: 0, data: [] });
  const [stats, setStats] = useState(null);
  const [channels, setChannels] = useState([]);
  const [channelError, setChannelError] = useState(false);
  const [detail, setDetail] = useState(null);
  const fetchChannels = async () => {
    try { const result = await listChannels(); setChannels(Array.isArray(result) ? result : []); setChannelError(false); }
    catch { setChannelError(true); }
  };
  useEffect(() => { fetchChannels(); }, []);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [page, setPage] = useState(1);
  const [view, setView] = useState('records');
  const pageSize = 20;
  // 首次加载失败标记：区分"真没数据"和"加载失败"，后者给重试入口
  const [loadError, setLoadError] = useState(false);

  const [filters, setFilters] = useState({
    group_name: linkedGroup,
    model_name: '',
    channel_id: 'all',
    request_id: '',
    client_protocol: 'all',
    success: 'all',
    range: null,
  });

  const [query, setQuery] = useState(filters);

  useEffect(() => {
    if (lastLinkedGroup.current === linkedGroup) return;
    lastLinkedGroup.current = linkedGroup;
    setFilters((f) => ({ ...f, group_name: linkedGroup }));
    setQuery((q) => ({ ...q, group_name: linkedGroup }));
    setPage(1);
  }, [linkedGroup]);

  // targetPage 显式传页码，避免 setPage 与请求闭包页码不一致的双请求竞态
  const fetchLogs = async (isRefresh = false, targetPage = page) => {
    // 有数据时使用 refreshing（不遮挡表格），无数据时使用 loading（显示骨架屏）
    const setRefreshState = isRefresh || logs.data?.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    const sequence = ++requestSequence.current;
    try {
      // 时间参数必须是带时区偏移的 RFC3339（见 utils/logQuery.js 的契约说明）
      const params = buildLogParams(query, targetPage, pageSize);
      const [l, s] = await Promise.all([view === 'requests' ? listLogRequests(params) : listLogs(params), getLogStats(params)]);
      if (sequence !== requestSequence.current) return;
      setLogs(l || { total: 0, data: [] });
      setStats(s);
      setLoadError(false);
    } catch {
      // 无数据时进错误态而非伪造空态；stats 保留旧值（表格错误态已表明数据不可信）
      if (sequence === requestSequence.current && (logs.data?.length || 0) === 0) setLoadError(true);
    } finally {
      if (sequence === requestSequence.current) { setLoading(false); setRefreshing(false); }
    }
  };

  useEffect(() => { fetchLogs(); }, [page, query, view]);

  const channelIds = [...new Set([...channels.map((item) => String(item.id)), ...(logs.data || []).map((item) => (item.latest_log || item).channel_id).filter((id) => id > 0).map(String), ...(filters.channel_id !== 'all' ? [filters.channel_id] : [])])];

  const totalPages = Math.max(1, Math.ceil((logs.total || 0) / pageSize));
  const advancedFilterCount = [filters.group_name, filters.model_name, filters.client_protocol !== 'all', filters.range].filter(Boolean).length;

  const defaultDate = (() => { const d = today(getLocalTimeZone()); return new CalendarDateTime(d.year, d.month, d.day, 0, 0, 0); })();

  const columns = [
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: '160px',
      render: (val) => (
        <span className="block whitespace-nowrap text-center font-mono text-xs text-muted">
          {val ? new Date(val).toLocaleString('zh-CN') : '—'}
        </span>
      ),
    },
    {
      title: '状态',
      dataIndex: 'success',
      key: 'success',
      width: '110px',
      render: (val, record) => (
        <span className="flex flex-col items-center gap-0.5">
          <span className={`inline-block rounded-full px-2 py-0.5 text-xs ${
            val ? 'bg-success/15 text-success' : 'bg-danger/15 text-danger'
          }`}>
            {val ? '成功' : '失败'}
          </span>
          {!val && record?.err_kind && (
            <span
              className="max-w-[100px] truncate text-xs text-muted"
              title={record?.err_msg || record.err_kind}
            >
              {errKindLabels[record.err_kind] || record.err_kind}
            </span>
          )}
        </span>
      ),
    },
    {
      title: '实际渠道 / 模型',
      dataIndex: 'model_name',
      key: 'model_name',
      width: '200px',
      render: (val, record) => <span className="block text-center"><span className="block truncate" title={String(val || '')}>{val || '未记录模型'}</span><span className="block truncate text-xs text-muted" title={logChannelLabel(record.channel_id, channels)}>{logChannelLabel(record.channel_id, channels)}</span></span>,
    },
    { title: '详情', dataIndex: 'id', key: 'detail', width: '90px', render: (id, record) => <Button size="sm" variant="secondary" aria-label={`查看日志 ${id} 详情`} onPress={() => setDetail({ record })}>详情</Button> },
    {
      title: '分组',
      dataIndex: 'group_name',
      key: 'group_name',
      width: '200px',
      render: (val) => (val ? (
        <span className="block truncate text-center" title={String(val)}>{val}</span>
      ) : '—'),
    },
    {
      title: '客户端 / 桥接',
      dataIndex: 'client_protocol',
      key: 'client_protocol',
      width: '150px',
      render: (val, record) => {
        // client_protocol 为空 = 本次改动前的存量日志（未记录入口协议）。
        // 桥接/垫片计数是判断「缓存问题是否真被修复」的观测点：
        // 桥接恒为 0 而客户端是 anthropic → Claude Code 没回传网关签名，桥接未生效。
        const bridge = record?.bridge_decoded || 0;
        const shim = record?.shim_rewrites || 0;
        return (
          <span className="block text-center text-xs">
            <span className="font-mono">{val || '—'}</span>
            {(bridge > 0 || shim > 0) && (
              <span className="block text-muted">
                {bridge > 0 ? `桥接 ${bridge}` : ''}
                {bridge > 0 && shim > 0 ? ' · ' : ''}
                {shim > 0 ? `垫片 ${shim}` : ''}
              </span>
            )}
          </span>
        );
      },
    },
    {
      title: '输入 Tokens',
      dataIndex: 'input_token',
      key: 'input_token',
      width: '140px',
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
      width: '120px',
      render: (val) => (
        <span className="text-center font-mono block">{formatNum(val)}</span>
      ),
    },
    {
      title: '耗时 / 首包',
      dataIndex: 'duration_ms',
      key: 'duration_ms',
      width: '200px',
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

  const requestColumns = [
    { title: '最近日志时间', dataIndex: 'latest_log', key: 'time', width: '170px', render: (row) => row.created_at ? new Date(row.created_at).toLocaleString('zh-CN') : '未记录' },
    { title: '最近日志结果', dataIndex: 'latest_log', key: 'result', width: '140px', render: (row) => <span className={row.success ? 'text-success' : 'text-danger'}>{row.success ? '成功' : '失败'} · {row.status || '无响应状态'}</span> },
    { title: '最近渠道 / 模型', dataIndex: 'latest_log', key: 'target', width: '230px', render: (row) => <span className="block"><span className="block truncate">{row.model_name || '未记录模型'}</span><span className="block truncate text-xs text-muted">{logChannelLabel(row.channel_id, channels)}</span></span> },
    { title: '尝试链', dataIndex: 'key', width: '110px', render: (_, item) => <Button size="sm" variant="secondary" aria-label={`查看关联 ${item.request_id || `历史日志 ${item.latest_log.id}`}`} onPress={() => setDetail({ record: item.latest_log, association: item })}>查看关联</Button> },
    { title: '请求关联 ID', dataIndex: 'request_id', width: '220px', render: (value, item) => <span className="block truncate font-mono text-xs" title={value || ''}>{value || `独立历史日志 #${item.latest_log.id}`}</span> },
    { title: '模型分组', dataIndex: 'latest_log', key: 'group', width: '160px', render: (row) => row.group_name || '未记录' },
    { title: '关联日志数', dataIndex: 'log_count', width: '110px' },
  ];
  const changeView = (next) => {
    if (next === view) return;
    requestSequence.current += 1;
    setView(next); setPage(1); setLogs({ total: 0, data: [] }); setLoading(true); setRefreshing(false); setLoadError(false); setDetail(null);
  };

  return (
    <div className="logs-page flex flex-col flex-1 min-h-0">
      <div className="resource-page-heading logs-heading">
        <div>
          <h1 className="text-xl font-semibold">调用记录</h1>
          <p className="mt-1 text-sm text-muted">追踪模型调用，查看用量与排查结果。</p>
        </div>
        <div className="resource-page-actions">
        <Button
          size="md"
          variant="secondary"
          aria-label="刷新调用记录"
          onPress={() => fetchLogs(true)}
          isPending={refreshing}
          className="logs-refresh-action"
        >
          <RotateCw className="size-4" />
        </Button>
        </div>
      </div>
      <div className="logs-toolbar">
      <div className="logs-view-switch" role="group" aria-label="调用记录视图">
        <Button size="sm" variant={view === 'records' ? 'primary' : 'secondary'} aria-pressed={view === 'records'} onPress={() => changeView('records')}>日志记录</Button>
        <Button size="sm" variant={view === 'requests' ? 'primary' : 'secondary'} aria-pressed={view === 'requests'} onPress={() => changeView('requests')}>按请求归并</Button>

      </div>
      <details className="logs-metric-help">
        <summary>统计与归并口径</summary>
        <p>记录数按日志行统计，一次客户端请求可能产生多条记录。用量来自上游报告，输入明细保留缓存读取和写入 token；近期记录指近 60 秒。</p>
        <p>按请求 ID 关联，无 ID 的历史日志单独保留。筛选命中任一日志即可显示关联项，摘要为整个请求的最近日志。统计卡按匹配日志行汇总，关联项数不是准确客户端请求数。</p>
      </details>
      </div>
      {/* 统计按当前查询范围汇总，不把日志行数当作独立客户端请求数。 */}
      {stats && (
        <div className="logs-stats">
          <StatCard label="记录数" value={String(stats.total_requests || 0)} />
          <StatCard label="非缓存输入 Token" value={formatNum(stats.input_tokens)} detail={`缓存读 ${formatNum(stats.cache_read_tokens)} · 缓存写 ${formatNum(stats.cache_write_tokens)}`} />
          <StatCard label="输出 Token" value={formatNum(stats.output_tokens)} />
          <StatCard label="近期记录" value={String(stats.recent_requests || 0)} />
        </div>
      )}

      {/* 筛选栏 */}
      <div className="logs-filters">
        <Select aria-label="日志渠道" selectedKey={filters.channel_id} onSelectionChange={(value) => setFilters((current) => ({ ...current, channel_id: value || 'all' }))} className="logs-filter-channel">
          <Label>渠道</Label><Select.Trigger className="logs-control"><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover className="dialog-options"><ListBox>
            <ListBox.Item id="all" textValue="全部渠道">全部渠道<ListBox.ItemIndicator /></ListBox.Item>
            {channelIds.map((id) => <ListBox.Item key={id} id={id} textValue={logChannelLabel(id, channels)}>{logChannelLabel(id, channels)}<ListBox.ItemIndicator /></ListBox.Item>)}
          </ListBox></Select.Popover>
        </Select>
        <TextField name="request_id" value={filters.request_id} onChange={(value) => setFilters((current) => ({ ...current, request_id: value }))} className="logs-filter-request">
          <Label>请求 ID</Label><Input className="logs-control" placeholder="精确匹配 request_id" />
        </TextField>
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

        <details className="logs-advanced">
          <summary>更多筛选{advancedFilterCount > 0 && <span> · 已配置 {advancedFilterCount} 项</span>}</summary>
          <div className="logs-advanced-fields">
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

            <Select
              name="client_protocol"
              selectedKey={filters.client_protocol}
              onSelectionChange={(v) => setFilters((f) => ({ ...f, client_protocol: v }))}
              className="logs-filter-protocol"
            >
              <Label>客户端</Label>
              <Select.Trigger className="logs-control">
                <Select.Value />
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox>
                  {clientProtocolOptions.map((o) => (
                    <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
                      {o.label}
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                  ))}
                </ListBox>
              </Select.Popover>
            </Select>

            <DateRangePicker
              granularity="minute"
              startName="start"
              endName="end"
              value={filters.range}
              onChange={(v) => setFilters((f) => ({ ...f, range: v }))}
              placeholderValue={defaultDate}
              className="logs-filter-date"
            >
              <Label>时间范围</Label>
              <DateField.Group fullWidth className="logs-control">
                <DateField.Input slot="start">
                  {(segment) => <DateField.Segment segment={segment} />}
                </DateField.Input>
                <DateRangePicker.RangeSeparator />
                <DateField.Input slot="end">
                  {(segment) => <DateField.Segment segment={segment} />}
                </DateField.Input>
                <DateField.Suffix>
                  <DateRangePicker.Trigger>
                    <DateRangePicker.TriggerIndicator />
                  </DateRangePicker.Trigger>
                </DateField.Suffix>
              </DateField.Group>
              <DateRangePicker.Popover>
                <RangeCalendar aria-label="时间范围">
                  <RangeCalendar.Header>
                    <RangeCalendar.YearPickerTrigger>
                      <RangeCalendar.YearPickerTriggerHeading />
                      <RangeCalendar.YearPickerTriggerIndicator />
                    </RangeCalendar.YearPickerTrigger>
                    <RangeCalendar.NavButton slot="previous" />
                    <RangeCalendar.NavButton slot="next" />
                  </RangeCalendar.Header>
                  <RangeCalendar.Grid>
                    <RangeCalendar.GridHeader>
                      {(day) => <RangeCalendar.HeaderCell>{day}</RangeCalendar.HeaderCell>}
                    </RangeCalendar.GridHeader>
                    <RangeCalendar.GridBody>
                      {(date) => <RangeCalendar.Cell date={date} />}
                    </RangeCalendar.GridBody>
                  </RangeCalendar.Grid>
                  <RangeCalendar.YearPickerGrid>
                    <RangeCalendar.YearPickerGridBody>
                      {({ year }) => <RangeCalendar.YearPickerCell year={year} />}
                    </RangeCalendar.YearPickerGridBody>
                  </RangeCalendar.YearPickerGrid>
                </RangeCalendar>
              </DateRangePicker.Popover>
            </DateRangePicker>

            {filters.range && (
              <Button
                size="md"
                variant="secondary"
                onPress={() => setFilters((f) => ({ ...f, range: null }))}
                className="logs-filter-action gap-1"
              >
                <X className="size-4" /> 清除时间
              </Button>
            )}

          </div>
        </details>
      </div>
      <div className="logs-query-summary" aria-label="已生效筛选">
        <span className="logs-query-caption">当前查询</span>
        <div className="logs-query-tags">
          {query.channel_id !== 'all' && <span>渠道：{logChannelLabel(query.channel_id, channels)}</span>}
          {query.request_id.trim() && <span>请求 ID：{query.request_id.trim()}</span>}
          {query.group_name && <span>分组：{query.group_name}</span>}
          {query.model_name && <span>模型：{query.model_name}</span>}
          {query.success !== 'all' && <span>状态：{query.success === 'true' ? '成功' : '失败'}</span>}
          {query.client_protocol !== 'all' && <span>客户端：{clientProtocolOptions.find((option) => option.id === query.client_protocol)?.label || query.client_protocol}</span>}
          {query.range && <span>时间：{query.range.start?.toString()} — {query.range.end?.toString()}</span>}
          {query.channel_id === 'all' && !query.request_id.trim() && !query.group_name && !query.model_name && query.success === 'all' && query.client_protocol === 'all' && !query.range && <span className="logs-query-all">全部记录</span>}
        </div>
        <Button size="sm" variant="tertiary" onPress={() => {
          const cleared = { group_name: '', model_name: '', channel_id: 'all', request_id: '', client_protocol: 'all', success: 'all', range: null };
          setFilters(cleared); setQuery(cleared); setPage(1);
        }}>清空筛选</Button>
      </div>

      {/* 日志表格 */}
      <DataTable
        className="logs-table"
        ariaLabel="调用记录"
        dataSource={view === 'requests' ? (logs.data || []).map((item) => ({ ...item, id: item.key })) : logs.data || []}
        columns={view === 'requests' ? requestColumns : columns}
        loading={loading}
        refreshing={refreshing}
        emptyText={
          query.channel_id !== 'all' || query.request_id.trim() || query.group_name || query.model_name || query.client_protocol !== 'all' || query.success !== 'all' || query.range
            ? '没有符合条件的调用记录，可调整或清除筛选'
            : '暂无调用记录'
        }
        errorText={loadError ? '调用记录加载失败，请检查后端服务后重试' : undefined}
        onRetry={() => fetchLogs()}
      />

      <LogDetailModal record={detail?.record} association={detail?.association} channels={channels} onClose={() => setDetail(null)} />

      {/* 分页 */}
      <div className="logs-pagination">
        <Typography type="body-sm" className="text-muted">
          共 {logs.total} {view === 'requests' ? '个关联项' : '条'}，第 {page}/{totalPages} 页
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
    <Card className="logs-stat-card">
      <Card.Header className="p-0">
        <Typography type="body-sm" className="text-muted">{label}</Typography>
      </Card.Header>
      <Card.Content className="p-0">
        <Typography type="body" className="logs-stat-value">{value}</Typography>
        {detail && <Typography type="body-xs" className="mt-1 text-muted">{detail}</Typography>}
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
