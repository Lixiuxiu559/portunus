import { ArrowLeft, Wallet } from 'lucide-react';
import { useState } from 'react';
import { Button, Modal, Spinner } from '@heroui/react';
import ConfirmModal from '../../../components/ConfirmModal';
import QuotaSourceEditor from './QuotaSourceEditor';
import { QuotaSourceState } from './QuotaSummary';
import useQuotaSources from './useQuotaSources';
import './quota.css';

/**
 * 余额与额度面板：渠道级 1:1。
 * 一个渠道就是一个上游账户，只保留一个查询来源；没有配置时进入配置页，
 * 已配置时只提供查询、编辑与移除，不再支持并列多个来源。
 */
export default function QuotaPanel({ channel, onClose, onChanged }) {
  const state = useQuotaSources(channel.id, onChanged);
  const [editing, setEditing] = useState(false);
  const [notice, setNotice] = useState('');
  const [deleting, setDeleting] = useState(null);
  const close = () => { onChanged(); onClose(); };
  const source = state.sources[0] || null;
  const adapterName = state.adapters.find((item) => item.id === source?.adapter_id)?.name || source?.adapter_id;
  return <>
    <Modal.Backdrop isOpen onOpenChange={(open) => { if (!open) close(); }}>
      <Modal.Container size="lg" className="quota-panel-container"><Modal.Dialog className="quota-panel-dialog">
        <Modal.CloseTrigger />
        <Modal.Header className="quota-panel-header"><div className="quota-panel-heading">
          {editing ? <button type="button" className="quota-back" aria-label="返回余额与额度" onClick={() => setEditing(false)}><ArrowLeft size={18} /></button> : <span className="quota-panel-icon"><Wallet size={20} /></span>}
          <div><p className="quota-eyebrow">{channel.name} · 上游渠道</p><Modal.Heading>{editing ? (source ? '编辑查询来源' : '配置查询来源') : '余额与额度'}</Modal.Heading></div>
        </div></Modal.Header>
        <Modal.Body className={`quota-panel-body${editing ? ' is-editing' : ''}`}>
          <div className="quota-panel-content">
            {notice && <p role="status" className="text-sm text-danger">{notice}</p>}
            {state.error && <div role="alert" className="text-sm text-danger">{state.error}<Button size="sm" variant="secondary" onPress={state.load}>重试</Button></div>}
            {state.loading ? <Spinner size="sm" /> : editing ? <QuotaSourceEditor key={source?.id || 'new'} channelId={channel.id}
              source={source} adapters={[...state.adapters].sort((a, b) => Number(b.provider_ids?.includes(channel.provider_id)) - Number(a.provider_ids?.includes(channel.provider_id)))}
              onCancel={() => setEditing(false)}
              onCommitted={onChanged} onSaved={(message) => { setNotice(message || ''); setEditing(false); state.changed(); }} /> : source ? <>
              <div className="quota-source-toolbar"><div><h3>查询来源</h3><p>渠道对应一个上游账户，余额查询配置只保留一条。</p></div></div>
              <section className="quota-source">
                <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
                  <div className="quota-source-name"><h3>{source.name}</h3><span className={`quota-enabled ${source.enabled ? 'is-enabled' : ''}`}>{source.enabled ? '已启用' : '未启用'}</span></div>
                  <div className="flex gap-1">
                    <Button size="sm" variant="secondary" isDisabled={!source.enabled || source.state?.in_progress || !!state.pending || (source.state?.next_allowed_at && new Date(source.state.next_allowed_at) > new Date())}
                      isPending={state.pending === source.id} onPress={() => state.refresh(source).catch(() => {})}>立即查询</Button>
                    <Button size="sm" variant="tertiary" isDisabled={!!state.pending} onPress={() => setEditing(true)}>编辑</Button>
                    <Button size="sm" variant="tertiary" className="quota-delete" isDisabled={!!state.pending} onPress={() => setDeleting(source)}>移除</Button>
                  </div>
                </div>
                <p className="text-xs text-muted mb-2 break-all">{adapterName} · {source.credential_mode === 'dedicated' ? '专用凭据' : source.credential_mode === 'none' ? '无鉴权' : '继承渠道 Key'} · {source.enabled && source.auto_refresh_minutes ? `每 ${source.auto_refresh_minutes} 分钟查询` : '定时查询关闭'}</p>
                <QuotaSourceState view={source.state} details />
              </section>
            </> : <div className="quota-empty">
              <Wallet size={28} />
              <h3>尚未配置余额查询</h3>
              <p>每个渠道只配置一个查询来源；可先测试接口，再决定是否启用自动刷新。</p>
              <Button size="sm" variant="primary" isDisabled={!state.adapters.length || !!state.pending} onPress={() => setEditing(true)}>配置查询来源</Button>
            </div>}
          </div>
        </Modal.Body>
        {!editing && <Modal.Footer className="quota-panel-footer"><span>查询结果仅供参考，以服务商账户为准</span><Button variant="secondary" onPress={close}>完成</Button></Modal.Footer>}
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop>
    <ConfirmModal isOpen={!!deleting} onOpenChange={(open) => !open && setDeleting(null)} title="移除查询来源"
      description={`确定移除「${deleting?.name}」及其查询记录吗？渠道和模型会保留，已保存的查询 Token 会被删除。`} confirmText="移除"
      onConfirm={async () => { await state.remove(deleting); setDeleting(null); }} />
  </>;
}
