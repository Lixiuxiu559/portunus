// 仅为页面首帧提供快照；所有请求仍然访问后端，失败不会被缓存掩盖。
// 数据只保存在当前渲染进程内，刷新/退出即释放，不写入浏览器存储。
const cacheable = new Set(['/channels', '/models', '/groups', '/logs', '/logs/stat', '/settings', '/apikey', '/apikeys', '/quota/summaries']);

export function createResponseCache({ maxEntries = 64, maxAge = 300_000, now = Date.now } = {}) {
  const entries = new Map();
  let generation = 0;
  const keyOf = (url, params = {}) => JSON.stringify([url,
    Object.entries(params).filter(([, value]) => value != null).sort(([a], [b]) => a.localeCompare(b)),
  ]);
  return {
    generation: () => generation,
    read(url, params) {
      const key = keyOf(url, params);
      const entry = entries.get(key);
      if (!entry) return undefined;
      if (now() - entry.at >= maxAge) { entries.delete(key); return undefined; }
      return structuredClone(entry.data);
    },
    write(url, params, data, requestGeneration) {
      // 保存前已发出的慢 GET，不能在保存成功后重新写回旧快照。
      if (!cacheable.has(url) || requestGeneration !== generation) return;
      const key = keyOf(url, params);
      entries.delete(key);
      entries.set(key, { data: structuredClone(data), at: now() });
      while (entries.size > maxEntries) entries.delete(entries.keys().next().value);
    },
    invalidate() { generation += 1; entries.clear(); },
  };
}

export const responseCache = createResponseCache();
export const getCachedResponse = (url, params) => responseCache.read(url, params);
