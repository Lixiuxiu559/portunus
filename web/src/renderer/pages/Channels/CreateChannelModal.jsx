import { useState } from 'react';
import { Button, Modal, Label, Input, TextField, Select, ListBox, Typography } from '@heroui/react';
import { Plus } from 'lucide-react';
import { createChannel } from '../../api';

// 协议类型与后端 protocol.Provider 保持一致
const providerOptions = [
  { id: 'openai', label: 'OpenAI Chat' },
  { id: 'openai_responses', label: 'OpenAI Responses' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'gemini', label: 'Gemini' },
];

// 各协议的默认 Base URL，选中后自动填充
const defaultBaseURLs = {
  openai: 'https://api.openai.com',
  openai_responses: 'https://api.openai.com',
  anthropic: 'https://api.anthropic.com',
  gemini: 'https://generativelanguage.googleapis.com',
};

const emptyForm = {
  name: '',
  type: 'openai',
  base_url: '',
  key: '',
  enabled: true,
  auto_sync: true,
};

export default function CreateChannelModal({ isOpen, onOpenChange, onCreated }) {
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const [error, setError] = useState('');

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  const handleSubmit = async () => {
    setError('');
    if (!form.name.trim() || !form.base_url.trim() || !form.key.trim()) {
      setError('请填写名称、Base URL 与 Key');
      return;
    }
    setSaving(true);
    try {
      await createChannel({
        name: form.name.trim(),
        type: form.type,
        base_url: form.base_url.trim(),
        key: form.key.trim(),
        enabled: form.enabled,
        auto_sync: form.auto_sync,
      });
      setForm(emptyForm);
      onOpenChange(false);
      onCreated?.();
    } catch (e) {
      setError(e.message || '创建失败');
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
            <Modal.Heading>新增渠道</Modal.Heading>
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

              <TextField isRequired name="key" type="password" value={form.key} onChange={set('key')} autoComplete="new-password">
                <Label>API Key</Label>
                <Input placeholder="sk-..." />
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
