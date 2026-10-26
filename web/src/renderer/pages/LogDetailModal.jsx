import { Button, Modal, toast } from '@heroui/react';
import { logAPIKeyLabel } from '../utils/apiKeys';
import RequestAttemptTimeline from './RequestAttemptTimeline';
import RequestRoutingTrace from './RequestRoutingTrace';
import { copyLogText, logChannelLabel, logErrorSummary, errKindLabels } from '../utils/logDetails';

export default function LogDetailModal({ record, channels, onClose, association }) {
  const copy = async (text) => {
    try { await copyLogText(text, navigator.clipboard); toast.success('已复制'); }
    catch { toast.danger('复制失败，请手动选择文本复制'); }
  };
  const error = record ? logErrorSummary(record) : '';
  const value = (data) => data === undefined || data === null || data === '' ? '未记录' : String(data);
  return <Modal.Backdrop isOpen={!!record} onOpenChange={(open) => { if (!open) onClose(); }}>
    <Modal.Container size="lg"><Modal.Dialog className="log-detail-dialog">
      <Modal.CloseTrigger />
      <Modal.Header><Modal.Heading>{association ? '请求关联详情' : `调用记录详情 #${record?.id}`}</Modal.Heading></Modal.Header>
      <Modal.Body>{record && <>
        <p className="text-xs text-muted mb-4">{association ? `关联项包含 ${association.log_count} 条持久化日志；以下为最近日志，不能据此确认最终请求结果。筛选只决定命中关联项，详情展示整个请求的已记录尝试。` : '这是一条日志记录；下方关联已采集的上游尝试，不能据此确认请求完整性。'}渠道名称来自当前配置，历史记录以渠道 ID 为准。</p>
        <dl className="log-detail-fields">
          <dt>时间</dt><dd>{record.created_at ? new Date(record.created_at).toLocaleString('zh-CN') : '未记录'}</dd>
          <dt>调用时 API Key</dt><dd>{logAPIKeyLabel(record)}</dd>
          <dt>API Key ID</dt><dd>{record.api_key_id > 0 ? record.api_key_id : '未记录'}</dd>
          <dt>模型分组</dt><dd>{value(record.group_name)}</dd>
          <dt>实际渠道</dt><dd>{logChannelLabel(record.channel_id, channels)}</dd>
          <dt>上游模型</dt><dd>{value(record.model_name)}</dd>
          <dt>请求 ID</dt><dd><span className="font-mono">{value(record.request_id)}</span>{record.request_id && <Button size="sm" variant="secondary" onPress={() => copy(record.request_id)}>复制请求 ID</Button>}</dd>
          <dt>{association ? '最近日志结果' : '日志结果'}</dt><dd>{record.success === true ? '成功' : record.success === false ? '失败' : '未记录'}</dd>
          <dt>HTTP 状态</dt><dd>{record.status > 0 ? record.status : '未记录'}</dd>
          <dt>响应方式</dt><dd>{record.stream === true ? '流式' : record.stream === false ? '非流式' : '未记录'}</dd>
          <dt>耗时 / 首包</dt><dd>{value(record.duration_ms)} ms / {record.first_token_ms > 0 ? `${record.first_token_ms} ms` : '未记录或不适用'}</dd>
          <dt>非缓存输入 token</dt><dd>{value(record.input_token)}</dd>
          <dt>缓存读 / 写 token</dt><dd>{value(record.cache_read_token)} / {value(record.cache_write_token)}</dd>
          <dt>输出 token</dt><dd>{value(record.output_token)}</dd>
        </dl>
        <p className="text-xs text-muted mt-3">用量按现有日志展示；历史字段可能未记录，0 值不能证明上游没有消耗。</p>
        <section className="log-detail-error" aria-label="错误详情">
          <h3 className="font-medium text-sm">错误详情</h3>
          <p className="text-xs text-muted">{record.err_kind ? `${errKindLabels[record.err_kind] || record.err_kind}（${record.err_kind}）` : '未记录错误分类'}</p>
          <pre>{record.err_msg || '未记录错误原文'}</pre>
          {error && <Button size="sm" variant="secondary" onPress={() => copy(error)}>复制错误摘要</Button>}
        </section>
        <RequestAttemptTimeline key={record.request_id || `log:${record.id}`} requestId={record.request_id} channels={channels} />
        <RequestRoutingTrace key={`routing:${record.request_id || record.id}`} requestId={record.request_id} channels={channels} />
        <details className="mt-4 text-sm"><summary>兼容诊断</summary><dl className="log-detail-fields mt-3"><dt>思考桥解码</dt><dd>{value(record.bridge_decoded)}</dd><dt>垫片改写</dt><dd>{value(record.shim_rewrites)}</dd><dt>剥离托管工具</dt><dd>{value(record.hosted_tools_stripped)}</dd></dl></details>
      </>}</Modal.Body>
      <Modal.Footer><Button variant="secondary" onPress={onClose}>关闭</Button></Modal.Footer>
    </Modal.Dialog></Modal.Container>
  </Modal.Backdrop>;
}
