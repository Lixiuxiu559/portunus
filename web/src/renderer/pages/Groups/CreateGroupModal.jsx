import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, toast } from '@heroui/react';
import { createGroup } from '../../api';
import { groupCreatePayload, moveGroupModel, toggleGroupModel } from '../../utils/groupModelSelection';
import GroupModelPicker from './GroupModelPicker';
import { strategyDescriptions } from '../../utils/groupService';

const strategies = [{ id: 'manual', label: '手动选择' }, { id: 'round_robin', label: '轮询' }, { id: 'failover', label: '故障转移' }];

export default function CreateGroupModal({ isOpen, onOpenChange, onCreated, channels }) {
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState('');
  const [strategy, setStrategy] = useState('manual');
  const [selected, setSelected] = useState([]);
  const [activeModelId, setActiveModelId] = useState('');
  const formRef = useRef(null);
  const submitting = useRef(false);
  useEffect(() => {
    if (isOpen) { setName(''); setStrategy('manual'); setSelected([]); setActiveModelId(''); }
  }, [isOpen]);
  const selectModel = (model) => {
    const next = toggleGroupModel(selected, model);
    setSelected(next);
    if (!next.some((item) => String(item.id) === String(activeModelId))) setActiveModelId(next.length ? String(next[0].id) : '');
  };
  const close = (open) => { if (!submitting.current) onOpenChange(open); };
  const handleSubmit = async (event) => {
    event.preventDefault();
    if (submitting.current || !formRef.current?.checkValidity()) return;
    let payload;
    try { payload = groupCreatePayload({ name, strategy, selected, activeModelId }); }
    catch (error) { toast.danger(error.message); return; }
    submitting.current = true;
    setSaving(true);
    try {
      await createGroup(payload);
      onOpenChange(false);
      onCreated?.();
      toast.success('分组创建成功');
    } catch { /* 请求拦截器提示错误，保留已选成员以便重试。 */ }
    finally { submitting.current = false; setSaving(false); }
  };
  const channelNames = Object.fromEntries(channels.map((channel) => [channel.id, channel.name]));
  return <Modal.Backdrop isOpen={isOpen} onOpenChange={close}>
    <Modal.Container size="lg"><Modal.Dialog>
      <Modal.CloseTrigger isDisabled={saving} />
      <Modal.Header><Modal.Heading>新增分组</Modal.Heading></Modal.Header>
      <Modal.Body><Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
        <TextField isRequired name="name" value={name} onChange={setName} isDisabled={saving} autoFocus autoComplete="off" validate={(value) => !value.trim() ? '请填写分组名称' : null}>
          <Label>分组名称（客户端 model）</Label><Input placeholder="例如：coding、daily" /><FieldError />
          <p className="text-xs text-muted">客户端使用这个名称调用分组；更换组内模型时无需修改客户端。</p>
        </TextField>
        <Select aria-label="路由策略" name="strategy" selectedKey={strategy} onSelectionChange={setStrategy} isDisabled={saving}>
          <Label>路由策略</Label><Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover className="dialog-options"><ListBox>{strategies.map((option) => <ListBox.Item key={option.id} id={option.id} textValue={option.label}>{option.label}<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover>
        </Select>
        <p className="text-xs text-muted">{strategyDescriptions[strategy]}</p>
        {isOpen && <GroupModelPicker channels={channels} selected={selected} onSelect={selectModel} multiple enabled={isOpen} disabled={saving} />}
        <section aria-label="已选成员" className="group-selected-members">
          <p className="text-sm font-medium">已选 {selected.length} 个成员</p>
          {!selected.length && <p className="text-xs text-muted">可跨页、跨渠道选择多个模型。</p>}
          {selected.map((model, index) => <div className="group-selected-row" key={model.id}>
            <span className="group-picker-identity"><span className="font-mono">{model.name}</span><span className="text-xs text-muted">{channelNames[model.channel_id] || `渠道 #${model.channel_id}`}{strategy === 'failover' ? ` · 第 ${index + 1} 顺位` : ''}</span></span>
            {strategy === 'failover' && <><Button type="button" size="sm" variant="secondary" aria-label={`上移 ${model.name}（${channelNames[model.channel_id]}）`} isDisabled={saving || index === 0} onPress={() => setSelected((items) => moveGroupModel(items, model.id, -1))}>上移</Button><Button type="button" size="sm" variant="secondary" aria-label={`下移 ${model.name}（${channelNames[model.channel_id]}）`} isDisabled={saving || index === selected.length - 1} onPress={() => setSelected((items) => moveGroupModel(items, model.id, 1))}>下移</Button></>}
            <Button type="button" size="sm" variant="secondary" aria-label={`取消选择 ${model.name}（${channelNames[model.channel_id]}）`} isDisabled={saving} onPress={() => selectModel(model)}>移除</Button>
          </div>)}
        </section>
        {strategy === 'manual' && selected.length > 0 && <Select aria-label="当前手动成员" selectedKey={activeModelId} onSelectionChange={setActiveModelId} isDisabled={saving}>
          <Label>当前手动成员</Label><Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
          <Select.Popover className="dialog-options"><ListBox>{selected.map((model) => <ListBox.Item key={model.id} id={String(model.id)} textValue={`${model.name}（${channelNames[model.channel_id]}）`}>{model.name}（{channelNames[model.channel_id]}）<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover>
        </Select>}
        {strategy === 'failover' && <p className="text-xs text-muted">按已选成员的顺序尝试；可用上移、下移调整备用顺序。</p>}
      </Form></Modal.Body>
      <Modal.Footer><Button variant="secondary" isDisabled={saving} onPress={() => close(false)}>取消</Button><Button variant="primary" isPending={saving} isDisabled={!selected.length || !name.trim()} onPress={() => formRef.current?.requestSubmit()}>{saving ? '保存中…' : '保存'}</Button></Modal.Footer>
    </Modal.Dialog></Modal.Container>
  </Modal.Backdrop>;
}
