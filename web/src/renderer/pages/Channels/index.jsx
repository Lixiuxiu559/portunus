import { useEffect, useState } from 'react';
import { Button, Typography, Spinner } from '@heroui/react';
import { Plus, X } from 'lucide-react';
import CreateChannelModal from './CreateChannelModal';
import DeleteChannelModal from './DeleteChannelModal';
import { listChannels, updateChannel } from '../../api';

export default function Channels() {
  const [isOpen, setIsOpen] = useState(false);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [togglingId, setTogglingId] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);

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

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">渠道管理</Typography>
        <Button variant="primary" size="md" onPress={() => setIsOpen(true)}>
          <Plus className="size-4" />
          新增渠道
        </Button>
      </div>

      {loading ? (
        <div className="flex justify-center py-12">
          <Spinner />
        </div>
      ) : channels.length === 0 ? (
        <div className="flex justify-center py-16 text-muted">
          <Typography>暂无渠道，点击右上角「新增渠道」创建</Typography>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {channels.map((ch) => (
            <div
              key={ch.id}
              className="group relative flex flex-col gap-3 rounded-xl bg-accent/10 shadow-md shadow-accent/5 p-4"
            >
              <button
                aria-label={`删除渠道 ${ch.name}`}
                onClick={() => setDeleteTarget(ch)}
                className="absolute right-2 top-2 flex size-6 items-center justify-center rounded-full text-muted transition-colors hover:bg-default hover:text-foreground cursor-pointer"
              >
                <X className="size-3.5" />
              </button>
              <div className="flex items-start justify-between gap-2">
                <div className="flex items-center gap-2 min-w-0">
                  <Typography className="font-medium truncate">{ch.name}</Typography>
                  <span className="shrink-0 rounded-full bg-default px-2 py-0.5 text-xs text-default-foreground">
                    {ch.type}
                  </span>
                </div>
              </div>
              <Typography color="muted" type="body-sm" className="truncate">
                {ch.base_url}
              </Typography>
              <div className="mt-auto flex justify-end">
                <Button
                  size="sm"
                  variant={ch.enabled ? 'primary' : 'ghost'}
                  isPending={togglingId === ch.id}
                  onPress={() => handleToggle(ch)}
                  className="w-fit"
                >
                  {ch.enabled ? '启用' : '停用'}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

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
    </div>
  );
}