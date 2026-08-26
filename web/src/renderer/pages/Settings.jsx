import { Typography, Button, Card, Spinner } from '@heroui/react';
import { Download, RotateCw, CheckCircle } from 'lucide-react';
import { useUpdater } from '../hooks/useUpdater';

const APP_VERSION = '0.1.0';

export default function Settings() {
  const { checking, available, downloaded, version, progress, error, check, download, install } =
    useUpdater();

  return (
    <div className="space-y-6">
      <Typography type="h2">系统设置</Typography>

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