// Public site metadata is advisory: never infer a default currency or overwrite manual settings.
export function newAPIDetectionTarget(draft) {
  if (draft.adapter_id !== 'newapi.account') return '';
  const raw = draft.query_base_url.trim().replace(/\/+$/, '');
  try {
    const url = new URL(raw);
    if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash) return '';
    return raw;
  } catch { return ''; }
}

export function applyNewAPIDetection(draft, options, explicit = false) {
  if (!explicit && (draft.options.quota_per_unit || draft.options.display_unit)) return null;
  return { ...draft.options, quota_per_unit: options.quota_per_unit, display_unit: options.display_unit };
}

export function createNewAPIDetector({ readDraft, detect, applyOptions, onChange }) {
  let generation = 0;
  let controller;
  let pendingTarget = '';
  let result;
  const attempted = new Set();
  const cancel = () => {
    generation += 1;
    controller?.abort();
    if (pendingTarget) attempted.delete(pendingTarget);
    pendingTarget = '';
    result = undefined;
  };
  const reset = () => { cancel(); onChange({ status: 'idle' }); };
  return {
    cancel, reset,
    apply() {
      if (!result || newAPIDetectionTarget(readDraft()) !== result.target) return;
      applyOptions(applyNewAPIDetection(readDraft(), result.options, true));
      onChange({ status: 'success', options: result.options, applied: true });
    },
    async run(force = false) {
      const target = newAPIDetectionTarget(readDraft());
      if (!target || (!force && attempted.has(target))) return;
      cancel();
      attempted.add(target);
      pendingTarget = target;
      controller = new AbortController();
      const { signal } = controller;
      const version = generation;
      const current = () => generation === version && !signal.aborted && newAPIDetectionTarget(readDraft()) === target;
      onChange({ status: 'loading' });
      try {
        const response = await detect({ query_base_url: target }, { signal });
        if (!current()) return;
        const options = response?.options;
        if (typeof options?.quota_per_unit !== 'string' || !options.quota_per_unit || typeof options?.display_unit !== 'string' || !options.display_unit) throw new Error('incomplete metadata');
        result = { target, options: { quota_per_unit: options.quota_per_unit, display_unit: options.display_unit } };
        const updated = applyNewAPIDetection(readDraft(), result.options);
        if (updated) applyOptions(updated);
        onChange({ status: 'success', options: result.options, applied: !!updated });
      } catch {
        if (current()) onChange({ status: 'error' });
      } finally {
        if (current()) pendingTarget = '';
      }
    },
  };
}
