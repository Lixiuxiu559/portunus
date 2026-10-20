// 主进程保存权威状态；订阅后再取快照，并以 revision 防止旧 IPC 回复覆盖新事件。
export function createUpdateStore(api) {
  let state = {
    revision: -1,
    checking: false,
    available: false,
    downloading: false,
    downloaded: false,
    installing: false,
    latestVersion: null,
    progress: { percent: 0, bytesPerSecond: 0 },
    error: null,
  };
  const listeners = new Set();
  let disconnect;
  let generation = 0;
  const publish = (next) => {
    state = next;
    for (const listener of listeners) listener();
  };
  const receive = (next) => {
    if (next && next.revision > state.revision) publish(next);
  };

  function subscribe(listener) {
    listeners.add(listener);
    if (listeners.size === 1) {
      const current = ++generation;
      disconnect = api?.onUpdateEvent?.('update:state', receive);
      api?.getUpdateState?.().then((next) => {
        if (generation === current) receive(next);
      }).catch(() => {
        // 不用读取快照的错误覆盖正常到达的更新事件。
      });
    }
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) {
        generation++;
        disconnect?.();
        disconnect = undefined;
      }
    };
  }

  async function invoke(method) {
    try {
      if (!api?.[method]) throw new Error('请在桌面应用中更新');
      await api[method]();
    } catch (err) {
      publish({ ...state, installing: false, error: err?.message ?? String(err) });
    }
  }

  return {
    getSnapshot: () => state,
    subscribe,
    check: () => invoke('checkForUpdate'),
    download: () => invoke('downloadUpdate'),
    install: () => {
      if (!state.downloaded || state.installing) return;
      publish({ ...state, installing: true, error: null });
      return invoke('installUpdate');
    },
  };
}
