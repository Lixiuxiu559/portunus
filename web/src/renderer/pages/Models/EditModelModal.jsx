import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Typography, toast } from '@heroui/react';

export default function EditModelModal({ model, channels, isOpen, onOpenChange, onSubmit }) {
  const [form, setForm] = useState({});
  const [saving, setSaving] = useState(false);
  const formRef = useRef(null);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  // 打开弹窗时用模型数据填充表单
  useEffect(() => {
    if (!isOpen || !model) return;
    setForm({
      input_price: model.input_price ?? '',
      output_price: model.output_price ?? '',
      cache_read_price: model.cache_read_price ?? '',
      cache_write_price: model.cache_write_price ?? '',
    });
    setSaving(false);
  }, [isOpen, model]);

  const channelName = channels.find((c) => c.id === model?.channel_id)?.name || '—';

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    setSaving(true);
    try {
      // 四个价格字段始终提交：清空视为 0（移除价格）
      const data = {
        input_price: form.input_price === '' ? 0 : Number(form.input_price),
        output_price: form.output_price === '' ? 0 : Number(form.output_price),
        cache_read_price: form.cache_read_price === '' ? 0 : Number(form.cache_read_price),
        cache_write_price: form.cache_write_price === '' ? 0 : Number(form.cache_write_price),
      };
      await onSubmit(model.id, data);
      onOpenChange(false);
    } catch (e) {
      toast.error(e.message || '保存失败');
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
            <Modal.Heading>编辑价格</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
              <div className="flex items-center gap-2 text-sm text-default-500">
                <Typography type="body-sm" className="font-medium">
                  {model?.name}
                </Typography>
                <span>·</span>
                <span>{channelName}</span>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <TextField name="input_price" value={form.input_price} onChange={set('input_price')}>
                  <Label>输入价格 ($/M tokens)</Label>
                  <Input type="number" placeholder="0" step="any" />
                </TextField>
                <TextField name="output_price" value={form.output_price} onChange={set('output_price')}>
                  <Label>输出价格 ($/M tokens)</Label>
                  <Input type="number" placeholder="0" step="any" />
                </TextField>
                <TextField name="cache_read_price" value={form.cache_read_price} onChange={set('cache_read_price')}>
                  <Label>缓存读取 ($/M)</Label>
                  <Input type="number" placeholder="0" step="any" />
                </TextField>
                <TextField name="cache_write_price" value={form.cache_write_price} onChange={set('cache_write_price')}>
                  <Label>缓存写入 ($/M)</Label>
                  <Input type="number" placeholder="0" step="any" />
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