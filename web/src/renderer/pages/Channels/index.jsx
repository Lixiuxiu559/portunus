import { useEffect, useState } from 'react';
import { Button, Typography, Card, Chip, Spinner, toast } from '@heroui/react';
import { Plus, Trash2, RefreshCw, Pencil, RotateCw } from 'lucide-react';
import CreateChannelModal from './CreateChannelModal';
import DeleteChannelModal from './DeleteChannelModal';
import EditChannelModal from './EditChannelModal';
import ProviderIcon from '../../components/ProviderIcon';
import IconButton from '../../components/IconButton';
import { listChannels, syncChannel } from '../../api';

export default function Channels() {
  const [isOpen, setIsOpen] = useState(false);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [editTarget, setEditTarget] = useState(null);
  const [syncingId, setSyncingId] = useState(null);
  // 首次加载失败标记：区分"真没数据"和"加载失败"，后者给重试入口
  const [loadError, setLoadError] = useState(false);

  const fetchChannels = async (isRefresh = false) => {
    // 有数据时使用 refreshing（不遮挡内容），无数据时使用 loading（显示加载状态）
    const setRefreshState = isRefresh || channels.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    try {
      setChannels(await listChannels());
      setLoadError(false);
    } catch {
      // 无数据时进入错误态而非伪造空态；已有数据时保留旧列表（toast 由拦截器提示）
      if (channels.length === 0) setLoadError(true);
    } finally {
      setRefreshState(false);
    }
  };

  useEffect(() => {
    fetchChannels();
  }, []);

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
      <div className="flex items-center justify-end mb-4 shrink-0">
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="md"
            aria-label="刷新渠道列表"
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
          <Spinner size="sm" />
        </div>
      ) : loadError && channels.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>渠道加载失败，请检查后端服务后重试</span>
          <Button variant="secondary" size="sm" onPress={() => fetchChannels()}>
            重试
          </Button>
        </div>
      ) : channels.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>暂无渠道</span>
          <Button variant="primary" size="sm" onPress={() => setIsOpen(true)}>
            <Plus className="size-4" />
            新增渠道
          </Button>
        </div>
      ) : (
        <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4 items-stretch">
            {channels.map((ch) => (
              <Card key={ch.id} className="gap-4 p-5">
                {/* 操作按钮上移标题行右侧：消除"按钮孤行"，与分组卡同模式 */}
                <Card.Header className="flex-row items-center justify-between gap-3 shrink-0">
                  <div className="flex items-center gap-3 min-w-0">
                    <ProviderIcon type={ch.type} className="size-6 shrink-0" />
                    <Typography className="font-medium text-lg truncate" title={ch.name}>
                      {ch.name}
                    </Typography>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <IconButton
                      label={`同步渠道 ${ch.name} 模型`}
                      onClick={() => handleSync(ch)}
                      disabled={syncingId === ch.id}
                    >
                      <RefreshCw className={`size-4 ${syncingId === ch.id ? 'animate-spin' : ''}`} />
                    </IconButton>
                    <IconButton label={`编辑渠道 ${ch.name}`} onClick={() => setEditTarget(ch)}>
                      <Pencil className="size-4" />
                    </IconButton>
                    <IconButton label={`删除渠道 ${ch.name}`} onClick={() => setDeleteTarget(ch)} danger>
                      <Trash2 className="size-4" />
                    </IconButton>
                  </div>
                </Card.Header>

                <Card.Content className="min-w-0">
                  <Chip variant="soft" size="sm">
                    {ch.type.replace(/_/g, ' ')}
                  </Chip>
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