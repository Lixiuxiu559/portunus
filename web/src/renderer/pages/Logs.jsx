import { useEffect, useState } from 'react';
import {
  Typography, Button, Card, TextField, Input, Select, ListBox, Label,
  DateRangePicker, DateField, RangeCalendar,
} from '@heroui/react';
import { Search, RotateCw, X } from 'lucide-react';
import { today, getLocalTimeZone, CalendarDateTime } from '@internationalized/date';
import DataTable from '../components/DataTable';
import { listLogs, getLogStats } from '../api';

const successOptions = [
  { id: 'all', label: '全部' },
  { id: 'true', label: '成功' },
  { id: 'false', label: '失败' },
];

// 失败类别中文标签（与后端 gateway.classifyErr 的 err_kind 取值对应）
const errKindLabels = {
  client_cancel: '用户取消',
  upstream_error: '上游错误',
  watchdog_timeout: '流静默超时',
  stream_interrupted: '流中断',
  convert_error: '转换失败',
  network: '网络错误',
  internal: '内部错误',
  circuit_open: '熔断开路',
};

export default function Logs() {
  const [logs, setLogs] = useState({ total: 0, data: [] });
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [page, setPage] = useState(1);
  const pageSize = 20;
  // 首次加载失败标记：区分"真没数据"和"加载失败"，后者给重试入口
  const [loadError, setLoadError] = useState(false);

  const [filters, setFilters] = useState({
    model_name: '',
    success: 'all',
    range: null,
  });

  // targetPage 显式传页码，避免 setPage 与请求闭包页码不一致的双请求竞态
  const fetchLogs = async (isRefresh = false, targetPage = page) => {
    // 有数据时使用 refreshing（不遮挡表格），无数据时使用 loading（显示骨架屏）
    const setRefreshState = isRefresh || logs.data?.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    try {
      const params = { page: targetPage, page_size: pageSize };
      if (filters.model_name) params.model_name = filters.model_name;
      if (filters.success !== 'all') params.success = filters.success;
      if (filters.range?.start) params.start_time = filters.range.start.toString();
      if (filters.range?.end) params.end_time = filters.range.end.toString();
      const [l, s] = await Promise.all([listLogs(params), getLogStats(params)]);
      setLogs(l || { total: 0, data: [] });
      setStats(s);
      setLoadError(false);
    } catch {
      // 无数据时进错误态而非伪造空态；stats 保留旧值（表格错误态已表明数据不可信）
      if ((logs.data?.length || 0) === 0) setLoadError(true);
    } finally {
      setRefreshState(false);
    }
  };

  useEffect(() => { fetchLogs(); }, [page]);

  const totalPages = Math.max(1, Math.ceil((logs.total || 0) / pageSize));

  const defaultDate = (() => { const d = today(getLocalTimeZone()); return new CalendarDateTime(d.year, d.month, d.day, 0, 0, 0); })();

  const columns = [
    {
      title: '时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (val) => (
        <span className="whitespace-nowrap font-mono text-xs text-muted">
          {val ? new Date(val).toLocaleString('zh-CN') : '—'}
        </span>
      ),
    },
    {
      title: '模型',
      dataIndex: 'model_name',
      key: 'model_name',
      width: '200px',
      render: (val) => (val ? (
        <span className="block truncate" title={String(val)}>{val}</span>
      ) : '—'),
    },
    {
      title: '分组',
      dataIndex: 'group_name',
      key: 'group_name',
      width: '200px',
      render: (val) => (val ? (
        <span className="block truncate" title={String(val)}>{val}</span>
      ) : '—'),
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
        // 缓存命中明细放小字；DB 字段保持计费口径（input_token 为未命中部分）不变。
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
    {
      title: '费用',
      dataIndex: 'cost',
      key: 'cost',
      width: '100px',
      render: (val) => (
        <span className="text-center font-mono block">${Number(val || 0).toFixed(6)}</span>
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
  ];

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">调用日志</Typography>
        <Button
          variant="secondary"
          size="md"
          aria-label="刷新日志"
          onPress={() => fetchLogs(true)}
          isPending={refreshing}
        >
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
      <div className="flex flex-wrap items-end gap-3 mb-4">
        <TextField
          name="model_name"
          value={filters.model_name}
          onChange={(v) => setFilters((f) => ({ ...f, model_name: v }))}
          className="w-40"
        >
          <Label>模型名</Label>
          <Input placeholder="筛选模型" />
        </TextField>

        <Select
          name="success"
          selectedKey={filters.success}
          onSelectionChange={(v) => setFilters((f) => ({ ...f, success: v }))}
          className="w-28"
        >
          <Label>状态</Label>
          <Select.Trigger>
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

        <DateRangePicker
          granularity="minute"
          startName="start"
          endName="end"
          value={filters.range}
          onChange={(v) => setFilters((f) => ({ ...f, range: v }))}
          placeholderValue={defaultDate}
          className="w-fit"
        >
          <Label>时间范围</Label>
          <DateField.Group fullWidth>
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
            className="gap-1"
          >
            <X className="size-4" /> 清除时间
          </Button>
        )}

        {/* 查询：已在第 1 页直接带筛选刷新；否则回第 1 页由 effect 以新页码请求，避免双请求竞态 */}
        <Button
          size="md"
          variant="primary"
          onPress={() => {
            if (page === 1) fetchLogs(true);
            else setPage(1);
          }}
        >
          <Search className="size-4" /> 查询
        </Button>
      </div>

      {/* 日志表格 */}
      <DataTable
        dataSource={logs.data || []}
        columns={columns}
        loading={loading}
        refreshing={refreshing}
        emptyText={
          filters.model_name || filters.success !== 'all' || filters.range
            ? '没有符合条件的日志，可调整或清除筛选'
            : '暂无日志'
        }
        errorText="日志加载失败，请检查后端服务后重试"
        onRetry={() => fetchLogs()}
      />

      {/* 分页 */}
      <div className="flex items-center justify-between mt-3">
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

function StatCard({ label, value }) {
  return (
    <Card className="gap-1 px-4 py-2 rounded-2xl">
      <Card.Header className="p-0">
        <Typography type="body-sm" className="text-muted">{label}</Typography>
      </Card.Header>
      <Card.Content className="p-0">
        <Typography type="body" className="font-semibold">{value}</Typography>
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
