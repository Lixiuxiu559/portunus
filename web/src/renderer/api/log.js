import request from './request';

/**
 * 日志：一次对外 LLM 调用的记录与汇总
 * 查询参数（query string）：
 *   api_key_id, channel_id, request_id（精确匹配）, group_name, model_name, client_protocol, success,
 *   start_time, end_time（RFC3339）, page, page_size
 */

/** 分页查询日志 { total, data } */
export function listLogs(params) {
  return request.get('/logs', { params });
}

/**
 * 日志汇总统计
 * @returns { input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, total_requests, recent_requests }
 */
export function getLogStats(params) {
  return request.get('/logs/stat', { params });
}

/** 按请求精确查询已采集的上游尝试；空数组不证明实际尝试数为 0。 */
export function listUpstreamAttempts(params) {
  return request.get('/logs/attempts', { params });
}

/** 服务端请求关联归并；无请求 ID 的历史行独立保留，total 为关联项数。 */
export function listLogRequests(params) {
  return request.get('/logs/requests', { params });
}

/** 精确查询网关切换诊断；data 为 null 时无法确认是否发生跳过。 */
export function getRoutingTrace(params) {
  return request.get('/logs/routing', { params });
}
