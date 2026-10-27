import { writeClipboardText } from './clipboard.js';

export const errKindLabels = {
  client_cancel: '用户取消', upstream_error: '上游错误', watchdog_timeout: '流静默超时',
  stream_interrupted: '流中断', convert_error: '转换失败', network: '网络错误',
  internal: '内部错误', circuit_open: '熔断开路', rate_limit_cooldown: '限流冷却',
  channel_capacity: '渠道并发已满',
};

// 名称来自当前渠道目录，历史日志只保存 ID；已删除渠道仍可定位。
// 渠道名在保存时唯一，展示只用名称；ID 仅在名称缺失时作为兜底。
export function logChannelLabel(channelId, channels) {
  if (!channelId) return '未记录渠道';
  const channel = channels.find((item) => Number(item.id) === Number(channelId));
  return channel ? channel.name : `渠道 #${channelId}（已删除或未加载）`;
}

export function logErrorSummary(record) {
  if (!record.err_kind && !record.err_msg) return '';
  return [record.err_kind ? errKindLabels[record.err_kind] || record.err_kind : '', record.err_msg || ''].filter(Boolean).join('\n');
}

// 让调用方仅在浏览器确认写入后显示成功，权限失败向上传递。
export async function copyLogText(text, clipboard) {
  if (!text || !clipboard?.writeText) throw new Error('剪贴板不可用');
  await writeClipboardText(text, { clipboard, document: null });
}
