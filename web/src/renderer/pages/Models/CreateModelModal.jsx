import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, toast } from '@heroui/react';

const emptyForm = {
  channel_id: '',
  name: '',
  input_price: '',
  output_price: '',
  cache_read_price: '',
  cache_write_price: '',
};

export default function CreateModelModal({ isOpen, onOpenChange, channels, onSubmit }) {
  const [form, setForm] = useState(emptyForm);
  const [saving, setSaving] = useState(false);
  const formRef = useRef(null);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  // 每次打开弹窗重置表单，确保不残留上次的值
  useEffect(() => {
    if (isOpen) {
      setForm(emptyForm);
      setSaving(false);
    }
  }, [isOpen]);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    setSaving(true);
    try {
      const data = {
        channel_id: Number(form.channel_id),
        name: form.name.trim(),
        ...(form.input_price !== '' && { input_price: Number(form.input_price) }),
        ...(form.output_price !== '' && { output_price: Number(form.output_price) }),
        ...(form.cache_read_price !== '' && { cache_read_price: Number(form.cache_read_price) }),
        ...(form.cache_write_price !== '' && { cache_write_price: Number(form.cache_write_price) }),
      };
      await onSubmit(data);
      setForm(emptyForm);
      onOpenChange(false);
    } catch (e) {
      toast.danger(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}>
      <Modal.Container size="md">
        <Modal.Dialog>
          <Modal.CloseTrigger />
          <Modal.Header>
            <Modal.Heading>新增模型</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
              <Select
                isRequired
                name="channel_id"
                placeholder="请选择渠道"
                selectedKey={form.channel_id}
                onSelectionChange={set('channel_id')}
                validate={(v) => (!v) ? '请选择渠道' : null}
              >
                <Label>所属渠道</Label>
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

              <TextField
                isRequired
                name="name"
                value={form.name}
                onChange={set('name')}
                autoFocus
                autoComplete="off"
                validate={(v) => (!v || !v.trim()) ? '请填写模型名称' : null}
              >
                <Label>模型名称</Label>
                <Input placeholder="例如：gpt-4o / claude-sonnet-4-20250514" />
                <FieldError />
              </TextField>

              <div className="grid grid-cols-2 gap-3">
                <TextField name="input_price" value={form.input_price} onChange={set('input_price')}>
                  <Label>输入价格 ($/M tokens)</Label>
                  <Input type="number" placeholder="0" />
                </TextField>
                <TextField name="output_price" value={form.output_price} onChange={set('output_price')}>
                  <Label>输出价格 ($/M tokens)</Label>
                  <Input type="number" placeholder="0" />
                </TextField>
                <TextField name="cache_read_price" value={form.cache_read_price} onChange={set('cache_read_price')}>
                  <Label>缓存读取 ($/M)</Label>
                  <Input type="number" placeholder="0" />
                </TextField>
                <TextField name="cache_write_price" value={form.cache_write_price} onChange={set('cache_write_price')}>
                  <Label>缓存写入 ($/M)</Label>
                  <Input type="number" placeholder="0" />
                </TextField>
              </div>
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
