import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, Form, toast } from '@heroui/react';
import { addGroupItem } from '../../api';
import GroupModelPicker from './GroupModelPicker';

export default function AddGroupItemModal({ group, channels, isOpen, onOpenChange, onAdded }) {
  const [saving, setSaving] = useState(false);
  const [selected, setSelected] = useState([]);
  const [priority, setPriority] = useState('0');
  const formRef = useRef(null);
  const submitting = useRef(false);
  useEffect(() => { if (isOpen) { setSelected([]); setPriority('0'); } }, [isOpen, group?.id]);
  const close = (open) => { if (!submitting.current) onOpenChange(open); };
  const handleSubmit = async (event) => {
    event.preventDefault();
    if (submitting.current || !formRef.current?.checkValidity() || !selected.length) return;
    submitting.current = true;
    setSaving(true);
    try {
      await addGroupItem(group.id, { model_id: selected[0].id, priority: Number(priority) || 0 });
      onOpenChange(false);
      onAdded?.();
      toast.success('模型已添加到分组');
    } catch { /* 请求拦截器提示错误，保留选择以便重试。 */ }
    finally { submitting.current = false; setSaving(false); }
  };
  return <Modal.Backdrop isOpen={isOpen} onOpenChange={close}>
    <Modal.Container size="lg"><Modal.Dialog>
      <Modal.CloseTrigger isDisabled={saving} />
      <Modal.Header><Modal.Heading>添加模型到「{group?.name}」</Modal.Heading></Modal.Header>
      <Modal.Body><Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
        {isOpen && <GroupModelPicker channels={channels} selected={selected} onSelect={(model) => setSelected([model])} excludedIds={(group?.items || []).map((item) => item.model_id)} enabled={isOpen} disabled={saving} />}
        {selected.length > 0 && <p className="text-sm" aria-live="polite">已选：{selected[0].name}（{channels.find((channel) => channel.id === selected[0].channel_id)?.name || `渠道 #${selected[0].channel_id}`}）</p>}
        <TextField name="priority" type="number" value={priority} onChange={setPriority} isDisabled={saving}><Label>优先级</Label><Input placeholder="0" /></TextField>
      </Form></Modal.Body>
      <Modal.Footer><Button variant="secondary" isDisabled={saving} onPress={() => close(false)}>取消</Button><Button variant="primary" isPending={saving} isDisabled={!selected.length} onPress={() => formRef.current?.requestSubmit()}>{saving ? '添加中…' : '添加'}</Button></Modal.Footer>
    </Modal.Dialog></Modal.Container>
  </Modal.Backdrop>;
}
