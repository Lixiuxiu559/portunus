import { useSyncExternalStore } from 'react';
import { createUpdateStore } from '../utils/updateStore';

// Layout 和设置页共享一份状态与订阅，切页不会丢失后台下载结果。
let store;
export function useUpdater() {
  store ??= createUpdateStore(window.api);
  const state = useSyncExternalStore(store.subscribe, store.getSnapshot);
  return { ...state, check: store.check, download: store.download, install: store.install };
}
