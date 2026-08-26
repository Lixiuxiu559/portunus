import { memo, useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Switch, Typography, Chip, toast, Select, ListBox, TextField, Input } from '@heroui/react';
import { Plus, Trash2, RotateCw, X, Search } from 'lucide-react';
import DataTable from '../../components/DataTable';
import CreateModelModal from './CreateModelModal';
import DeleteModelModal from './DeleteModelModal';
import { listModels, updateModel, createModel, deleteModel, listChannels } from '../../api';

export default function Models() {
  const [models, setModels] = useState([]);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [togglingId, setTogglingId] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [showCreate, setShowCreate] = useState(false);
  // 查询条件（点击查询按钮后才生效）
  const [queryChannel, setQueryChannel] = useState('all');
  const [queryName, setQueryName] = useState('');
  // 输入框的临时值，未点击查询前不触发请求
  const [draftName, setDraftName] = useState('');
  const [draftChannel, setDraftChannel] = useState('all');

  const fetchAll = async () => {
    setLoading(true);
    try {
      const start = Date.now();
      const [m, c] = await Promise.all([listModels(), listChannels()]);
      setModels(Array.isArray(m) ? m : []);
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
  const performSearch = useCallback(async (channel, name) => {
    setQueryChannel(channel);
    setQueryName(name.trim());
    const params = {};
    if (channel !== 'all') params.channel_id = channel;
    if (name.trim()) params.name = name.trim();
    setLoading(true);
    try {
      setModels(await listModels(params));
    } catch {
      setModels([]);
    } finally {
      setLoading(false);
    }
  }, []);

  // 点查询按钮 / 回车：按当前草稿值查询
  const handleSearch = useCallback(() => {
    performSearch(draftChannel, draftName);
  }, [performSearch, draftChannel, draftName]);

  // 切换渠道：立即请求（名称取当前输入框的草稿值）
  const handleChannelChange = useCallback(
    (channel) => {
      setDraftChannel(channel);
      performSearch(channel, draftName);
    },
    [performSearch, draftName],
  );

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

  const handleDelete = async (m) => {
    await deleteModel(m.id);
    setModels((prev) => prev.filter((x) => x.id !== m.id));
    toast.success('模型已删除');
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
      title: '操作',
      dataIndex: 'id',
      key: 'action',
      width: '60px',
      render: (_, record) => (
        <button
          aria-label={`删除模型 ${record.name}`}
          onClick={() => setDeleteTarget(record)}
          className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-danger/10 hover:text-danger cursor-pointer"
        >
          <Trash2 className="size-4" />
        </button>
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
          <Button variant="secondary" size="md" onPress={fetchAll} isPending={loading}>
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
        emptyText={queryName || queryChannel !== 'all'
          ? '没有符合条件的模型'
          : '暂无模型，点击右上角「新增模型」创建'}
      />

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
