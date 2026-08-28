import { memo, useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Switch, Typography, Chip, toast, Select, ListBox, TextField, Input } from '@heroui/react';
import { Plus, Trash2, RotateCw, X, Search, Pencil } from 'lucide-react';
import DataTable from '../../components/DataTable';
import CreateModelModal from './CreateModelModal';
import EditModelModal from './EditModelModal';
import DeleteModelModal from './DeleteModelModal';
import { listModels, updateModel, createModel, deleteModel, listChannels } from '../../api';

export default function Models() {
  const [models, setModels] = useState([]);
  const [channels, setChannels] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [togglingId, setTogglingId] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [editTarget, setEditTarget] = useState(null);
  const [showCreate, setShowCreate] = useState(false);
  // 查询条件（点击查询按钮后才生效）
  const [queryChannel, setQueryChannel] = useState('all');
  const [queryName, setQueryName] = useState('');
  // 输入框的临时值，未点击查询前不触发请求
  const [draftName, setDraftName] = useState('');
  const [draftChannel, setDraftChannel] = useState('all');
  const pageSize = 20;

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
      setModels([]);
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
    // 有数据时使用 refreshing（不遮挡表格），无数据时使用 loading（显示骨架屏）
    const setRefreshState = models.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    try {
      const res = await listModels(params);
      setModels(Array.isArray(res?.data) ? res.data : []);
      setTotal(res?.total ?? 0);
      setPage(targetPage);
    } catch {
      setModels([]);
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

  // 刷新按钮的 isPending 状态
  const isRefreshing = refreshing;

  const handleToggle = async (m) => {
    setTogglingId(m.id);
    try {
      await updateModel(m.id, { enabled: !m.enabled });
      setModels((prev) => prev.map((x) => (x.id === m.id ? { ...x, enabled: !x.enabled } : x)));
      toast.success(m.enabled ? '已停用' : '已启用');
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setTogglingId(null);
    }
  };

  const handleCreate = async (data) => {
    await createModel(data);
    toast.success('模型创建成功');
    await fetchAll();
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

  const columns = useMemo(() => [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      isRowHeader: true,
      render: (val) => (
        <span className="font-medium">{val}</span>
      ),
    },
    {
      title: '渠道',
      dataIndex: 'channel_id',
      key: 'channel_id',
      render: (val) => (
        <Chip variant="soft" size="sm" color="default">
          {channelMap[val] || val}
        </Chip>
      ),
    },
    {
      title: '输入价格',
      dataIndex: 'input_price',
      key: 'input_price',
      width: '110px',
      render: (val) => (
        <span className="font-mono">${val ?? '—'}</span>
      ),
    },
    {
      title: '输出价格',
      dataIndex: 'output_price',
      key: 'output_price',
      width: '110px',
      render: (val) => (
        <span className="font-mono">${val ?? '—'}</span>
      ),
    },
    {
      title: '缓存读取',
      dataIndex: 'cache_read_price',
      key: 'cache_read_price',
      width: '110px',
      render: (val) => (
        <span className="font-mono">${val ?? '—'}</span>
      ),
    },
    {
      title: '缓存写入',
      dataIndex: 'cache_write_price',
      key: 'cache_write_price',
      width: '110px',
      render: (val) => (
        <span className="font-mono">${val ?? '—'}</span>
      ),
    },
    {
      title: '操作',
      dataIndex: 'id',
      key: 'action',
      width: '100px',
      render: (_, record) => (
        <div className="flex items-center gap-0.5">
          <button
            aria-label={`编辑模型 ${record.name}`}
            onClick={() => setEditTarget(record)}
            className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-primary/10 hover:text-primary cursor-pointer"
          >
            <Pencil className="size-4" />
          </button>
          <button
            aria-label={`删除模型 ${record.name}`}
            onClick={() => setDeleteTarget(record)}
            className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-danger/10 hover:text-danger cursor-pointer"
          >
            <Trash2 className="size-4" />
          </button>
        </div>
      ),
    },
    {
      title: '启用',
      dataIndex: 'enabled',
      key: 'enabled',
      width: '80px',
      render: (val, record) => (
        <Switch
          size="sm"
          isSelected={val}
          isDisabled={togglingId === record.id}
          onChange={() => handleToggle(record)}
        >
          <Switch.Content>
            <Switch.Control>
              <Switch.Thumb />
            </Switch.Control>
          </Switch.Content>
        </Switch>
      ),
    },
  ], [channelMap, togglingId]);

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">模型管理</Typography>
        <div className="flex items-center gap-2">
          <Button variant="secondary" size="md" onPress={() => performSearch(queryChannel, queryName, page)} isPending={refreshing}>
            <RotateCw className="size-4" />
          </Button>
          <Button variant="primary" size="md" onPress={() => setShowCreate(true)}>
            <Plus className="size-4" />
            新增模型
          </Button>
        </div>
      </div>

      {/* 筛选区：名称 + 渠道，点击查询按钮后按条件请求后端 */}
      <div className="model-filters flex items-center gap-2 mb-3">
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
          <button
            onClick={() => { setDraftName(''); setQueryName(''); }}
            className="flex size-6 items-center justify-center rounded text-muted hover:text-foreground cursor-pointer"
            aria-label="清除名称筛选"
          >
            <X className="size-3.5" />
          </button>
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
        <Button variant="primary" size="sm" onPress={handleSearch} isPending={loading}>
          <Search className="size-4" />
          查询
        </Button>
      </div>

      <DataTable
        dataSource={models}
        columns={columns}
        loading={loading}
        refreshing={refreshing}
        emptyText={queryName || queryChannel !== 'all'
          ? '没有符合条件的模型'
          : '暂无模型，点击右上角「新增模型」创建'}
      />

      {/* 分页 */}
      <div className="flex items-center justify-between mt-3">
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
