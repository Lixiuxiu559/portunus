import { isTerminal, isTransferBusy } from './model.js';

const messageOf = (error) => error?.message || '配置迁移暂时不可用，请重试';
const initialDialog = { mode: null, stage: 'idle', file: null, preview: null, result: null, error: '', changed: false,
  errorField: '', errorStage: '', operationId: null, recoveredAt: null, exported: null };
const errorPatch = (error) => ({ error: messageOf(error), errorField: error?.field || '', errorStage: error?.stage || '' });

// 把副作用与渲染分开，使迟到响应、提交未知和 ACK 时点能够离线验证。
export function createTransferController(api, { onCommitted = () => {}, now = Date.now,
  wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms)), queryBudget = 120_000 } = {}) {
  let state = { ...initialDialog, summary: null, summaryStatus: 'loading', summaryError: '',
    recoveries: [], recoveryCursor: null, recoveryStatus: 'loading', recoveryError: '' };
  const listeners = new Set();
  const committed = new Set();
  let generation = 0;
  let summaryGeneration = 0;
  let recoveryGeneration = 0;
  let disposed = false;
  let autoOpened = false;
  const emit = (patch) => {
    if (disposed) return;
    state = { ...state, ...patch };
    listeners.forEach((listener) => listener());
  };
  const current = (epoch) => !disposed && epoch === generation;
  const quietlyDiscard = (preview) => Promise.resolve(api.discard(preview ? { preview_id: preview.preview_id } : {})).catch(() => {});

  async function loadSummary() {
    const epoch = ++summaryGeneration;
    emit({ summaryStatus: 'loading', summaryError: '' });
    try {
      const summary = await api.summary();
      if (!disposed && epoch === summaryGeneration) emit({ summary, summaryStatus: 'ready' });
    } catch (error) {
      if (!disposed && epoch === summaryGeneration) emit({ summaryStatus: 'error', summaryError: messageOf(error) });
    }
  }

  async function loadRecoveries({ more = false, autoOpen = false } = {}) {
    const epoch = ++recoveryGeneration;
    const cursor = more ? state.recoveryCursor : null;
    emit({ recoveryStatus: 'loading', recoveryError: '' });
    try {
      const page = await api.recoveryList({ ...(cursor ? { cursor } : {}), limit: 20 });
      if (disposed || epoch !== recoveryGeneration) return;
      const recoveries = more ? [...state.recoveries, ...(page.data || [])] : (page.data || []);
      emit({ recoveries, recoveryCursor: page.next_cursor || null, recoveryStatus: 'ready' });
      if (autoOpen && !autoOpened && !state.mode && recoveries.length) {
        autoOpened = true;
        void openRecovery(recoveries[0]);
      }
    } catch (error) {
      if (!disposed && epoch === recoveryGeneration) emit({ recoveryStatus: 'error', recoveryError: messageOf(error) });
    }
  }

  function finish(result, epoch) {
    if (!current(epoch) || !isTerminal(result)) return false;
    const oldPreview = state.preview;
    emit({ stage: 'result', result, preview: null, file: null, error: '', operationId: result.operation_id });
    if (oldPreview) void quietlyDiscard(oldPreview);
    if (result.state === 'committed' && !committed.has(result.operation_id)) {
      committed.add(result.operation_id);
      // 列表刷新失败不能把已经提交的事务改成失败。
      try { Promise.resolve(onCommitted(result)).catch(() => {}); } catch { /* 回执仍是权威。 */ }
      void loadSummary();
    }
    return true;
  }

  async function verify(operationId, epoch, started = now()) {
    let attempts = 0;
    emit({ stage: 'checking', error: '', operationId });
    while (current(epoch)) {
      if (now() - started >= queryBudget) break;
      try {
        let result = await api.operationResult({ operation_id: operationId });
        if (!current(epoch)) return;
        if (finish(result, epoch)) return;
        if (result?.state === 'prepared') {
          result = await api.recoverOperation({ operation_id: operationId });
          if (!current(epoch)) return;
          if (finish(result, epoch)) return;
        }
      } catch { /* 404、断线和 DB 不可读都不能作为回滚证明。 */ }
      if (!current(epoch)) return;
      if (now() - started >= queryBudget) break;
      await wait(Math.min(attempts++ === 0 ? 1000 : 2000, Math.max(0, queryBudget - (now() - started))));
    }
    if (current(epoch)) emit({ stage: 'unconfirmed', error: '结果待核实。请继续查询本次操作，避免重复导入。' });
  }

  function open(mode) {
    if (disposed || isTransferBusy(state.stage) || state.mode) return false;
    ++generation;
    emit({ ...initialDialog, mode, stage: mode });
    return true;
  }

  async function chooseFile() {
    if (disposed || isTransferBusy(state.stage)) return;
    const oldPreview = state.preview;
    const epoch = ++generation;
    emit({ file: null, preview: null, error: '', errorField: '', errorStage: '', changed: false, stage: 'selecting' });
    await quietlyDiscard(oldPreview);
    if (!current(epoch)) return;
    try {
      const file = await api.chooseImportFile();
      if (!current(epoch)) { void quietlyDiscard(); return; }
      emit({ file: file?.cancelled ? null : file, stage: 'import' });
    } catch (error) {
      if (current(epoch)) emit({ stage: 'import', ...errorPatch(error) });
    }
  }

  async function preview(password) {
    if (disposed || isTransferBusy(state.stage) || !state.file) return;
    const epoch = generation;
    const fileToken = state.file.file_token;
    emit({ stage: 'decrypting', error: '' });
    try {
      const previewData = await api.previewImport({ file_token: fileToken, password });
      if (!current(epoch)) { void quietlyDiscard(previewData); return; }
      emit({ stage: 'preview', preview: previewData, operationId: previewData.operation_id, changed: false });
    } catch (error) {
      if (current(epoch)) emit({ stage: 'import', ...errorPatch(error) });
    }
  }

  async function backToFile() {
    if (disposed || isTransferBusy(state.stage)) return;
    // discard 也会释放 Main 文件令牌，返回后必须重新选择文件。
    const oldPreview = state.preview;
    ++generation;
    emit({ stage: 'import', preview: null, file: null, operationId: null, error: '', changed: false });
    await quietlyDiscard(oldPreview);
  }

  async function exportConfig(password) {
    if (disposed || isTransferBusy(state.stage)) return;
    const epoch = generation;
    emit({ stage: 'exporting', error: '' });
    try {
      const exported = await api.exportConfig({ password });
      if (current(epoch)) emit(exported?.cancelled ? { stage: 'export' } : { stage: 'exported', exported });
    } catch (error) {
      if (current(epoch)) emit({ stage: 'export', ...errorPatch(error) });
    }
  }

  async function commit() {
    if (disposed || state.stage !== 'preview' || !state.preview) return;
    const epoch = generation;
    const { preview_id, operation_id, file_sha256, plan_sha256 } = state.preview;
    const started = now();
    emit({ stage: 'committing', error: '', operationId: operation_id });
    try {
      const result = await api.commitImport({ preview_id, operation_id, file_sha256, plan_sha256 });
      if (!current(epoch)) return;
      if (finish(result, epoch)) return;
    } catch (error) {
      if (!current(epoch)) return;
      if (error?.code === 'PREVIEW_CHANGED' && error.replacement_preview) {
        const replacement = error.replacement_preview;
        emit({ stage: 'preview', preview: replacement, operationId: replacement.operation_id, changed: true,
          error: '预览中的名称已被占用，本次尚未写入。请检查更新后的完整预览，再次确认导入。' });
        return;
      }
      // 不论 retryable，都不能重发确认或更换操作号。
    }
    if (current(epoch)) await verify(operation_id, epoch, started);
  }

  async function openRecovery(item) {
    if (disposed || state.mode) return;
    const epoch = ++generation;
    emit({ ...initialDialog, mode: 'import', stage: 'checking', operationId: item.operation_id,
      recoveredAt: item.confirmed_at || item.created_at });
    await verify(item.operation_id, epoch);
  }

  function close() {
    if (disposed || isTransferBusy(state.stage)) return false;
    const { preview: oldPreview, result, stage, mode } = state;
    ++generation;
    emit({ ...initialDialog });
    if (mode === 'import' && stage !== 'unconfirmed') void quietlyDiscard(oldPreview);
    if (isTerminal(result)) {
      // 只在用户实际关闭终态结果（包含查看分组）时 ACK。
      void Promise.resolve(api.acknowledge({ operation_id: result.operation_id })).catch(() => {}).finally(() => loadRecoveries());
    } else if (mode === 'import') void loadRecoveries();
    return true;
  }

  function dispose() {
    const oldPreview = state.preview;
    const uncertain = ['committing', 'checking', 'unconfirmed'].includes(state.stage);
    disposed = true;
    ++generation;
    ++summaryGeneration;
    ++recoveryGeneration;
    listeners.clear();
    if (!uncertain && state.mode === 'import') void quietlyDiscard(oldPreview);
  }

  return { getSnapshot: () => state, subscribe: (listener) => { listeners.add(listener); return () => listeners.delete(listener); },
    loadSummary, loadRecoveries, open, chooseFile, preview, backToFile, exportConfig, commit, openRecovery, close, dispose,
    continueQuery: () => state.stage === 'unconfirmed' && verify(state.operationId, generation) };
}
