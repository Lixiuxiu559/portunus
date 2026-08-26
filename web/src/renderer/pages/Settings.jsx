import { useEffect, useState } from 'react';
import { Typography, Button, Card, Spinner, Input, TextField, toast } from '@heroui/react';
import { Download, RotateCw, CheckCircle, RefreshCw } from 'lucide-react';
import { useUpdater } from '../hooks/useUpdater';
import { getSettings, setSyncInterval, syncNow } from '../api/setting';

const APP_VERSION = '0.1.0';

export default function Settings() {
  const { checking, available, downloaded, version, progress, error, check, download, install } =
    useUpdater();

  const [syncInterval, setSyncIntervalState] = useState('');
  const [lastSyncAt, setLastSyncAt] = useState(0);
  const [savingInterval, setSavingInterval] = useState(false);
  const [syncing, setSyncing] = useState(false);

  const loadSyncSettings = async () => {
    try {
      const s = await getSettings();
      setSyncIntervalState(String(s.sync_interval ?? 0));
      setLastSyncAt(s.last_sync_at ?? 0);
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  useEffect(() => {
    loadSyncSettings();
  }, []);

  const handleSaveInterval = async () => {
    const minutes = parseInt(syncInterval, 10);
    if (Number.isNaN(minutes) || minutes < 0) {
      toast.error('请输入非负整数分钟数');
      return;
    }
    setSavingInterval(true);
    try {
      await setSyncInterval(minutes);
      toast.success('同步间隔已保存');
      loadSyncSettings();
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setSavingInterval(false);
    }
  };

  const handleSyncNow = async () => {
    setSyncing(true);
    try {
      const res = await syncNow();
      toast.success(`同步完成，共 ${res.synced ?? 0} 个渠道，新增 ${res.added ?? 0} 个模型`);
      loadSyncSettings();
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setSyncing(false);
    }
  };

  return (
    <div className="space-y-6">
      <Typography type="h2">系统设置</Typography>

      {/* 渠道同步 */}
      <Card className="gap-4 p-5">
        <Card.Header className="p-0">
          <Typography type="body-sm" className="text-muted">
            渠道同步
          </Typography>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="flex flex-col gap-4">
            {/* 自动同步间隔 */}
            <div className="flex items-center justify-between">
              <div className="flex flex-col gap-1">
                <Typography className="font-medium">自动同步间隔（分钟）</Typography>
                <Typography type="body-sm" className="text-muted">
                  0 表示关闭自动同步；对所有开启自动同步的渠道生效
                </Typography>
              </div>
              <div className="flex items-center gap-2">
                <TextField
                  size="sm"
                  type="number"
                  min="0"
                  value={syncInterval}
                  onChange={setSyncIntervalState}
                  className="w-24"
                  aria-label="自动同步间隔（分钟）"
                >
                  <Input />
                </TextField>
                <Button variant="secondary" size="sm" onPress={handleSaveInterval} isPending={savingInterval}>
                  保存
                </Button>
              </div>
            </div>

            {/* 上次同步时间 */}
            <div className="flex items-center justify-between">
              <Typography className="font-medium">上次同步时间</Typography>
              <Typography type="body-sm" className="text-muted">
                {lastSyncAt > 0 ? new Date(lastSyncAt * 1000).toLocaleString() : '从未同步'}
              </Typography>
            </div>

            {/* 立即同步 */}
            <div className="flex items-center justify-between">
              <Typography className="font-medium">立即同步</Typography>
              <Button variant="primary" size="sm" onPress={handleSyncNow} isPending={syncing}>
                <RefreshCw className={`size-4 ${syncing ? 'animate-spin' : ''}`} />
                立即同步
              </Button>
            </div>
          </div>
        </Card.Content>
      </Card>

      {/* 版本信息 */}
      <Card className="gap-4 p-5">
        <Card.Header className="p-0">
          <Typography type="body-sm" className="text-muted">
            关于 Portunus
          </Typography>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="flex items-center justify-between">
            <div>
              <Typography className="font-medium">版本 {APP_VERSION}</Typography>
              {available && version && (
                <Typography type="body-sm" className="text-primary mt-1">
                  新版本 {version} 可用
                </Typography>
              )}
              {error && (
                <Typography type="body-sm" className="text-red-500 mt-1">
                  更新出错: {error}
                </Typography>
              )}
            </div>
            <div className="flex items-center gap-2">
              {checking && (
                <Spinner size="sm" />
              )}
              {!downloaded && !checking && (
                <Button variant="tertiary" size="sm" onPress={check}>
                  <RotateCw className="size-4" />
                  检查更新
                </Button>
              )}
              {available && !downloaded && !checking && (
                <Button variant="primary" size="sm" onPress={download}>
                  <Download className="size-4" />
                  下载更新
                </Button>
              )}
              {downloaded && (
                <Button variant="primary" size="sm" onPress={install}>
                  <CheckCircle className="size-4" />
                  安装并重启
                </Button>
              )}
            </div>
          </div>
        </Card.Content>

        {progress.percent > 0 && progress.percent < 100 && (
          <div className="mt-1">
            <div className="w-full h-2 rounded-full bg-accent/10 overflow-hidden">
              <div
                className="h-full rounded-full bg-primary transition-all duration-300"
                style={{ width: `${progress.percent}%` }}
              />
            </div>
            <Typography type="body-sm" className="text-muted mt-1">
              {Math.round(progress.percent)}% · {formatSpeed(progress.bytesPerSecond)}
            </Typography>
          </div>
        )}
      </Card>
    </div>
  );
}

function formatSpeed(bytesPerSecond) {
  if (bytesPerSecond < 1024) return `${Math.round(bytesPerSecond)} B/s`;
  if (bytesPerSecond < 1024 * 1024) return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`;
  return `${(bytesPerSecond / (1024 * 1024)).toFixed(1)} MB/s`;
}