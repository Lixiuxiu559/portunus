import { useMemo, useState, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, toast } from '@heroui/react';
import { createGroup } from '../../api';

const strategyOptions = [
  { id: 'manual', label: '手动选择' },
  { id: 'round_robin', label: '轮询' },
  { id: 'failover', label: '故障转移' },
];

const emptyForm = { name: '', strategy: 'manual', channel_id: '', model_id: '' };

export default function CreateGroupModal({ isOpen, onOpenChange, onCreated, channels, models }) {
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const formRef = useRef(null);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  // 切换渠道时清空已选模型（渠道变了，原模型不再属于新渠道）
  const handleChannelChange = (value) => {
    setForm((f) => ({ ...f, channel_id: value, model_id: '' }));
  };

  // 当前渠道下的模型
  const channelModels = useMemo(
    () => models.filter((m) => m.channel_id === Number(form.channel_id)),
    [models, form.channel_id],
  );

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    if (!form.channel_id || !form.model_id) return;
    setSaving(true);
    try {
      await createGroup({
        name: form.name.trim(),
        strategy: form.strategy,
        items: [{ model_id: Number(form.model_id), priority: 0 }],
      });
      setForm(emptyForm);
      onOpenChange(false);
      onCreated?.();
      toast.success('分组创建成功');
    } catch (e) {
      toast.error(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}>
      <Modal.Container size="sm">
        <Modal.Dialog>
          <Modal.CloseTrigger />
          <Modal.Header>
            <Modal.Heading>新增分组</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
              <TextField
                isRequired
                name="name"
                value={form.name}
                onChange={set('name')}
                autoFocus
                autoComplete="off"
                validate={(v) => (!v || !v.trim()) ? '请填写分组名称' : null}
              >
                <Label>分组名称</Label>
                <Input placeholder="例如：GPT-4 负载均衡" />
                <FieldError />
              </TextField>

              <Select
                isRequired
                name="strategy"
                selectedKey={form.strategy}
                onSelectionChange={set('strategy')}
              >
                <Label>路由策略</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {strategyOptions.map((o) => (
                      <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
                        {o.label}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>

              <Select
                isRequired
                name="channel_id"
                placeholder="请选择渠道"
                selectedKey={form.channel_id}
                onSelectionChange={handleChannelChange}
                validate={(v) => (!v) ? '请选择渠道' : null}
              >
                <Label>渠道</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {channels.map((c) => (
                      <ListBox.Item key={String(c.id)} id={String(c.id)} textValue={c.name}>
                        {c.name}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
                <FieldError />
              </Select>

              <Select
                isRequired
                name="model_id"
                placeholder={form.channel_id ? '请选择模型' : '请先选择渠道'}
                selectedKey={form.model_id}
                onSelectionChange={set('model_id')}
                isDisabled={!form.channel_id}
                validate={(v) => (!v) ? '请选择模型' : null}
              >
                <Label>模型</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {channelModels.map((m) => (
                      <ListBox.Item key={String(m.id)} id={String(m.id)} textValue={m.name}>
                        {m.name}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
                <FieldError />
              </Select>
            </Form>
          </Modal.Body>

          <Modal.Footer>
            <Button slot="close" variant="secondary">
              取消
            </Button>
            <Button variant="primary" isPending={saving} onClick={() => formRef.current?.requestSubmit()}>
              {saving ? '保存中…' : '保存'}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
