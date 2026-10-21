import { ArrowLeft, Plus, Wallet } from 'lucide-react';
import { useState } from 'react';
import { Button, Modal, Spinner } from '@heroui/react';
import ConfirmModal from '../../../components/ConfirmModal';
import QuotaSourceEditor from './QuotaSourceEditor';
import { QuotaSourceState } from './QuotaSummary';
import useQuotaSources from './useQuotaSources';
import './quota.css';

export default function QuotaPanel({ channel, onClose, onChanged }) {
  const state = useQuotaSources(channel.id, onChanged);
  const [editing, setEditing] = useState(null);
  const [notice, setNotice] = useState('');
  const [deleting, setDeleting] = useState(null);
  const close = () => { onChanged(); onClose(); };
  return <>
    <Modal.Backdrop isOpen onOpenChange={(open) => { if (!open) close(); }}>
      <Modal.Container size="lg" className="quota-panel-container"><Modal.Dialog className="quota-panel-dialog">
        <Modal.CloseTrigger />
        <Modal.Header className="quota-panel-header"><div className="quota-panel-heading">
          {editing ? <button type="button" className="quota-back" aria-label="返回查询来源" onClick={() => setEditing(null)}><ArrowLeft size={18} /></button> : <span className="quota-panel-icon"><Wallet size={20} /></span>}
          <div><p className="quota-eyebrow">{channel.name} · 上游渠道</p><Modal.Heading>{editing ? editing.id ? '编辑查询来源' : '新增查询来源' : '余额与额度'}</Modal.Heading></div>
        </div></Modal.Header>
        <Modal.Body className={`quota-panel-body${editing ? ' is-editing' : ''}`}>
          <div className="quota-panel-content">
            {notice && <p role="status" className="text-sm text-danger">{notice}</p>}
            {state.error && <div role="alert" className="text-sm text-danger">{state.error}<Button size="sm" variant="secondary" onPress={state.load}>重试</Button></div>}
            {state.loading ? <Spinner size="sm" /> : editing ? <QuotaSourceEditor key={editing.id || 'new'} channelId={channel.id}
              source={editing.id ? editing : null} adapters={[...state.adapters].sort((a, b) => Number(b.provider_ids?.includes(channel.provider_id)) - Number(a.provider_ids?.includes(channel.provider_id)))} sources={state.sources} onCancel={() => setEditing(null)}
              onCommitted={onChanged} onSaved={(message) => { setNotice(message || ''); setEditing(null); state.changed(); }} /> : <>
              <div className="quota-source-toolbar"><div><h3>查询来源 <span>{state.sources.length}</span></h3><p>独立展示各账户、币种的额度，不影响渠道路由。</p></div><Button size="sm" variant="secondary" isDisabled={!state.adapters.length || !!state.pending} onPress={() => setEditing({})}><Plus size={15} />添加来源</Button></div>
              {!state.sources.length && <div className="quota-empty"><Wallet size={28} /><h3>了解上游账户的可用额度</h3><p>添加一个查询来源，可先测试接口，再决定是否自动刷新。</p></div>}
              {state.sources.map((source) => <section key={source.id} className="quota-source">
                <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
                  <div className="quota-source-name"><h3>{source.name}</h3><span className={`quota-enabled ${source.enabled ? 'is-enabled' : ''}`}>{source.enabled ? '已启用' : '未启用'}</span></div>
                  <div className="flex gap-1">
                    <Button size="sm" variant="secondary" isDisabled={!source.enabled || source.state?.in_progress || !!state.pending || (source.state?.next_allowed_at && new Date(source.state.next_allowed_at) > new Date())}
                      isPending={state.pending === source.id} onPress={() => state.refresh(source).catch(() => {})}>立即查询</Button>
                    <Button size="sm" variant="tertiary" isDisabled={!!state.pending} onPress={() => setEditing(source)}>编辑</Button>
                    <Button size="sm" variant="tertiary" className="quota-delete" isDisabled={!!state.pending} onPress={() => setDeleting(source)}>删除</Button>
                  </div>
                </div>
                <p className="text-xs text-muted mb-2 break-all">{state.adapters.find((item) => item.id === source.adapter_id)?.name || source.adapter_id} · {source.credential_mode === 'dedicated' ? '专用凭据' : source.credential_mode === 'none' ? '无鉴权' : '继承渠道 Key'} · {source.enabled && source.auto_refresh_minutes ? `每 ${source.auto_refresh_minutes} 分钟查询` : '定时查询关闭'}</p>
                <QuotaSourceState view={source.state} details />
              </section>)}
            </>}
          </div>
        </Modal.Body>
        {!editing && <Modal.Footer className="quota-panel-footer"><span>查询结果仅供参考，以服务商账户为准</span><Button variant="secondary" onPress={close}>完成</Button></Modal.Footer>}
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop>
    <ConfirmModal isOpen={!!deleting} onOpenChange={(open) => !open && setDeleting(null)} title="删除查询来源"
      description={`确定删除「${deleting?.name}」及其查询记录吗？渠道和模型会保留。`} confirmText="删除"
      onConfirm={async () => { await state.remove(deleting); setDeleting(null); }} />
  </>;
}
