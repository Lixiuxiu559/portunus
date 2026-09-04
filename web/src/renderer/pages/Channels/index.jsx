import { useEffect, useState } from 'react';
import { Button, Switch, Typography, Card, toast } from '@heroui/react';
import { Plus, Trash2, RefreshCw, Pencil, RotateCw } from 'lucide-react';
import CreateChannelModal from './CreateChannelModal';
import DeleteChannelModal from './DeleteChannelModal';
import EditChannelModal from './EditChannelModal';
import ProviderIcon from '../../components/ProviderIcon';
import { listChannels, updateChannel, syncChannel } from '../../api';

// 协议类型标签颜色
const providerBadge = {
  openai: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400',
  openai_responses: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400',
  anthropic: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-400',
  gemini: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-400',
};

export default function Channels() {
  const [isOpen, setIsOpen] = useState(false);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [togglingId, setTogglingId] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [editTarget, setEditTarget] = useState(null);
  const [syncingId, setSyncingId] = useState(null);

  const fetchChannels = async (isRefresh = false) => {
    // 有数据时使用 refreshing（不遮挡内容），无数据时使用 loading（显示加载状态）
    const setRefreshState = isRefresh || channels.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    try {
      setChannels(await listChannels());
    } catch {
      setChannels([]);
    } finally {
      setRefreshState(false);
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

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex items-center justify-between mb-4 shrink-0">
        <Typography type="h2">渠道管理</Typography>
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="md"
            onPress={() => fetchChannels(true)}
            isPending={refreshing}
          >
            <RotateCw className="size-4" />
          </Button>
          <Button variant="primary" size="md" onPress={() => setIsOpen(true)}>
            <Plus className="size-4" />
            新增渠道
          </Button>
        </div>
      </div>

      {loading ? (
        <div className="flex justify-center py-12">
          <span className="text-muted">加载中…</span>
        </div>
      ) : channels.length === 0 ? (
        <div className="flex justify-center py-16 text-muted">
          暂无渠道，点击右上角「新增渠道」创建
        </div>
      ) : (
        <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-stretch">
            {channels.map((ch) => (
              <Card key={ch.id} className="gap-4 p-5">
                <Card.Header className="flex-row items-center justify-between gap-3 shrink-0">
                  <div className="flex items-center gap-3 min-w-0">
                    <ProviderIcon type={ch.type} className="size-6 shrink-0" />
                    <Typography className="font-medium text-lg truncate" title={ch.name}>
                      {ch.name}
                    </Typography>
                  </div>
                  <Switch
                    size="sm"
                    isSelected={ch.enabled}
                    isDisabled={togglingId === ch.id}
                    onChange={() => handleToggle(ch)}
                  >
                    <Switch.Content>
                      <Switch.Control>
                        <Switch.Thumb />
                      </Switch.Control>
                    </Switch.Content>
                  </Switch>
                </Card.Header>

                <Card.Content className="min-w-0">
                  <span
                    className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${
                      providerBadge[ch.type] || 'bg-default text-default-foreground'
                    }`}
                  >
                    {ch.type.replace(/_/g, ' ')}
                  </span>
                  <div className="mt-3">
                    <div className="text-xs text-muted mb-1">Base URL</div>
                    <span
                      className="block truncate font-mono text-xs text-foreground/70"
                      title={ch.base_url}
                    >
                      {ch.base_url}
                    </span>
                  </div>
                </Card.Content>

                <Card.Footer className="shrink-0">
                  <div className="flex items-center gap-1">
                    <button
                      aria-label={`同步渠道 ${ch.name} 模型`}
                      onClick={() => handleSync(ch)}
                      disabled={syncingId === ch.id}
                      className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-accent/10 hover:text-accent cursor-pointer disabled:opacity-50"
                    >
                      <RefreshCw className={`size-4 ${syncingId === ch.id ? 'animate-spin' : ''}`} />
                    </button>
                    <button
                      aria-label={`编辑渠道 ${ch.name}`}
                      onClick={() => setEditTarget(ch)}
                      className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-accent/10 hover:text-accent cursor-pointer"
                    >
                      <Pencil className="size-4" />
                    </button>
                    <button
                      aria-label={`删除渠道 ${ch.name}`}
                      onClick={() => setDeleteTarget(ch)}
                      className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-danger/10 hover:text-danger cursor-pointer"
                    >
                      <Trash2 className="size-4" />
                    </button>
                  </div>
                </Card.Footer>
              </Card>
            ))}
          </div>
        </div>
      )}

      <CreateChannelModal
        isOpen={isOpen}
        onOpenChange={setIsOpen}
        onCreated={() => fetchChannels(true)}
      />
      <DeleteChannelModal
        channel={deleteTarget}
        isOpen={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        onDeleted={() => fetchChannels(true)}
      />
      <EditChannelModal
        channel={editTarget}
        isOpen={editTarget !== null}
        onOpenChange={(open) => {
          if (!open) setEditTarget(null);
        }}
        onUpdated={() => fetchChannels(true)}
      />
    </div>
  );
}