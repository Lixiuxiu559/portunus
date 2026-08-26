import { useState, useEffect } from 'react';
import { Button, Modal, Label, Input, TextField, Select, ListBox, Typography, toast } from '@heroui/react';
import { updateChannel } from '../../api';

// 协议类型与后端 protocol.Provider 保持一致
const providerOptions = [
  { id: 'openai', label: 'OpenAI Chat' },
  { id: 'openai_responses', label: 'OpenAI Responses' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'gemini', label: 'Gemini' },
];

export default function EditChannelModal({ channel, isOpen, onOpenChange, onUpdated }) {
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({ name: '', type: 'openai', base_url: '', key: '' });
  const [error, setError] = useState('');

  // 打开弹窗时用渠道数据填充表单
  useEffect(() => {
    if (isOpen && channel) {
      setForm({
        name: channel.name || '',
        type: channel.type || 'openai',
        base_url: channel.base_url || '',
        key: '', // key 脱敏，编辑时留空则不更新
      });
      setError('');
    }
  }, [isOpen, channel]);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  const handleSubmit = async () => {
    setError('');
    if (!form.name.trim() || !form.base_url.trim()) {
      setError('请填写名称与 Base URL');
      return;
    }
    setSaving(true);
    try {
      const payload = {
        name: form.name.trim(),
        type: form.type,
        base_url: form.base_url.trim(),
      };
      // key 仅在用户填写时更新
      if (form.key.trim()) {
        payload.key = form.key.trim();
      }
      await updateChannel(channel.id, payload);
      onOpenChange(false);
      onUpdated?.();
      toast.success('渠道更新成功');
    } catch (e) {
      setError(e.message || '更新失败');
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
            <Modal.Heading>编辑渠道</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <div className="flex flex-col gap-4">
              <TextField
                isRequired
                name="name"
                value={form.name}
                onChange={set('name')}
                autoFocus
                autoComplete="off"
              >
                <Label>渠道名称</Label>
                <Input placeholder="例如：OpenAI 官方 / Anthropic 直连" />
              </TextField>

              <Select
                isRequired
                name="type"
                selectedKey={form.type}
                onSelectionChange={set('type')}
              >
                <Label>协议类型</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {providerOptions.map((o) => (
                      <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
                        {o.label}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>

              <TextField
                isRequired
                name="base_url"
                value={form.base_url}
                onChange={set('base_url')}
                autoComplete="off"
              >
                <Label>Base URL</Label>
                <Input placeholder="https://api.openai.com" />
              </TextField>

              <TextField
                name="key"
                type="password"
                value={form.key}
                onChange={set('key')}
                autoComplete="new-password"
              >
                <Label>API Key</Label>
                <Input placeholder="留空则不更新" />
              </TextField>

              {error && (
                <Typography color="danger" type="body-sm">
                  {error}
                </Typography>
              )}
            </div>
          </Modal.Body>

          <Modal.Footer>
            <Button slot="close" variant="secondary">
              取消
            </Button>
            <Button variant="primary" isPending={saving} onPress={handleSubmit}>
              {saving ? '保存中…' : '保存'}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
