import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Typography, Button, Card, Spinner, Input, TextField, toast } from '@heroui/react';
import { Download, RotateCw, CheckCircle, Tag, ExternalLink, RefreshCw } from 'lucide-react';
import { useUpdater } from '../hooks/useUpdater';
import { getSettings, setSyncInterval, syncNow } from '../api/setting';
import { getCachedResponse } from '../api/responseCache';
import { formatDateTime } from '../utils/dateTime';
import './settings.css';

const GITHUB_URL = 'https://github.com/Lixiuxiu559/portunus';

export default function Settings() {
  const { checking, available, downloading, downloaded, installing, latestVersion, progress, error, check, download, install } =
    useUpdater();

  const [cached] = useState(() => getCachedResponse('/settings'));
  const [syncInterval, setSyncIntervalState] = useState(cached ? String(cached.sync_interval ?? 0) : '');
  const [lastSyncAt, setLastSyncAt] = useState(cached?.last_sync_at ?? 0);
  const [savingInterval, setSavingInterval] = useState(false);
  const [syncing, setSyncing] = useState(false);

  // 应用版本仍从主进程读取；服务令牌与访问范围统一在服务接入页维护。
  const [appVersion, setAppVersion] = useState('');
  useEffect(() => {
    window.api?.getAppVersion?.().then((v) => setAppVersion(v ?? '')).catch(() => {});
  }, []);

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
      toast.danger('请输入非负整数分钟数');
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

  const openGitHub = (e) => {
    e.preventDefault();
    if (window.api?.openExternal) {
      window.api.openExternal(GITHUB_URL).catch(() => {});
    } else {
      // 纯浏览器（开发模式）下退化为新标签页打开
      window.open(GITHUB_URL, '_blank', 'noopener');
    }
  };

  const showDownloadProgress = downloading || downloaded;
  const downloadPercent = downloaded ? 100 : Math.round(progress.percent);
  let updateStatus = '发现新版本，正在准备后台下载…';
  if (error) updateStatus = `更新出错：${error}`;
  else if (installing) updateStatus = '正在重启并应用更新…';
  else if (downloaded) updateStatus = '下载完成，重启即可更新';
  else if (downloading) {
    updateStatus = progress.percent > 0
      ? `${downloadPercent}% · ${formatSpeed(progress.bytesPerSecond)}`
      : '准备下载…';
  }

  return (
    <div className="settings-page flex flex-col flex-1 min-h-0">
      <div className="settings-sections flex-1 min-h-0 overflow-y-auto no-scrollbar">
      {/* 上游模型同步 */}
      <Card className="settings-card gap-4 p-5">
        <Card.Header className="p-0">
          <Typography type="body-sm" className="settings-section-title text-muted">
            上游模型同步
          </Typography>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="settings-rows">
            {/* 自动同步间隔 */}
            <div className="settings-row">
              <div className="settings-row-copy">
                <Typography className="settings-label">自动同步间隔（分钟）</Typography>
                <Typography type="body-sm" className="settings-hint">
                  0 表示关闭自动同步；对所有开启自动同步的渠道生效
                </Typography>
              </div>
              <div className="settings-row-actions">
                <TextField
                  size="sm"
                  type="number"
                  min="0"
                  value={syncInterval}
                  onChange={setSyncIntervalState}
                  className="settings-interval-field"
                  aria-label="自动同步间隔（分钟）"
                >
                  <Input />
                </TextField>
                <Button className="settings-control" variant="secondary" size="sm" onPress={handleSaveInterval} isPending={savingInterval}>
                  保存
                </Button>
              </div>
            </div>

            {/* 上次同步时间 */}
            <div className="settings-row">
              <Typography className="settings-label">上次同步时间</Typography>
              <Typography type="body-sm" className="settings-value">
                {lastSyncAt > 0 ? formatDateTime(lastSyncAt * 1000) : '从未同步'}
              </Typography>
            </div>

            {/* 立即同步 */}
            <div className="settings-row">
              <Typography className="settings-label">立即同步</Typography>
              <Button className="settings-control" variant="primary" size="sm" onPress={handleSyncNow} isPending={syncing}>
                <RefreshCw className={`size-4 ${syncing ? 'animate-spin' : ''}`} />
                立即同步
              </Button>
            </div>
          </div>
        </Card.Content>
      </Card>

      <Card className="settings-card gap-4 p-5">
        <Card.Header className="p-0"><Typography type="body-sm" className="settings-section-title text-muted">服务接入</Typography></Card.Header>
        <Card.Content className="settings-access p-0">
          <Typography type="body-sm" className="settings-hint">网关地址、访问令牌、监听范围及 API / 客户端接入示例统一在服务接入页维护。</Typography>
          <Link to="/clients" className="settings-access-link text-sm text-accent hover:underline">前往服务接入 →</Link>
        </Card.Content>
      </Card>

      {/* 版本信息 */}
      <Card className="settings-card gap-4 p-5">
        <Card.Header className="p-0">
          <Typography type="body-sm" className="settings-section-title text-muted">
            关于 {window.api?.appName || "Portunus"}
          </Typography>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="settings-about">
            <div className="settings-about-copy">
              <div className="settings-version-details">
                <a
                  href={GITHUB_URL}
                  onClick={openGitHub}
                  aria-label="查看项目仓库"
                  title={GITHUB_URL}
                  className="settings-repository transition-opacity hover:opacity-80 hover:underline decoration-accent underline-offset-4"
                >
                  <GitHubIcon className="size-4 shrink-0" />
                  <span className="settings-repository-name text-sm text-accent">github.com/Lixiuxiu559/portunus</span>
                  <ExternalLink className="size-3.5 shrink-0 text-accent" />
                </a>
                <div className="settings-version">
                  <Tag className="size-3.5 shrink-0" />
                  <Typography className="text-sm text-muted">当前 v{appVersion || '—'}</Typography>
                </div>
                {latestVersion && (
                  <Typography
                    className={`settings-latest-version text-sm ${available ? 'text-accent' : 'text-muted'}`}
                  >
                    · 最新 v{latestVersion}
                    {available ? '（可更新）' : '（已是最新）'}
                  </Typography>
                )}
              </div>
            </div>
            {window.api?.updatesEnabled === false ? (
              <Typography className="text-xs text-muted">开发环境 · 13061 · 独立数据 · 禁用正式更新</Typography>
            ) : <div className="settings-update-actions">
              {checking && (
                <Spinner size="sm" />
              )}
              {!downloaded && !checking && (
                <Button className="settings-control" variant="tertiary" size="sm" onPress={check} isDisabled={downloading}>
                  <RotateCw className="size-4" />
                  检查更新
                </Button>
              )}
              {available && !downloaded && !checking && !downloading && error && (
                <Button className="settings-control" variant="primary" size="sm" onPress={download}>
                  <Download className="size-4" />
                  重新下载
                </Button>
              )}
              {downloaded && (
                <Button className="settings-control" variant="primary" size="sm" onPress={install} isDisabled={installing}>
                  <CheckCircle className="size-4" />
                  {installing ? '正在重启…' : '重启以更新'}
                </Button>
              )}
            </div>}
          </div>
        </Card.Content>

        {/* 发现新版本时即预留状态高度，开始下载、失败或完成都在原位更新。 */}
        {(available || downloaded || error) && (
          <div className="settings-update-status">
            <div
              className="w-full h-2 rounded-full bg-accent/10 overflow-hidden"
              role={showDownloadProgress ? 'progressbar' : undefined}
              aria-label={showDownloadProgress ? '更新下载进度' : undefined}
              aria-valuemin={showDownloadProgress ? 0 : undefined}
              aria-valuemax={showDownloadProgress ? 100 : undefined}
              aria-valuenow={showDownloadProgress ? downloadPercent : undefined}
            >
              <div
                className="h-full rounded-full bg-accent transition-all duration-300"
                style={{ width: `${downloaded ? 100 : error ? 0 : progress.percent}%` }}
              />
            </div>
            <Typography
              type="body-sm"
              className={`${error ? 'text-danger' : 'text-muted'} settings-update-message`}
              role="status"
              title={error ? `更新出错：${error}` : undefined}
            >
              {updateStatus}
            </Typography>
          </div>
        )}
      </Card>
      </div>
    </div>
  );
}

/** GitHub 品牌图标（lucide-react 已移除品牌图标，内联 SVG） */
function GitHubIcon({ className }) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" className={className} aria-hidden="true">
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z" />
    </svg>
  );
}

function formatSpeed(bytesPerSecond) {
  if (bytesPerSecond < 1024) return `${Math.round(bytesPerSecond)} B/s`;
  if (bytesPerSecond < 1024 * 1024) return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`;
  return `${(bytesPerSecond / (1024 * 1024)).toFixed(1)} MB/s`;
}
