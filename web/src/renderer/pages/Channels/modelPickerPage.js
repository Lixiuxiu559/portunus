export const MODEL_PICKER_PAGE_SIZE = 50;

/** 分页只控制展示，不修改候选或已选项；全选仍由完整候选列表决定。 */
export function modelPickerPage(names, search, requestedPage, pageSize = MODEL_PICKER_PAGE_SIZE) {
  const query = search.toLowerCase();
  const matches = query ? names.filter((name) => name.toLowerCase().includes(query)) : names;
  const totalPages = Math.max(1, Math.ceil(matches.length / pageSize));
  const page = Math.max(1, Math.min(requestedPage, totalPages));
  return { names: matches.slice((page - 1) * pageSize, page * pageSize), total: matches.length, totalPages, page };
}
