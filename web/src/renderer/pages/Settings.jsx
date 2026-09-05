import { useEffect, useState } from 'react';
import { Typography, Button, Card, Spinner, Input, TextField, Modal, toast } from '@heroui/react';
import { Download, RotateCw, CheckCircle, Tag, ExternalLink, RefreshCw, Copy, Check } from 'lucide-react';
import { useUpdater } from '../hooks/useUpdater';
import IconButton from '../components/IconButton';
import { getSettings, setSyncInterval, syncNow } from '../api/setting';
import { getAPIKey, regenerateAPIKey } from '../api/apikey';

const GITHUB_URL = 'https://github.com/Lixiuxiu559/portunus';
const RELEASES_URL = `${GITHUB_URL}/releases/latest`;

export default function Settings() {
  const { checking, available, downloaded, version, progress, error, check, download, install, isMac } =
    useUpdater();

  const [syncInterval, setSyncIntervalState] = useState('');
  const [lastSyncAt, setLastSyncAt] = useState(0);
  const [savingInterval, setSavingInterval] = useState(false);
  const [syncing, setSyncing] = useState(false);

  const [apiKey, setApiKey] = useState(null);
  const [copied, setCopied] = useState(false);
  const [regenerateOpen, setRegenerateOpen] = useState(false);
  const [regenerating, setRegenerating] = useState(false);

  // 应用版本（从主进程 package.json 读，消除前端双写）
  const [appVersion, setAppVersion] = useState('');
  // 后端监听范围：local（只本机）/ lan（局域网）
  const [networkMode, setNetworkModeState] = useState('local');
  const [savingMode, setSavingMode] = useState(false);

  const loadAPIKey = async () => {
    try {
      const k = await getAPIKey();
      setApiKey(k ?? null);
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  useEffect(() => {
    loadAPIKey();
  }, []);

  // 读应用版本与网络监听范围（Electron 环境下才有）
  useEffect(() => {
    window.api?.getAppVersion?.().then((v) => setAppVersion(v ?? '')).catch(() => {});
    window.api?.getNetworkMode?.().then((m) => setNetworkModeState(m ?? 'local')).catch(() => {});
  }, []);

  const handleCopyKey = async (key) => {
    try {
      await navigator.clipboard.writeText(key);
    } catch {
      // 降级方案
      const input = document.createElement('input');
      input.value = key;
      document.body.appendChild(input);
      input.select();
      document.execCommand('copy');
      document.body.removeChild(input);
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  const handleRegenerate = async () => {
    setRegenerating(true);
    try {
      const k = await regenerateAPIKey();
      setApiKey(k);
      setRegenerateOpen(false);
      toast.success('令牌已重新生成');
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setRegenerating(false);
    }
  };

  const maskKey = (key) => {
    if (!key) return '';
    if (key.length <= 8) return key;
    return key.slice(0, 4) + '****' + key.slice(-4);
  };

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

  const openGitHub = (e) => {
    e.preventDefault();
    if (window.api?.openExternal) {
      window.api.openExternal(GITHUB_URL).catch(() => {});
    } else {
      // 纯浏览器（开发模式）下退化为新标签页打开
      window.open(GITHUB_URL, '_blank', 'noopener');
    }
  };

  const openDownloadPage = (e) => {
    e.preventDefault?.();
    if (window.api?.openExternal) {
      window.api.openExternal(RELEASES_URL).catch(() => {});
    } else {
      window.open(RELEASES_URL, '_blank', 'noopener');
    }
  };

  const saveNetworkMode = async (mode) => {
    if (mode === networkMode) return;
    setSavingMode(true);
    try {
      const res = await window.api?.setNetworkMode?.(mode);
      setNetworkModeState(res?.networkMode ?? mode);
      toast.success(res?.restarted ? '已保存，后端已自动重启' : '已保存');
    } catch (err) {
      toast.error(`保存失败：${err?.message ?? ''}`);
    } finally {
      setSavingMode(false);
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

      {/* 令牌管理 */}
      <Card className="gap-4 p-5">
        <Card.Header className="p-0">
          <Typography type="body-sm" className="text-muted">
            令牌管理
          </Typography>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="flex flex-col gap-3">
            {apiKey ? (
              <>
                <div className="flex items-center justify-between">
                  <div className="flex flex-col gap-0.5 min-w-0">
                    <Typography type="body-xs" className="text-muted font-mono">
                      {maskKey(apiKey.key)}
                    </Typography>
                    <Typography type="body-xs" className="text-muted">
                      创建于 {new Date(apiKey.created_at).toLocaleDateString()}
                    </Typography>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <IconButton label="复制令牌" onClick={() => handleCopyKey(apiKey.key)}>
                      {copied ? <Check className="size-4 text-success" /> : <Copy className="size-4" />}
                    </IconButton>
                    <Button variant="secondary" size="sm" onPress={() => setRegenerateOpen(true)}>
                      <RefreshCw className="size-4" />
                      重新生成
                    </Button>
                  </div>
                </div>
                <Typography type="body-xs" className="text-muted">
                  重新生成后旧令牌立即失效，已配置的客户端需重新写入。
                </Typography>
              </>
            ) : (
              <Typography type="body-sm" className="text-muted">加载中…</Typography>
            )}
          </div>
        </Card.Content>
      </Card>

      {/* 重新生成确认弹窗 */}
      <Modal.Backdrop isOpen={regenerateOpen} onOpenChange={setRegenerateOpen}>
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.CloseTrigger />
            <Modal.Header>
              <Modal.Heading>重新生成令牌</Modal.Heading>
            </Modal.Header>
            <Modal.Body>
              <Typography color="muted">
                重新生成后旧令牌立即失效，已配置的 Claude Code / Codex 客户端需重新写入，是否继续？
              </Typography>
            </Modal.Body>
            <Modal.Footer>
              <Button slot="close" variant="secondary">
                取消
              </Button>
              <Button variant="danger" isPending={regenerating} onPress={handleRegenerate}>
                {regenerating ? '生成中…' : '重新生成'}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>

      {/* 网络访问（后端 sidecar 监听范围） */}
      <Card className="gap-4 p-5">
        <Card.Header className="p-0">
          <Typography type="body-sm" className="text-muted">
            网络访问
          </Typography>
        </Card.Header>
        <Card.Content className="p-0">
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <div className="flex flex-col gap-1">
                <Typography className="font-medium">后端监听范围</Typography>
                <Typography type="body-sm" className="text-muted">
                  仅本机：只这台机器能连；局域网：同网络下其他设备也能用你的聚合服务
                </Typography>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <Button
                  size="sm"
                  variant={networkMode === 'local' ? 'primary' : 'secondary'}
                  onPress={() => saveNetworkMode('local')}
                >
                  仅本机
                </Button>
                <Button
                  size="sm"
                  variant={networkMode === 'lan' ? 'primary' : 'secondary'}
                  onPress={() => saveNetworkMode('lan')}
                >
                  局域网
                </Button>
              </div>
            </div>
            {savingMode && (
              <Typography type="body-sm" className="text-muted">
                保存中，后端正在自动重启…
              </Typography>
            )}
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
              <div className="flex items-center gap-2">
                <a
                  href={GITHUB_URL}
                  onClick={openGitHub}
                  aria-label="查看开源仓库"
                  title={GITHUB_URL}
                  className="inline-flex items-center gap-1.5 transition-opacity hover:opacity-80 hover:underline decoration-accent underline-offset-4"
                >
                  <GitHubIcon className="size-4" />
                  <span className="text-sm text-accent">github.com/Lixiuxiu559/portunus</span>
                  <ExternalLink className="size-3.5 text-accent" />
                </a>
                <Tag className="size-4 ml-2" />
                <Typography className="text-sm text-muted">{appVersion || '—'}</Typography>
              </div>
              {available && version && (
                <Typography type="body-sm" className="text-accent mt-1">
                  新版本 {version} 可用
                </Typography>
              )}
              {error && (
                <Typography type="body-sm" className="text-danger mt-1">
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
              {available && !downloaded && !checking && (isMac ? (
                <Button variant="primary" size="sm" onPress={openDownloadPage}>
                  <ExternalLink className="size-4" />
                  去下载
                </Button>
              ) : (
                <Button variant="primary" size="sm" onPress={download}>
                  <Download className="size-4" />
                  下载更新
                </Button>
              ))}
              {downloaded && !isMac && (
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
                className="h-full rounded-full bg-accent transition-all duration-300"
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