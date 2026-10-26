import { useEffect, useRef, useState } from 'react';
import { Button, Form, Modal, toast } from '@heroui/react';
import { X } from 'lucide-react';
import { createChannel } from '../../api';
import ConfirmModal from '../../components/ConfirmModal';
import { setNavBlock } from '../../utils/navGuard';
import ConnectionFields from './ConnectionFields';
import ModelPicker from './ModelPicker';
import CallTestPanel from './CallTestPanel';
import useChannelDraft from './useChannelDraft';
import { channelDraftFingerprint, savePayload } from './channelDraft';
import './channel-create.css';

function CreateChannelPanel({ onOpenChange, onCreated }) {
  const state = useChannelDraft();
  const [saving, setSaving] = useState(false);
  const [touched, setTouched] = useState(false);
  const [confirmClose, setConfirmClose] = useState(false);
  const baseline = useRef(null);
  const formRef = useRef(null);
  if (baseline.current === null && state.draft.type) baseline.current = channelDraftFingerprint(state.draft, []);
  const dirty = touched || (baseline.current !== null && baseline.current !== channelDraftFingerprint(state.draft, state.selected));
  const locked = saving || state.iconPending;

  useEffect(() => {
    setNavBlock('create-channel', dirty ? '添加渠道有未保存的修改' : null);
    return () => setNavBlock('create-channel', null);
  }, [dirty]);
  const requestClose = () => {
    if (locked) return;
    if (dirty) setConfirmClose(true);
    else onOpenChange(false);
  };
  const handleSubmit = async (event) => {
    event.preventDefault();
    if (locked || !state.draft.type || !formRef.current?.checkValidity()) return;
    setSaving(true);
    try {
      await createChannel(savePayload(state.draft, state.selected));
      toast.success(state.selected.length ? '渠道与所选模型已保存，请到模型分组中添加使用' : '渠道已保存，尚未导入模型');
      onOpenChange(false);
      onCreated?.();
    } catch { /* 请求层显示错误，保留草稿供修正。 */ }
    finally { setSaving(false); }
  };
  return <>
    <Modal.Backdrop isOpen onOpenChange={(open) => { if (!open) requestClose(); }} isDismissable={!locked} isKeyboardDismissDisabled={locked}>
      <Modal.Container size="xl"><Modal.Dialog className="channel-create-dialog">
        <Modal.Header className="channel-create-header"><div><Modal.Heading>添加渠道</Modal.Heading></div><button type="button" className="channel-create-close" aria-label="关闭添加渠道" disabled={locked} onClick={requestClose}><X className="size-4" /></button></Modal.Header>
        <Modal.Body className="channel-create-body">
          <Form id="create-channel-form" ref={formRef} onSubmit={handleSubmit} onChange={() => setTouched(true)}>
            <fieldset disabled={saving} className="channel-form-grid channel-create-form-grid">
              <div className="channel-form-column"><ConnectionFields state={state} /></div>
              <div className="channel-form-column"><ModelPicker state={state} /><CallTestPanel state={state} /></div>
            </fieldset>
          </Form>
        </Modal.Body>
        <Modal.Footer className="channel-create-footer"><span>已选 {state.selected.length} 个模型</span><Button variant="secondary" isDisabled={locked} onPress={requestClose}>取消</Button><Button variant="primary" isPending={saving} isDisabled={state.iconPending || !state.draft.type} onPress={() => formRef.current?.requestSubmit()}>{saving ? '添加中…' : '添加渠道'}</Button></Modal.Footer>
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop>
    <ConfirmModal isOpen={confirmClose} onOpenChange={setConfirmClose} title="放弃未保存的渠道？" description="已填写的连接信息和模型选择尚未保存。" confirmText="放弃并关闭" onConfirm={() => onOpenChange(false)} />
  </>;
}

export default function CreateChannelModal({ isOpen, ...props }) {
  return isOpen ? <CreateChannelPanel {...props} /> : null;
}
