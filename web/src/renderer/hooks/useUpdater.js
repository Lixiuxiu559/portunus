import { useEffect, useState, useCallback } from 'react';

/**
 * 监听 Electron 主进程的自动更新事件
 * 全平台统一「检查 → 下载 → 安装并重启」全自动流程（mac / win / linux）。
 * @returns {{ checking, available, downloaded, version, progress, error, check, download, install }}
 */
export function useUpdater() {
  const [checking, setChecking] = useState(false);
  const [available, setAvailable] = useState(false);
  const [downloaded, setDownloaded] = useState(false);
  // 远端最新版本号：available（有新版）、not-available（已是最新）、downloaded 时都会带出
  const [latestVersion, setLatestVersion] = useState(null);
  const [progress, setProgress] = useState({ percent: 0, bytesPerSecond: 0 });
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!window.api?.onUpdateEvent) return;

    window.api.onUpdateEvent('update:checking', () => {
      setChecking(true);
      setError(null);
    });

    window.api.onUpdateEvent('update:available', (info) => {
      setChecking(false);
      setAvailable(true);
      setLatestVersion(info.version);
    });

    window.api.onUpdateEvent('update:not-available', (info) => {
      setChecking(false);
      setAvailable(false);
      setLatestVersion(info?.version ?? null);
    });

    window.api.onUpdateEvent('update:download-progress', (p) => {
      setProgress({ percent: p.percent, bytesPerSecond: p.bytesPerSecond });
    });

    window.api.onUpdateEvent('update:downloaded', (info) => {
      setDownloaded(true);
      setLatestVersion(info.version);
    });

    window.api.onUpdateEvent('update:error', (msg) => {
      setChecking(false);
      setError(msg);
    });
  }, []);

  // 点击必须有反馈：promise 的 rejection 直接落到 error 状态，
  // 不能只依赖 update:error 事件（事件管道可能没注册，dev 下就是黑洞）。
  const check = useCallback(
    () =>
      window.api?.checkForUpdate?.().catch((err) => {
        setChecking(false);
        setError(err?.message ?? String(err));
      }),
    [],
  );
  const download = useCallback(
    () =>
      window.api?.downloadUpdate?.().catch((err) => {
        setError(err?.message ?? String(err));
      }),
    [],
  );
  const install = useCallback(
    () =>
      window.api?.installUpdate?.().catch((err) => {
        setError(err?.message ?? String(err));
      }),
    [],
  );

  return { checking, available, downloaded, latestVersion, progress, error, check, download, install };
}