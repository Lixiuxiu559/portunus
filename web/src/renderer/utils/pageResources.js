// 只缓存页面模块，不挂载组件，也不发起业务请求。
export function createPageResources(loaders) {
  const loaded = new Map();
  const pending = new Map();
  return {
    get: (id) => loaded.get(id),
    load(id) {
      if (loaded.has(id)) return Promise.resolve(loaded.get(id));
      if (!pending.has(id)) {
        const promise = Promise.resolve().then(() => loaders[id]()).then((module) => {
          loaded.set(id, module);
          pending.delete(id);
          return module;
        }, (error) => {
          pending.delete(id);
          throw error;
        });
        pending.set(id, promise);
      }
      return pending.get(id);
    },
  };
}

// 每次空闲只准备一页；取消后，正在加载的模块结束也不会继续启动下一页。
export function warmPageResources(resources, ids, { schedule, cancel }) {
  const remaining = ids.filter((id) => !resources.get(id));
  let stopped = false;
  let handle = null;
  const next = () => {
    if (stopped || remaining.length === 0) return;
    handle = schedule(() => {
      handle = null;
      if (stopped) return;
      resources.load(remaining.shift()).catch(() => {}).finally(next);
    });
  };
  next();
  return () => {
    stopped = true;
    if (handle !== null) cancel(handle);
  };
}
