import request from './request';

/**
 * 日志：一次对外 LLM 调用的记录与汇总
 * 查询参数（query string）：
 *   api_key_id, channel_id, group_name, model_name, success,
 *   start_time, end_time（RFC3339）, page, page_size
 */

/** 分页查询日志 { total, data } */
export function listLogs(params) {
  return request.get('/logs', { params });
}

/**
 * 日志汇总统计
 * @returns { total_cost, input_tokens, output_tokens, total_requests, recent_requests }
 */
export function getLogStats(params) {
  return request.get('/logs/stat', { params });
}
