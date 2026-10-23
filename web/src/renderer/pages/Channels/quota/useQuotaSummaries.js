import { useCallback, useEffect, useRef, useState } from 'react';
import { getQuotaSummaries, refreshQuotaSource } from '../../../api/quota';
import { createCachePoller, createRequestGate } from './quotaModel';
import { getCachedResponse } from '../../../api/responseCache';

function readSummaries(channels) {
  if (!channels.length) return null;
  const entries = [];
  for (let i = 0; i < channels.length; i += 100) {
    const channel_ids = channels.slice(i, i + 100).map((channel) => channel.id).join(',');
    const batch = getCachedResponse('/quota/summaries', { channel_ids });
    if (batch === undefined) return null;
    entries.push(...batch);
  }
  return Object.fromEntries(entries.map((entry) => [entry.channel_id, entry.sources || []]));
}

const refreshMessage = (err) => {
  if (err?.status === 429) return '查询频率受限，请稍后刷新';
  if (err?.status === 404) return '查询来源已变更，请重新打开配置';
  if (err?.status === 409) return '查询配置已更新，请稍后刷新';
  return '刷新失败，请稍后重试';
};

export default function useQuotaSummaries(channels) {
  const [cached] = useState(() => readSummaries(channels));
  const [summaries, setSummaries] = useState(cached ?? {});
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(channels.length > 0 && !cached);
  const [pending, setPending] = useState({});
  const [refreshErrors, setRefreshErrors] = useState({});
  const gate = useRef(createRequestGate());
  const commands = useRef(new Map());
  const lifecycle = useRef({ active: false, epoch: 0 });
  const current = useRef(cached ?? {});
  // 只有首次成功读取前的全局 loading 才代表"余额尚未载入"。
  // 刷新后的静默重读不能把其他没有查询来源的卡片切到"正在读取余额"，
  // 否则点击一个卡片的刷新会让相邻卡片文字闪动。
  const loaded = useRef(!!cached);
  const ids = channels.map((channel) => channel.id).join(',');
  const cancelCommands = useCallback(() => {
    lifecycle.current.epoch += 1;
    for (const task of commands.current.values()) task.controller.abort();
    commands.current.clear();
  }, []);
  const load = useCallback(async (invalidate = false) => {
    if (!lifecycle.current.active) return;
    if (invalidate) {
      cancelCommands();
      gate.current.cancel();
      const snapshot = readSummaries(ids.split(',').filter(Boolean).map((id) => ({ id })));
      current.current = snapshot ?? {}; setSummaries(snapshot ?? {}); setPending({}); setRefreshErrors({});
      loaded.current = !!snapshot;
    }
    // POST 结束前的缓存可能仍是旧值；结束后统一重读，轮询不触发上游查询。
    if (commands.current.size) return;
    if (!ids) { setLoading(false); return; }
    const task = gate.current.start();
    // 静默重读只更新数据，不改变全局载入态（见 loaded 注释）。
    setLoading(!loaded.current);
    try {
      const channelIds = ids.split(',');
      const batches = [];
      for (let i = 0; i < channelIds.length; i += 100) batches.push(channelIds.slice(i, i + 100));
      const results = await Promise.all(batches.map((batch) => getQuotaSummaries(batch, { signal: task.signal })));
      if (!task.current() || !lifecycle.current.active) return;
      const next = Object.fromEntries(results.flat().map((entry) => [entry.channel_id, entry.sources || []]));
      current.current = next; setSummaries(next); setError(false);
      loaded.current = true;
    } catch { if (task.current() && lifecycle.current.active) setError(true); }
    finally { if (task.current() && lifecycle.current.active) setLoading(false); }
  }, [ids, cancelCommands]);
  useEffect(() => {
    lifecycle.current.active = true;
    // 渠道更新（包括 Key 保存）隔离全部旧命令，再重读缓存。
    load(true);
    return () => {
      lifecycle.current.active = false;
      cancelCommands(); gate.current.cancel();
    };
  }, [channels, load, cancelCommands]);
  const refresh = useCallback(async (source) => {
    const sourceId = source?.id;
    if (!lifecycle.current.active || !Number.isSafeInteger(sourceId) || sourceId <= 0 || commands.current.has(sourceId)) return;
    const channelId = ids.split(',').find((id) => (current.current[id] || []).some((item) => item.id === sourceId));
    if (!channelId) return;
    const task = { controller: new AbortController(), epoch: lifecycle.current.epoch };
    const isCurrent = () => lifecycle.current.active && lifecycle.current.epoch === task.epoch && commands.current.get(sourceId) === task;
    commands.current.set(sourceId, task);
    gate.current.cancel(); setLoading(false);
    setPending((old) => ({ ...old, [sourceId]: true }));
    setRefreshErrors((old) => { const next = { ...old }; delete next[sourceId]; return next; });
    try {
      const updated = await refreshQuotaSource(sourceId, { signal: task.controller.signal });
      if (!isCurrent()) return;
      gate.current.cancel();
      const next = { ...current.current, [channelId]: current.current[channelId].map((item) => item.id === sourceId ? updated : item) };
      current.current = next; setSummaries(next);
      return updated;
    } catch (err) {
      if (isCurrent()) setRefreshErrors((old) => ({ ...old, [sourceId]: refreshMessage(err) }));
    } finally {
      // 旧请求即使忽略 Abort，也不能清掉同来源的新命令或覆盖新渠道数据。
      if (isCurrent()) {
        commands.current.delete(sourceId);
        setPending((old) => { const next = { ...old }; delete next[sourceId]; return next; });
        if (!commands.current.size) load();
      }
    }
  }, [ids, load]);
  useEffect(() => {
    const poller = createCachePoller({
      load, visible: () => !document.hidden,
      interval: () => Object.values(current.current).some((sources) => sources.some((source) => source.state?.in_progress)) ? 1500 : 30000,
      cancel: () => gate.current.cancel(),
    });
    poller.start();
    document.addEventListener('visibilitychange', poller.visibilityChanged);
    return () => { poller.stop(); document.removeEventListener('visibilitychange', poller.visibilityChanged); };
  }, [load, summaries]);
  return { summaries, error, loading, pending, refreshErrors, refresh, invalidate: () => load(true) };
}
