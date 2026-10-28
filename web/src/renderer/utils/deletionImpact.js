/**
 * 删除影响预览的纯逻辑（不依赖 React / axios）：
 * - createDeletionImpactController：加载、重试、确认删除的状态机；
 *   切换目标或关闭时丢弃迟到响应，未知影响一律阻止确认，确认中防重复提交。
 * - buildDeletionImpactView：把后端影响数据转成弹窗展示模型；
 *   缺失字段保持“未知”，不把 0 或空数组推断成其他含义。
 * 组件只负责接线；真实 DOM 交互不在 node:test 覆盖范围内。
 */

import { strategyLabels } from './groupService.js';

const emptyState = () => ({ target: null, status: 'idle', impact: null, error: '', deleteError: '' });

export const emptyDeletionImpactState = emptyState;

const sameTarget = (a, b) => !!a && !!b && a.kind === b.kind && a.id === b.id;

const errorText = (error, fallback) => {
  const message = typeof error?.message === 'string' ? error.message.trim() : '';
  return message || fallback;
};

/** 与分组/接入页共用同一策略文案，避免同一策略在不同页面叫法不同。 */
export function strategyLabel(strategy) {
  return strategyLabels[strategy] || strategy || '未知策略';
}

/** 校验并规整预览响应；结构不完整视为失败，绝不当作“无引用”。 */
export function normalizeDeletionImpact(raw) {
  if (!raw || typeof raw !== 'object' || !Array.isArray(raw.groups)) {
    throw new Error('删除影响数据不完整');
  }
  return {
    groups: raw.groups.map((group) => {
      if (!group || typeof group !== 'object'
        || !Number.isInteger(group.group_id)
        || typeof group.name !== 'string'
        || typeof group.strategy !== 'string'
        || !Array.isArray(group.removed_item_ids)
        || typeof group.clears_active !== 'boolean'
        || typeof group.becomes_empty !== 'boolean') {
        throw new Error('删除影响数据不完整');
      }
      return {
        groupId: group.group_id,
        name: group.name,
        strategy: group.strategy,
        removedItemIds: group.removed_item_ids.slice(),
        clearsActive: group.clears_active,
        becomesEmpty: group.becomes_empty,
      };
    }),
    modelCount: Number.isFinite(raw.model_count) ? raw.model_count : null,
    hasQuotaSource: typeof raw.has_quota_source === 'boolean' ? raw.has_quota_source : null,
  };
}

/** 渠道预览还必须携带模型数与余额来源；缺失时按未知影响处理，不降级为“无”。 */
export function assertDeletionImpactComplete(kind, impact) {
  if (kind === 'channel' && (impact.modelCount === null || impact.hasQuotaSource === null)) {
    throw new Error('删除影响数据不完整');
  }
  return impact;
}

/** 展示模型：只描述后端给出的内容；空组风险仅来自 becomes_empty。 */
export function buildDeletionImpactView(kind, impact) {
  if (!impact || !Array.isArray(impact.groups)) throw new Error('删除影响数据不完整');
  const groups = impact.groups.map((group, index) => ({
    key: group.groupId === null ? `group-${index}` : String(group.groupId),
    name: group.name || (group.groupId === null ? '未命名分组' : `分组 #${group.groupId}`),
    strategyLabel: strategyLabel(group.strategy),
    removedCount: group.removedItemIds.length,
    clearsActive: group.clearsActive,
    becomesEmpty: group.becomesEmpty,
  }));
  return {
    groups,
    noReferences: groups.length === 0,
    headline: groups.length === 0 ? '没有分组引用' : `将影响 ${groups.length} 个分组`,
    note: '分组名称和历史调用记录会保留；手动选定项清除后不会自动补位。',
    channel: kind === 'channel'
      ? {
          modelCountText: impact.modelCount === null ? '模型数量未知' : `${impact.modelCount} 个模型`,
          hasQuotaSource: impact.hasQuotaSource,
          quotaText: impact.hasQuotaSource === true
            ? '该渠道的余额来源将一并移除'
            : impact.hasQuotaSource === false ? '该渠道没有余额来源' : '无法确认是否存在余额来源',
        }
      : null,
  };
}

/**
 * 删除影响控制器。
 * @param {object} options
 * @param {(target: object, options: { signal: AbortSignal }) => Promise<object>} options.load 只读预览
 * @param {(target: object) => Promise<unknown>} options.remove 真正执行删除
 * @param {(state: object) => void} [options.onChange] 状态变化回调
 * @param {(target: object) => void} [options.onDeleted] 删除成功后的回调；失败不调用
 */
export function createDeletionImpactController({ load, remove, onChange = () => {}, onDeleted = () => {} }) {
  if (typeof load !== 'function' || typeof remove !== 'function') {
    throw new TypeError('删除影响控制器需要 load 与 remove');
  }
  let generation = 0;
  let activeRequest;
  let state = emptyState();

  const emit = (next) => {
    state = next;
    onChange(state);
  };
  const isCurrent = (version, target, request) => (
    generation === version && !request.signal.aborted && sameTarget(state.target, target)
  );
  const isCurrentTarget = (version, target) => generation === version && sameTarget(state.target, target);
  const canConfirm = () => state.status === 'ready' && state.impact !== null;

  function cancel() {
    generation += 1;
    activeRequest?.abort();
    activeRequest = undefined;
    if (state.target || state.status !== 'idle') emit(emptyState());
  }

  function open(target) {
    if (!target || target.id === undefined || target.id === null) {
      cancel();
      return Promise.resolve(null);
    }
    generation += 1;
    activeRequest?.abort();
    const request = new AbortController();
    activeRequest = request;
    const version = generation;
    const snapshot = { kind: target.kind, id: target.id, name: target.name ?? '' };
    emit({ target: snapshot, status: 'loading', impact: null, error: '', deleteError: '' });
    return (async () => {
      try {
        const raw = await load(snapshot, { signal: request.signal });
        if (!isCurrent(version, snapshot, request)) return null;
        const impact = assertDeletionImpactComplete(snapshot.kind, normalizeDeletionImpact(raw));
        emit({ ...state, status: 'ready', impact, error: '' });
        return impact;
      } catch (error) {
        if (!isCurrent(version, snapshot, request)) return null;
        emit({ ...state, status: 'error', impact: null, error: errorText(error, '影响预览加载失败') });
        return null;
      } finally {
        if (activeRequest === request) activeRequest = undefined;
      }
    })();
  }

  function retry() {
    if (!state.target) return Promise.resolve(null);
    return open(state.target);
  }

  async function confirm() {
    if (!canConfirm()) return false;
    const target = state.target;
    const version = generation;
    emit({ ...state, status: 'deleting', deleteError: '' });
    try {
      await remove(target);
    } catch (error) {
      if (!isCurrentTarget(version, target)) return false;
      emit({ ...state, status: 'ready', deleteError: errorText(error, '删除失败，请重试') });
      return false;
    }
    if (!isCurrentTarget(version, target)) return false;
    emit({ ...state, status: 'done', deleteError: '' });
    // 删除已成功；回调异常不能把成功改写成失败或留下未处理的 rejection。
    try { onDeleted(target); } catch { /* 由调用方负责提示 */ }
    return true;
  }

  return { open, retry, cancel, confirm, canConfirm, getState: () => state };
}
