import { useCallback, useEffect, useRef, useState } from 'react';
import { listQuotaAdapters, listQuotaSources, deleteQuotaSource, refreshQuotaSource } from '../../../api/quota';
import { createCachePoller, createRequestGate } from './quotaModel';

export default function useQuotaSources(channelId, onChanged) {
  const [adapters, setAdapters] = useState([]);
  const [sources, setSources] = useState([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(null);
  const readGate = useRef(createRequestGate());
  const actionGate = useRef(createRequestGate());
  const actionLock = useRef(false);
  const load = useCallback(async () => {
    const task = readGate.current.start();
    try {
      const [catalog, items] = await Promise.all([
        listQuotaAdapters({ signal: task.signal }), listQuotaSources(channelId, { signal: task.signal }),
      ]);
      if (task.current()) { setAdapters(catalog); setSources(items); setError(''); }
    } catch (err) { if (task.current()) setError(err.message || '查询来源读取失败'); }
    finally { if (task.current()) setLoading(false); }
  }, [channelId]);
  useEffect(() => { load(); return () => { readGate.current.cancel(); actionGate.current.cancel(); }; }, [load]);
  const inflight = sources.some((source) => source.state?.in_progress);
  useEffect(() => {
    const poller = createCachePoller({
      load, visible: () => !document.hidden, interval: () => inflight ? 1500 : 30000,
      cancel: () => readGate.current.cancel(),
    });
    poller.start();
    document.addEventListener('visibilitychange', poller.visibilityChanged);
    return () => { poller.stop(); document.removeEventListener('visibilitychange', poller.visibilityChanged); };
  }, [sources, inflight, load]);
  const changed = async () => { onChanged?.(); await load(); };
  const act = async (source, remove = false) => {
    if (actionLock.current) return;
    actionLock.current = true; setPending(source.id);
    const task = actionGate.current.start();
    try {
      if (remove) await deleteQuotaSource(source.id, { signal: task.signal });
      else {
        const updated = await refreshQuotaSource(source.id, { signal: task.signal });
        if (task.current()) setSources((old) => old.map((item) => item.id === source.id ? updated : item));
      }
      if (task.current()) await changed();
    } finally { actionLock.current = false; if (task.current()) setPending(null); }
  };
  return { adapters, sources, error, loading, pending, load, changed, refresh: (source) => act(source), remove: (source) => act(source, true) };
}
