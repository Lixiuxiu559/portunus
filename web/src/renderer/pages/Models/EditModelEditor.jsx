import { useState, useRef } from 'react';
import { Button, Label, Input, TextField, Form, Typography, Select, ListBox, toast } from '@heroui/react';

// 编辑表单：由 ExpandEditor 挂载，model 恒非空；onClose 走反向收起动画，onSubmit 提交后由父组件就地更新列表。
// 改货币只改解读口径，不换算已存数字。
export default function EditModelEditor({ model, channelName, onClose, onSubmit }) {
  const [form, setForm] = useState({
    currency: model?.currency || 'USD',
    input_price: model?.input_price ?? '',
    output_price: model?.output_price ?? '',
    cache_read_price: model?.cache_read_price ?? '',
    cache_write_price: model?.cache_write_price ?? '',
  });
  const [saving, setSaving] = useState(false);
  const formRef = useRef(null);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));
  const symbol = form.currency === 'CNY' ? '¥' : '$';

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    setSaving(true);
    try {
      // 四个价格字段始终提交：清空视为 0（移除价格）
      const data = {
        currency: form.currency,
        input_price: form.input_price === '' ? 0 : Number(form.input_price),
        output_price: form.output_price === '' ? 0 : Number(form.output_price),
        cache_read_price: form.cache_read_price === '' ? 0 : Number(form.cache_read_price),
        cache_write_price: form.cache_write_price === '' ? 0 : Number(form.cache_write_price),
      };
      await onSubmit(model.id, data);
      onClose();
    } catch (e) {
      toast.danger(e.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className="flex items-center gap-2 text-sm text-muted">
        <Typography type="body-sm" className="font-medium">
          {model?.name}
        </Typography>
        <span>·</span>
        <span>{channelName}</span>
      </div>

      <Select name="currency" selectedKey={form.currency} onSelectionChange={set('currency')}>
        <Label>计价货币</Label>
        <Select.Trigger>
          <Select.Value />
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            <ListBox.Item id="USD" textValue="USD（美元）">
              USD（美元）
              <ListBox.ItemIndicator />
            </ListBox.Item>
            <ListBox.Item id="CNY" textValue="CNY（人民币）">
              CNY（人民币）
              <ListBox.ItemIndicator />
            </ListBox.Item>
          </ListBox>
        </Select.Popover>
      </Select>

      <div className="grid grid-cols-2 gap-3">
        <TextField name="input_price" value={form.input_price} onChange={set('input_price')} autoFocus>
          <Label>输入价格 ({symbol}/M tokens)</Label>
          <Input type="number" placeholder="0" step="any" />
        </TextField>
        <TextField name="output_price" value={form.output_price} onChange={set('output_price')}>
          <Label>输出价格 ({symbol}/M tokens)</Label>
          <Input type="number" placeholder="0" step="any" />
        </TextField>
        <TextField name="cache_read_price" value={form.cache_read_price} onChange={set('cache_read_price')}>
          <Label>缓存读取 ({symbol}/M)</Label>
          <Input type="number" placeholder="0" step="any" />
        </TextField>
        <TextField name="cache_write_price" value={form.cache_write_price} onChange={set('cache_write_price')}>
          <Label>缓存写入 ({symbol}/M)</Label>
          <Input type="number" placeholder="0" step="any" />
        </TextField>
      </div>

      <div className="flex justify-end gap-2 border-t border-separator pt-4">
        <Button variant="secondary" onPress={onClose}>
          取消
        </Button>
        <Button variant="primary" isPending={saving} onClick={() => formRef.current?.requestSubmit()}>
          {saving ? '保存中…' : '保存'}
        </Button>
      </div>
    </Form>
  );
}
