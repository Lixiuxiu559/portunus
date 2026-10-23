import { errKindLabels, logChannelLabel } from './logDetails.js';
import { attemptReasonLabels } from './requestAttempts.js';

const eventLabels = {
  candidate_skipped: '跳过候选', candidate_selected: '选择候选', admission_blocked: '调用准入受阻',
  probe_acquired: '取得半开探测名额', probe_finished: '半开探测结束',
  capacity_acquired: '取得渠道并发名额', capacity_released: '释放渠道并发名额',
  relay_finished: '目标调用结束', target_exhausted: '结束当前目标', http_attempt: '发起显式 HTTP 尝试',
};
const reasonLabels = {
  ...attemptReasonLabels,
  rate_limit_cooldown: '限流冷却中', circuit_open: '熔断开路', probe_busy: '半开探测名额已占用',
  stale_lease: '探测或调用租约代次已失效', primary: '选择主目标', backup: '选择备用目标',
  channel_capacity: '渠道并发已满', capacity_acquired: '取得渠道并发名额', capacity_released: '释放渠道并发名额',
  half_open: '半开恢复探测', succeeded: '成功', failed: '失败', stalled: '等候响应头超时',
  ignored: '中性释放，不计恢复成功或健康失败', retry_exhausted: '本目标重试用尽',
  not_retryable: '当前错误不再重试此目标', header_stall: '等候响应头超时',
};
const stopLabels = {
  conversion_safety_stop: '转换或流式校验触发安全停止', request_stopped: '请求触发安全停止',
  relay_succeeded: '网关调用处理成功', committed_failure: '开始交付后失败，禁止重放',
  replay_unsafe: '重放不安全，停止切换', client_cancel: '客户端已取消', resource_limit: '触发资源限额',
  attempt_budget_exhausted: '请求总尝试预算用尽', non_stream_deadline: '非流式累计期限已到',
  first_content_deadline: '首内容累计期限已到', no_available_target: '没有可用目标',
  candidates_exhausted: '候选目标已用尽', request_invalid: '请求不合法', request_read_failed: '读取请求失败',
  group_not_found: '模型分组不存在', route_unresolved: '无法解析路由', handler_interrupted: '网关处理意外中止',
  client_write: '客户端响应写入失败',
};
const outcomeLabels = {
  relay_succeeded: '网关调用处理成功', relay_failed: '网关调用处理失败',
  rejected: '网关拒绝请求', interrupted: '网关处理中止',
};

// 未知枚举保留原值，避免新服务端事件被解释成已知行为。
export function routingLabel(kind, value) {
  const labels = { event: eventLabels, reason: { ...errKindLabels, ...reasonLabels }, stop: stopLabels,
    outcome: outcomeLabels, strategy: { manual: '手动', failover: '故障转移' }, scope: { model: '模型', channel: '渠道' } };
  const mapping = Object.hasOwn(labels, kind) ? labels[kind] : null;
  return mapping && Object.hasOwn(mapping, value) ? mapping[value] : value || '未记录';
}

export function routingEventDetails(event) {
  const details = [];
  if (event.relay_attempt > 0) details.push(`relay #${event.relay_attempt}`);
  if (event.scope) details.push(`${routingLabel('scope', event.scope)}作用域`);
  if (event.retry_after !== undefined && event.retry_after !== null && event.retry_after !== '') {
    details.push(`建议 ${event.retry_after} 秒后重试`);
  }
  if (event.probe === true) details.push('半开探测');
  if (Number.isSafeInteger(event.capacity_limit) && event.capacity_limit >= 0 &&
    Number.isSafeInteger(event.in_flight) && event.in_flight >= 0) {
    details.push(`渠道并发：${event.in_flight} / ${event.capacity_limit === 0 ? '不限' : event.capacity_limit}`);
  }
  if (event.err_kind) details.push(`错误分类：${routingLabel('reason', event.err_kind)}（${event.err_kind}）`);
  return details.join(' · ');
}

// 独立摘要字段不依赖事件尾部是否被截断；选中不表示已发送 HTTP 或调用成功。
export function routingFinalCandidate(trace, channels = []) {
  if (!(trace.final_candidate > 0)) return '未记录选中候选';
  return `候选 #${trace.final_candidate} · ${logChannelLabel(trace.final_channel_id, channels)} · 模型 ID ${trace.final_model_id || '未记录'}`;
}

export function routingCoverage(trace) {
  if (!trace) return '未获得切换诊断，可能未采集、尚未完成或已清理；无法据此判断是否跳过候选。';
  return trace.dropped_events > 0
    ? `诊断已截断，另有 ${trace.dropped_events} 条事件未保留；以下事件无法覆盖完整切换过程。`
    : '以下为已保留的网关路由事件。候选跳过和探测名额事件不构成 HTTP 尝试。';
}
