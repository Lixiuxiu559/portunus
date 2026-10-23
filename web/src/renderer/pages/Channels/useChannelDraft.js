import { useEffect, useRef, useState } from 'react';
import { listUpstreamPresets, previewModels, resolveChannel, testChannel } from '../../api';
import { applyProfile, connectionIdentity, connectionPayload, createDraft, isCurrentRequest, normalizeModels } from './channelDraft';

// 所有请求绑定草稿代次；修改连接时同步取消，防止旧结果覆盖新配置。
export default function useChannelDraft(channel) {
  const [draft, setDraft] = useState(() => createDraft(channel));
  const draftRef = useRef(draft);
  const [presets, setPresets] = useState([]);
  const [iconPending, setIconPending] = useState(false);
  const [presetError, setPresetError] = useState('');
  const [pending, setPending] = useState({});
  const [results, setResults] = useState({});
  const [candidates, setCandidates] = useState([]);
  const [selected, setSelected] = useState([]);
  const requests = useRef({});
  const generation = useRef(0);
  const serial = useRef(0);
  const mounted = useRef(true);

  const cancel = (kind) => {
    requests.current[kind]?.controller.abort();
    delete requests.current[kind];
    setPending((old) => ({ ...old, [kind]: false }));
  };
  const clearResult = (kind) => {
    cancel(kind);
    setResults((old) => ({ ...old, [kind]: undefined }));
  };
  const changeDraft = (next) => {
    if (connectionIdentity(next) !== connectionIdentity(draftRef.current)) {
      generation.current += 1;
      Object.values(requests.current).forEach(({ controller }) => controller.abort());
      requests.current = {};
      setPending({}); setResults({}); setCandidates([]); setSelected([]);
    }
    draftRef.current = next;
    setDraft(next);
  };
  const patch = (fields) => changeDraft({ ...draftRef.current, ...fields });
  const chooseProfile = (preset, profile, replaceAddress = false) => {
    if (profile) changeDraft(applyProfile(draftRef.current, preset, profile, replaceAddress));
  };

  const loadPresets = async (signal) => {
    setPresetError('');
    try {
      const data = await listUpstreamPresets({ signal });
      if (!mounted.current || signal?.aborted) return;
      setPresets(Array.isArray(data) ? data : []);
      if (!channel && !draftRef.current.type) {
        const preset = data.find((entry) => entry.id === 'generic') || data[0];
        if (preset) chooseProfile(preset, preset.profiles.find((p) => p.id === preset.default_profile) || preset.profiles[0]);
      }
    } catch (error) {
      if (mounted.current && !signal?.aborted) setPresetError(error.message || '预设加载失败');
    }
  };
  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    loadPresets(controller.signal);
    return () => {
      mounted.current = false;
      generation.current += 1;
      controller.abort();
      Object.values(requests.current).forEach(({ controller: active }) => active.abort());
    };
  }, []);

  const run = async (kind, operation, onSuccess) => {
    cancel(kind);
    const token = { id: ++serial.current, generation: generation.current, controller: new AbortController() };
    requests.current[kind] = token;
    setPending((old) => ({ ...old, [kind]: true }));
    setResults((old) => ({ ...old, [kind]: undefined }));
    const current = () => mounted.current && !token.controller.signal.aborted
      && isCurrentRequest(token, generation.current, requests.current[kind]?.id);
    try {
      const result = await operation(connectionPayload(draftRef.current, channel?.id), { signal: token.controller.signal });
      if (current()) { setResults((old) => ({ ...old, [kind]: result })); onSuccess?.(result); }
    } catch (error) {
      if (current()) setResults((old) => ({ ...old, [kind]: { error: error.message || '操作失败' } }));
    } finally {
      if (current()) { delete requests.current[kind]; setPending((old) => ({ ...old, [kind]: false })); }
    }
  };
  const fetchModels = () => run('preview', previewModels, (result) => {
    const names = normalizeModels(Array.isArray(result.models) ? result.models : []);
    setCandidates((old) => normalizeModels([...old, ...names]));
    // 获取只产生候选，用户明确选择导入项。
  });
  const addModel = (name) => {
    const names = normalizeModels([name]);
    setCandidates((old) => normalizeModels([...old, ...names]));
    setSelected((old) => normalizeModels([...old, ...names]));
  };
  return {
    draft, patch, presets, iconPending, setIconPending, presetError, loadPresets, chooseProfile, pending, results, clearResult,
    candidates, selected, setSelected, addModel, fetchModels,
    resolve: () => run('resolve', resolveChannel),
    test: (settings) => run('test', (data, options) => testChannel({ ...data, ...settings }, options)),
    canConnect: !!draft.type && !!draft.base_url.trim() && (!!draft.key.trim() || !!channel),
  };
}
