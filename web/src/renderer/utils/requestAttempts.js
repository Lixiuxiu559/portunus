export const attemptReasonLabels = {
  initial: '首次调用', retry: '同目标重试', failover: '切换目标', budget_compat: '预算字段兼容重试',
};

// 连续序号不证明尾部没有丢失，未采集也不能解释成确实没有 HTTP 调用。
export function attemptCoverage(rows, page, pageSize) {
  if (!rows.length) return '没有已采集的尝试记录；可能未采集、未发起或记录缺失，无法确认调用次数。';
  const first = (page - 1) * pageSize + 1;
  if (rows.some((row, index) => row.sequence !== first + index)) {
    return '尝试序号有缺口，记录可能不完整；不能据此推断完整请求结果。';
  }
  return '当前页序号连续；完整性未确认，仍可能存在进行中或尾部缺失的记录。';
}

// 弹窗换请求、翻页和重试时忽略旧响应，关闭后不再回写状态。
export function createAttemptLoader(fetchPage) {
  let version = 0;
  return {
    invalidate() { version += 1; },
    async load(params) {
      const current = ++version;
      try {
        const result = await fetchPage(params);
        return current === version ? result : null;
      } catch (error) {
        if (current !== version) return null;
        throw error;
      }
    },
  };
}
