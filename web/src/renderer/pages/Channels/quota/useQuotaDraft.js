import { useEffect, useRef, useState } from 'react';
import { createQuotaSource, updateQuotaSource, previewQuotaSource, refreshQuotaSource, detectNewAPIQuota } from '../../../api/quota';
import { headersWithoutToken } from './customQuotaModel';
import { createNewAPIDetector } from './newAPIDetection';
import { createRequestGate, quotaDraft, quotaPayload, switchQuotaAdapter } from './quotaModel';

export default function useQuotaDraft({ channelId, source, adapters, onSaved, onCommitted }) {
  const [draft, setDraft] = useState(() => quotaDraft(source, adapters.find((adapter) => adapter.id === source?.adapter_id) || adapters[0]));
  const draftRef = useRef(draft);
  const [detection, setDetection] = useState({ status: 'idle' });
  const detector = useRef(null);
  if (!detector.current) detector.current = createNewAPIDetector({
    readDraft: () => draftRef.current, detect: detectNewAPIQuota,
    applyOptions: (options) => patch({ options }, true), onChange: setDetection,
  });
  const [preview, setPreview] = useState(null);
  const [error, setError] = useState('');
  const [pending, setPending] = useState('');
  const gate = useRef(createRequestGate());
  const lock = useRef(false);
  useEffect(() => {
    detector.current.run();
    return () => { gate.current.cancel(); detector.current.cancel(); };
  }, []);
  const patch = (values, fromDetection = false) => {
    const old = draftRef.current;
    const next = { ...old, ...values };
    if (!fromDetection && (next.adapter_id !== old.adapter_id || next.query_base_url !== old.query_base_url
      || next.options.quota_per_unit !== old.options.quota_per_unit || next.options.display_unit !== old.options.display_unit)) detector.current.reset();
    gate.current.cancel(); setPreview(null); setError(''); setPending('');
    draftRef.current = next; setDraft(next);
  };
  const chooseAdapter = (id) => {
    const adapter = adapters.find((item) => item.id === id);
    if (adapter) patch(switchQuotaAdapter(draftRef.current, adapters.find((item) => item.id === draftRef.current.adapter_id), adapter));
  };
  const chooseCredential = (mode) => {
    const values = { credential_mode: mode, token: '', clearToken: false };
    if (mode === 'none' && draft.options.custom) {
      values.customEditors = { ...draft.customEditors, headers: headersWithoutToken(draft.customEditors.headers) };
    }
    patch(values);
  };
  const run = async (mode) => {
    if (lock.current) return;
    lock.current = true;
    detector.current.reset();
    const task = gate.current.start();
    setPending(mode); setError(''); setPreview(null);
    try {
      const payload = quotaPayload(draft, source, mode === 'query');
      if (mode === 'preview') {
        const view = await previewQuotaSource({ ...payload, channel_id: channelId, ...(source ? { source_id: source.id } : {}) }, { signal: task.signal });
        if (task.current()) setPreview(view);
      } else {
        const saved = source ? await updateQuotaSource(source.id, payload, { signal: task.signal }) : await createQuotaSource(channelId, payload, { signal: task.signal });
        if (!task.current()) return;
        // 保存已经生效；即便立即查询失败也必须失效列表缓存。
        onCommitted?.();
        if (mode === 'query') {
          let queryError;
          try { await refreshQuotaSource(saved.id, { signal: task.signal }); }
          catch (err) { queryError = `配置已保存，但立即查询未完成：${err.message || '请稍后重试'}`; }
          if (task.current()) onSaved(queryError);
        } else onSaved();
      }
    } catch (err) { if (task.current()) setError(err.status === 409 ? '配置已被修改，请关闭编辑后重新加载，避免覆盖新配置。' : err.message || '查询操作失败'); }
    finally { lock.current = false; if (task.current()) setPending(''); }
  };
  return { draft, patch, detection, detect: () => detector.current.run(), retryDetection: () => detector.current.run(true), applyDetection: () => detector.current.apply(), chooseAdapter, chooseCredential, preview, error, pending, run };
}
