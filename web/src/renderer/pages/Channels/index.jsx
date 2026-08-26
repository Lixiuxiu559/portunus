import { useEffect, useState } from 'react';
import { Button, Switch, Typography, toast } from '@heroui/react';
import { Plus, Trash2, RefreshCw, Pencil } from 'lucide-react';
import DataTable from '../../components/DataTable.tsx';
import CreateChannelModal from './CreateChannelModal';
import DeleteChannelModal from './DeleteChannelModal';
import EditChannelModal from './EditChannelModal';
import { listChannels, updateChannel, syncChannel } from '../../api';

export default function Channels() {
  const [isOpen, setIsOpen] = useState(false);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [togglingId, setTogglingId] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [editTarget, setEditTarget] = useState(null);
  const [syncingId, setSyncingId] = useState(null);

  const fetchChannels = async () => {
    setLoading(true);
    try {
      setChannels(await listChannels());
    } catch {
      setChannels([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchChannels();
  }, []);

  const handleToggle = async (ch) => {
    setTogglingId(ch.id);
    try {
      await updateChannel(ch.id, { enabled: !ch.enabled });
      setChannels((prev) =>
        prev.map((c) => (c.id === ch.id ? { ...c, enabled: !c.enabled } : c)),
      );
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setTogglingId(null);
    }
  };

  const handleSync = async (ch) => {
    setSyncingId(ch.id);
    try {
      const res = await syncChannel(ch.id);
      toast.success(`同步完成，新增 ${res.added ?? 0} 个模型`);
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setSyncingId(null);
    }
  };

  const columns = [
    {
      title: '渠道名称',
      dataIndex: 'name',
      key: 'name',
      render: (_, record) => (
        <span className="font-medium">{record.name}</span>
      ),
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (val) => (
        <span className="inline-block rounded-full bg-default px-2 py-0.5 text-xs text-default-foreground">
          {val}
        </span>
      ),
    },
    {
      title: 'Base URL',
      dataIndex: 'base_url',
      key: 'base_url',
      copy: true,
      render: (val) => (
        <span className="text-muted truncate max-w-[300px] inline-block" title={val}>
          {val}
        </span>
      ),
    },
    {
      title: '操作',
      dataIndex: 'id',
      key: 'action',
      width: '140px',
      render: (_, record) => (
        <div className="flex items-center gap-1">
          <button
            aria-label={`同步渠道 ${record.name} 模型`}
            onClick={() => handleSync(record)}
            disabled={syncingId === record.id}
            className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-accent/10 hover:text-accent cursor-pointer disabled:opacity-50"
          >
            <RefreshCw className={`size-4 ${syncingId === record.id ? 'animate-spin' : ''}`} />
          </button>
          <button
            aria-label={`编辑渠道 ${record.name}`}
            onClick={() => setEditTarget(record)}
            className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-accent/10 hover:text-accent cursor-pointer"
          >
            <Pencil className="size-4" />
          </button>
          <button
            aria-label={`删除渠道 ${record.name}`}
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
  ];

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">渠道管理</Typography>
        <Button variant="primary" size="md" onPress={() => setIsOpen(true)}>
          <Plus className="size-4" />
          新增渠道
        </Button>
      </div>

      <DataTable
        dataSource={channels}
        columns={columns}
        loading={loading}
        emptyText="暂无渠道，点击右上角「新增渠道」创建"
      />

      <CreateChannelModal
        isOpen={isOpen}
        onOpenChange={setIsOpen}
        onCreated={fetchChannels}
      />
      <DeleteChannelModal
        channel={deleteTarget}
        isOpen={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        onDeleted={fetchChannels}
      />
      <EditChannelModal
        channel={editTarget}
        isOpen={editTarget !== null}
        onOpenChange={(open) => {
          if (!open) setEditTarget(null);
        }}
        onUpdated={fetchChannels}
      />
    </div>
  );
}