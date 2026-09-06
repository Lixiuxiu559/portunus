import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Typography, Chip, Card, toast, Select, ListBox, TextField, Input, Spinner } from '@heroui/react';
import { Plus, Trash2, RotateCw, X, Search, Pencil } from 'lucide-react';
import CreateModelModal from './CreateModelModal';
import EditModelModal from './EditModelModal';
import DeleteModelModal from './DeleteModelModal';
import { listModels, updateModel, createModel, deleteModel, listChannels } from '../../api';
import IconButton from '../../components/IconButton';

// 价格格式化：避免科学计数法（如 1e-7），最多 8 位小数并去掉尾零
function formatPrice(v) {
  if (v == null || v === '') return '—';
  const n = Number(v);
  if (!Number.isFinite(n)) return '—';
  if (n === 0) return '0';
  return n.toFixed(8).replace(/\.?0+$/, '');
}

export default function Models() {
  const [models, setModels] = useState([]);
  const [channels, setChannels] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [editTarget, setEditTarget] = useState(null);
  const [showCreate, setShowCreate] = useState(false);
  // 首次/查询失败标记：区分"真没数据"和"加载失败"，后者给重试入口
  const [loadError, setLoadError] = useState(false);
  // 列表滚动容器：翻页/换筛选后回顶，避免停留在上一页的滚动位置
  const listRef = useRef(null);
  // 查询条件（点击查询按钮后才生效）
  const [queryChannel, setQueryChannel] = useState('all');
  const [queryName, setQueryName] = useState('');
  // 输入框的临时值，未点击查询前不触发请求
  const [draftName, setDraftName] = useState('');
  const [draftChannel, setDraftChannel] = useState('all');
  const pageSize = 24;

  const fetchAll = async () => {
    setLoading(true);
    try {
      const start = Date.now();
      const [m, c] = await Promise.all([listModels({ page: 1, page_size: pageSize }), listChannels()]);
      setModels(Array.isArray(m?.data) ? m.data : []);
      setTotal(m?.total ?? 0);
      setPage(1);
      setChannels(Array.isArray(c) ? c : []);
      // 确保加载状态至少显示 300ms，避免闪烁
      const elapsed = Date.now() - start;
      if (elapsed < 300) await new Promise((r) => setTimeout(r, 300 - elapsed));
    } catch {
      if (models.length === 0) setLoadError(true); // 无数据时进错误态而非伪造空态
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchAll(); }, []);

  const channelMap = useMemo(
    () => Object.fromEntries(channels.map((c) => [c.id, c.name])),
    [channels],
  );

  // 实际执行查询：channel + name 组合
  const performSearch = useCallback(async (channel, name, targetPage = 1) => {
    setQueryChannel(channel);
    setQueryName(name.trim());
    const params = { page: targetPage, page_size: pageSize };
    if (channel !== 'all') params.channel_id = channel;
    if (name.trim()) params.name = name.trim();
    // 有数据时使用 refreshing（不遮挡内容），无数据时使用 loading（显示加载状态）
    const setRefreshState = models.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    try {
      const res = await listModels(params);
      setModels(Array.isArray(res?.data) ? res.data : []);
      setTotal(res?.total ?? 0);
      setPage(targetPage);
      setLoadError(false);
      // 内容整体替换后回到列表顶部，避免停留在上一页的滚动位置
      listRef.current?.scrollTo({ top: 0 });
    } catch {
      if (models.length === 0) setLoadError(true); // 无数据时进错误态而非伪造空态
    } finally {
      setRefreshState(false);
    }
  }, [pageSize, models.length]);

  // 点查询按钮 / 回车：按当前草稿值查询（回到第 1 页）
  const handleSearch = useCallback(() => {
    performSearch(draftChannel, draftName, 1);
  }, [performSearch, draftChannel, draftName]);

  // 切换渠道：立即请求（名称取当前输入框的草稿值）
  const handleChannelChange = useCallback(
    (channel) => {
      setDraftChannel(channel);
      performSearch(channel, draftName, 1);
    },
    [performSearch, draftName],
  );

  // 翻页：按已生效的查询条件请求
  const handlePageChange = useCallback(
    (nextPage) => {
      performSearch(queryChannel, queryName, nextPage);
    },
    [performSearch, queryChannel, queryName],
  );

  const handleCreate = async (data) => {
    await createModel(data);
    toast.success('模型创建成功');
    // 保留当前筛选回第 1 页，避免整体刷新静默重置筛选与输入框状态
    await performSearch(queryChannel, queryName, 1);
  };

  const handleUpdate = async (id, data) => {
    await updateModel(id, data);
    toast.success('模型价格已更新');
    // 就地更新当前列表，避免整页刷新导致分页/筛选状态丢失
    setModels((prev) => prev.map((x) => (x.id === id ? { ...x, ...data } : x)));
  };

  const handleDelete = async (m) => {
    await deleteModel(m.id);
    toast.success('模型已删除');
    // 刷新当前页：删掉当前页最后一条时自动回退到上一页
    const nextPage = models.length === 1 && page > 1 ? page - 1 : page;
    await performSearch(queryChannel, queryName, nextPage);
  };

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex items-center justify-between mb-4 shrink-0">
        <Typography type="h2">模型管理</Typography>
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="md"
            onPress={() => performSearch(queryChannel, queryName, page)}
            isPending={loading || refreshing}
            aria-label="刷新模型列表"
          >
            <RotateCw className="size-4" />
          </Button>
          <Button variant="primary" size="md" onPress={() => setShowCreate(true)}>
            <Plus className="size-4" />
            新增模型
          </Button>
        </div>
      </div>

      {/* 筛选区：名称 + 渠道，点击查询按钮后按条件请求后端 */}
      <div className="model-filters flex items-center gap-2 mb-3 shrink-0">
        <TextField
          size="sm"
          value={draftName}
          onChange={setDraftName}
          placeholder="按名称筛选"
          className="w-56"
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleSearch();
          }}
        >
          <Input />
        </TextField>
        {draftName && (
          <IconButton
            size="sm"
            label="清除名称筛选"
            onClick={() => {
              setDraftName('');
              performSearch(draftChannel, '', 1); // 清除后立即重新请求，避免列表与筛选状态脱节
            }}
          >
            <X className="size-3.5" />
          </IconButton>
        )}

        <Select
          size="sm"
          selectedKey={draftChannel}
          onSelectionChange={handleChannelChange}
          className="w-52"
        >
          <Select.Trigger>
            <Select.Value />
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox>
              <ListBox.Item id="all" textValue="全部渠道">
                全部渠道
                <ListBox.ItemIndicator />
              </ListBox.Item>
              {channels.map((c) => (
                <ListBox.Item key={c.id} id={String(c.id)} textValue={c.name}>
                  {c.name}
                  <ListBox.ItemIndicator />
                </ListBox.Item>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
        <Button variant="primary" size="sm" onPress={handleSearch} isPending={loading || refreshing}>
          <Search className="size-4" />
          查询
        </Button>
      </div>

      {loading ? (
        <div className="flex justify-center py-12">
          <Spinner size="sm" />
        </div>
      ) : loadError && models.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>模型加载失败，请检查后端服务后重试</span>
          <Button
            variant="secondary"
            size="sm"
            onPress={() => (channels.length === 0 ? fetchAll() : performSearch(queryChannel, queryName, page))}
          >
            重试
          </Button>
        </div>
      ) : models.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>{queryName || queryChannel !== 'all' ? '没有符合条件的模型' : '暂无模型'}</span>
          <Button variant="primary" size="sm" onPress={() => setShowCreate(true)}>
            <Plus className="size-4" />
            新增模型
          </Button>
        </div>
      ) : (
        <div ref={listRef} className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-stretch">
            {models.map((m) => {
              const channelName = channelMap[m.channel_id] || m.channel_id;
              return (
              <Card key={m.id} className="gap-4 p-5">
                {/* 操作按钮上移标题行右侧：消除"按钮孤行"，与渠道/分组卡同模式 */}
                <Card.Header className="flex-row items-center justify-between gap-3 shrink-0">
                  {/* 模型名单行截断：长模型名（无空格 token）不换行，同网格行内价格区对齐 */}
                  <Typography className="min-w-0 flex-1 truncate font-medium text-lg" title={m.name}>
                    {m.name}
                  </Typography>
                  <div className="flex items-center gap-1 shrink-0">
                    <IconButton label={`编辑模型 ${m.name}`} onClick={() => setEditTarget(m)}>
                      <Pencil className="size-4" />
                    </IconButton>
                    <IconButton label={`删除模型 ${m.name}`} onClick={() => setDeleteTarget(m)} danger>
                      <Trash2 className="size-4" />
                    </IconButton>
                  </div>
                </Card.Header>

                <Card.Content className="min-w-0">
                  {/* 渠道归属 + 计价单位同行（左属性右单位），替代原 footer 行 */}
                  <div className="flex items-center justify-between gap-3">
                    <Chip variant="soft" size="sm" color="default" className="min-w-0 max-w-[60%]">
                      <span className="truncate" title={channelName}>
                        {channelName}
                      </span>
                    </Chip>
                    <span className="shrink-0 text-xs text-muted">USD / 1M tokens</span>
                  </div>
                  {/* 价格两行制：标签左值右同行，2×2 压成两行；列内值右对齐 + tabular-nums 成数字纵列 */}
                  <div className="mt-2 grid grid-cols-2 gap-x-4 gap-y-2">
                    <div className="flex min-w-0 items-baseline justify-between gap-2">
                      <span className="shrink-0 text-xs text-muted">输入</span>
                      <span className="min-w-0 truncate font-mono text-sm tabular-nums" title={formatPrice(m.input_price)}>
                        {formatPrice(m.input_price)}
                      </span>
                    </div>
                    <div className="flex min-w-0 items-baseline justify-between gap-2">
                      <span className="shrink-0 text-xs text-muted">输出</span>
                      <span className="min-w-0 truncate font-mono text-sm tabular-nums" title={formatPrice(m.output_price)}>
                        {formatPrice(m.output_price)}
                      </span>
                    </div>
                    <div className="flex min-w-0 items-baseline justify-between gap-2">
                      <span className="shrink-0 text-xs text-muted">缓存读取</span>
                      <span className="min-w-0 truncate font-mono text-sm tabular-nums" title={formatPrice(m.cache_read_price)}>
                        {formatPrice(m.cache_read_price)}
                      </span>
                    </div>
                    <div className="flex min-w-0 items-baseline justify-between gap-2">
                      <span className="shrink-0 text-xs text-muted">缓存写入</span>
                      <span className="min-w-0 truncate font-mono text-sm tabular-nums" title={formatPrice(m.cache_write_price)}>
                        {formatPrice(m.cache_write_price)}
                      </span>
                    </div>
                  </div>
                </Card.Content>
              </Card>
              );
            })}
          </div>
        </div>
      )}

      {/* 分页 */}
      <div className="flex items-center justify-between mt-3 shrink-0">
        <Typography type="body-sm" className="text-muted">
          共 {total} 条，第 {page}/{Math.max(1, Math.ceil(total / pageSize))} 页
        </Typography>
        <div className="flex gap-1">
          <Button
            size="sm"
            variant="secondary"
            isDisabled={page <= 1}
            onPress={() => handlePageChange(page - 1)}
          >
            上一页
          </Button>
          <Button
            size="sm"
            variant="secondary"
            isDisabled={page >= Math.max(1, Math.ceil(total / pageSize))}
            onPress={() => handlePageChange(page + 1)}
          >
            下一页
          </Button>
        </div>
      </div>

      <CreateModelModal
        isOpen={showCreate}
        onOpenChange={setShowCreate}
        channels={channels}
        onSubmit={handleCreate}
      />
      <EditModelModal
        model={editTarget}
        channels={channels}
        isOpen={editTarget !== null}
        onOpenChange={(open) => { if (!open) setEditTarget(null); }}
        onSubmit={handleUpdate}
      />
      <DeleteModelModal
        model={deleteTarget}
        isOpen={deleteTarget !== null}
        onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}
        onDeleted={handleDelete}
      />
    </div>
  );
}