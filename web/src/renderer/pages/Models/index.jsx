import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Typography, Card, toast, Select, ListBox, TextField, Input, Spinner } from '@heroui/react';
import { Plus, Trash2, RotateCw, X, Search, Box } from 'lucide-react';
import CreateModelModal from './CreateModelModal';
import DeleteModelModal from './DeleteModelModal';
import { listModels, createModel, deleteModel, listChannels } from '../../api';
import IconButton from '../../components/IconButton';

export default function Models() {
  const [models, setModels] = useState([]);
  const [channels, setChannels] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
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

  const handleDelete = async (m) => {
    await deleteModel(m.id);
    toast.success('模型已删除');
    // 刷新当前页：删掉当前页最后一条时自动回退到上一页
    const nextPage = models.length === 1 && page > 1 ? page - 1 : page;
    await performSearch(queryChannel, queryName, nextPage);
  };

  return (
    <div className="flex flex-col flex-1 min-h-0 min-w-0">
      <div className="resource-page-heading">
        <div>
          <h1 className="text-xl font-semibold">上游模型</h1>
          <p className="mt-1 text-sm text-muted">按渠道维护分组可使用的模型资源。客户端应填写分组名称，而不是这里的上游模型 ID。</p>
        </div>
        <div className="resource-page-actions">
          <Button variant="secondary" size="md" onPress={() => performSearch(queryChannel, queryName, page)} isPending={loading || refreshing} aria-label="刷新模型列表"><RotateCw className="size-4" /></Button>
          <Button variant="primary" size="md" onPress={() => setShowCreate(true)}><Plus className="size-4" />新增模型</Button>
        </div>
      </div>
      <div className="model-filters resource-model-filters">
        <TextField size="sm" aria-label="按模型名称筛选" value={draftName} onChange={setDraftName} placeholder="按名称筛选" className="w-56" onKeyDown={(e) => { if (e.key === 'Enter') handleSearch(); }}><Input /></TextField>
        {draftName && <IconButton size="sm" label="清除名称筛选" onClick={() => { setDraftName(''); performSearch(draftChannel, '', 1); }}><X className="size-3.5" /></IconButton>}
        <Select size="sm" selectedKey={draftChannel} onSelectionChange={handleChannelChange} className="w-52">
          <Select.Trigger aria-label="按上游渠道筛选" aria-labelledby={null}><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover className="resource-channel-popover"><ListBox aria-label="上游渠道">
            <ListBox.Item id="all" textValue="全部渠道">全部渠道<ListBox.ItemIndicator /></ListBox.Item>
            {channels.map((c) => <ListBox.Item key={c.id} id={String(c.id)} textValue={c.name}><span className="min-w-0 truncate" title={c.name}>{c.name}</span><ListBox.ItemIndicator /></ListBox.Item>)}
          </ListBox></Select.Popover>
        </Select>
        <Button variant="secondary" size="sm" onPress={handleSearch} isPending={loading || refreshing}><Search className="size-4" />查询</Button>
      </div>
      {loading ? <div className="flex justify-center py-12"><Spinner size="sm" /></div>
      : loadError && models.length === 0 ? <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>模型加载失败，请检查后端服务后重试</span>
          <Button variant="secondary" size="sm" onPress={() => (channels.length === 0 ? fetchAll() : performSearch(queryChannel, queryName, page))}>重试</Button>
        </div>
      : models.length === 0 ? <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>{queryName || queryChannel !== 'all' ? '没有符合条件的模型' : '暂无模型'}</span>
          <Button variant="primary" size="sm" onPress={() => setShowCreate(true)}><Plus className="size-4" />新增模型</Button>
        </div>
      : <div ref={listRef} className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
          <div className="resource-grid">
            {models.map((m) => {
              const channelName = channelMap[m.channel_id] || m.channel_id;
              return <Card key={m.id} className="resource-card">
                <Card.Header className="flex-row items-center justify-between gap-3 shrink-0">
                  <div className="resource-card-identity">
                    <span className="resource-card-symbol" aria-hidden="true"><Box className="size-4" /></span>
                    <div className="min-w-0 flex-1">
                      <Typography className="resource-card-title resource-card-model" title={m.name}>{m.name}</Typography>
                      <span className="resource-card-source" title={String(channelName)}>{channelName}</span>
                    </div>
                  </div>
                  <IconButton label={`删除模型 ${m.name}`} onClick={() => setDeleteTarget(m)} danger><Trash2 className="size-4" /></IconButton>
                </Card.Header>
              </Card>;
            })}
          </div>
        </div>}
      <div className="flex items-center justify-between mt-3 shrink-0">
        <Typography type="body-sm" className="text-muted">共 {total} 条，第 {page}/{Math.max(1, Math.ceil(total / pageSize))} 页</Typography>
        <div className="flex gap-1">
          <Button size="sm" variant="secondary" isDisabled={page <= 1} onPress={() => handlePageChange(page - 1)}>上一页</Button>
          <Button size="sm" variant="secondary" isDisabled={page >= Math.max(1, Math.ceil(total / pageSize))} onPress={() => handlePageChange(page + 1)}>下一页</Button>
        </div>
      </div>
      <CreateModelModal
        isOpen={showCreate}
        onOpenChange={setShowCreate}
        channels={channels}
        onSubmit={handleCreate}
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
