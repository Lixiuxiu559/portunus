import { useEffect, useState } from 'react';
import { Button, Switch, Typography, Chip, toast } from '@heroui/react';
import { Plus, Trash2, Pencil, RotateCw } from 'lucide-react';
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

  const channelMap = Object.fromEntries(channels.map((c) => [c.id, c.name]));

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

  const columns = [
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
  ];

  return (
    <div>
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

      <DataTable
        dataSource={models}
        columns={columns}
        loading={loading}
        emptyText="暂无模型，点击右上角「新增模型」创建"
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
