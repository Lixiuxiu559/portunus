import { useEffect, useState, useCallback } from 'react';

/**
 * 监听 Electron 主进程的自动更新事件
 * @returns {{ checking, available, downloaded, version, progress, error, check, download, install }}
 */
export function useUpdater() {
  const [checking, setChecking] = useState(false);
  const [available, setAvailable] = useState(false);
  const [downloaded, setDownloaded] = useState(false);
  const [version, setVersion] = useState(null);
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
      setVersion(info.version);
    });

    window.api.onUpdateEvent('update:not-available', () => {
      setChecking(false);
      setAvailable(false);
    });

    window.api.onUpdateEvent('update:download-progress', (p) => {
      setProgress({ percent: p.percent, bytesPerSecond: p.bytesPerSecond });
    });

    window.api.onUpdateEvent('update:downloaded', (info) => {
      setDownloaded(true);
      setVersion(info.version);
    });

    window.api.onUpdateEvent('update:error', (msg) => {
      setChecking(false);
      setError(msg);
    });
  }, []);

  const check = useCallback(() => window.api?.checkForUpdate?.(), []);
  const download = useCallback(() => window.api?.downloadUpdate?.(), []);
  const install = useCallback(() => window.api?.installUpdate?.(), []);

  return { checking, available, downloaded, version, progress, error, check, download, install };
}