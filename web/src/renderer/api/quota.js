import request from './request';
import { RETRY_MAX_ATTEMPTS } from './retryPolicy';

// 额度请求由后端决定退避；每次主动查询最多发送一次。
const config = (options) => ({ ...options, __retryCount: RETRY_MAX_ATTEMPTS });
export const listQuotaAdapters = (options) => request.get('/quota/adapters', config(options));
export const listQuotaSources = (channelId, options) => request.get(`/channels/${channelId}/quota-sources`, config(options));
export const createQuotaSource = (channelId, data, options) => request.post(`/channels/${channelId}/quota-sources`, data, config(options));
export const updateQuotaSource = (id, data, options) => request.put(`/quota-sources/${id}`, data, config(options));
export const deleteQuotaSource = (id, options) => request.delete(`/quota-sources/${id}`, config(options));
export const previewQuotaSource = (data, options) => request.post('/quota-sources/preview', data, config(options));
export const refreshQuotaSource = (id, options) => request.post(`/quota-sources/${id}/refresh`, undefined, config(options));
export const getQuotaSummaries = (channelIds, options) => request.get('/quota/summaries', {
  ...config(options), params: { channel_ids: channelIds.join(',') },
});
