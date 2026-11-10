// 页面级导航守卫：页面有未保存数据时注册拦截原因，Layout 切页前检查并弹确认。
// 按 key 注册（一页/一面板一个 key），注册与注销配对，多面板并存时互不覆盖。
const blocks = new Map();

export const setNavBlock = (key, reason, { locked = false } = {}) => {
  if (reason) blocks.set(key, { reason, locked });
  else blocks.delete(key);
};

// 返回任一未决的拦截原因（null 表示可自由导航）
export const getNavBlock = () => {
  // 进行中的迁移优先于其他可放弃草稿。
  for (const block of blocks.values()) if (block.locked) return block.reason;
  for (const block of blocks.values()) return block.reason;
  return null;
};

export const isNavLocked = () => [...blocks.values()].some((block) => block.locked);
