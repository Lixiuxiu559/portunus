import { createPageResources, warmPageResources } from './utils/pageResources';

const loaders = {
  channels: () => import('./pages/Channels'),
  models: () => import('./pages/Models'),
  groups: () => import('./pages/Groups'),
  logs: () => import('./pages/Logs'),
  clients: () => import('./pages/Clients'),
  settings: () => import('./pages/Settings'),
};

const resources = createPageResources(loaders);
export const pageIds = Object.keys(loaders);
export const loadPage = (id) => resources.load(id);
export const getLoadedPage = (id) => resources.get(id)?.default;
export const preparePage = (id) => { void loadPage(id).catch(() => {}); };

export function warmPages() {
  const idle = typeof window.requestIdleCallback === 'function';
  return warmPageResources(resources, pageIds, {
    schedule: (callback) => idle ? window.requestIdleCallback(callback, { timeout: 1000 }) : window.setTimeout(callback, 32),
    cancel: (handle) => idle ? window.cancelIdleCallback(handle) : window.clearTimeout(handle),
  });
}
