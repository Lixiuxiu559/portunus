import { useState, useEffect } from 'react';
import { Button, Modal, Label, Input, TextField, Select, ListBox, Typography, Chip, toast, ScrollShadow } from '@heroui/react';
import { Plus, X, RefreshCw } from 'lucide-react';
import { createChannel, previewModels } from '../../api';

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
  const [models, setModels] = useState([]);
  const [syncing, setSyncing] = useState(false);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  // 每次打开弹窗重置表单
  useEffect(() => {
    if (isOpen) {
      setForm(emptyForm);
      setModels([]);
      setError('');
      setSyncing(false);
      setSaving(false);
    }
  }, [isOpen]);

  // 预览上游模型：不创建渠道，只获取模型列表
  const handleSyncModels = async () => {
    setSyncing(true);
    try {
      const res = await previewModels({
        type: form.type,
        base_url: form.base_url.trim(),
        key: form.key.trim(),
      });
      setModels(Array.isArray(res.models) ? res.models : []);
      toast.success(`获取到 ${(res.models || []).length} 个模型`);
    } catch (e) {
      toast.error(e.message || '获取模型失败');
    } finally {
      setSyncing(false);
    }
  };

  const handleRemoveModel = (name) => {
    setModels((prev) => prev.filter((m) => m !== name));
  };

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
      toast.success('渠道创建成功');
    } catch (e) {
      setError(e.message || '创建失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}>
      <Modal.Container size="xl">
        <Modal.Dialog className="w-[700px]">
          <Modal.CloseTrigger />
          <Modal.Header>
            <Modal.Heading>新增渠道</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <div className="flex flex-col gap-4">
              <div className="grid grid-cols-2 gap-3">
                <TextField
                  isRequired
                  name="name"
                  value={form.name}
                  onChange={set('name')}
                  autoFocus
                  autoComplete="off"
                >
                  <Label>渠道名称</Label>
                  <Input placeholder="例如：OpenAI 官方" />
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
              </div>

              {error && (
                <Typography color="danger" type="body-sm">
                  {error}
                </Typography>
              )}

              {/* 模型列表 */}
              <div className="border-t border-default-200 pt-4">
                <div className="mb-2 flex items-center justify-between">
                  <Typography type="body-sm" className="text-default-500">
                    模型列表
                  </Typography>
                  <Button
                    variant="secondary"
                    size="sm"
                    onPress={handleSyncModels}
                    isPending={syncing}
                    isDisabled={!form.base_url.trim() || !form.key.trim()}
                    className="h-7 px-2 gap-1 text-xs"
                  >
                    <RefreshCw className={`size-3 ${syncing ? 'animate-spin' : ''}`} />
                    同步模型
                  </Button>
                </div>
                {models.length === 0 ? (
                  <Typography type="body-sm" className="text-default-400">
                    暂无模型
                  </Typography>
                ) : (
                  <ScrollShadow
                    className="flex flex-wrap gap-2 max-h-[190px] p-1"
                    orientation="vertical"
                    hideScrollBar
                  >
                    {models.map((name) => (
                      <Chip
                        key={name}
                        variant="soft"
                        size="sm"
                      >
                        <span className="flex items-center gap-1">
                          {name}
                          <button
                            onClick={() => handleRemoveModel(name)}
                            className="ml-0.5 rounded-full p-0.5 hover:bg-danger/20 cursor-pointer"
                            aria-label={`移除模型 ${name}`}
                          >
                            <X className="size-3" />
                          </button>
                        </span>
                      </Chip>
                    ))}
                  </ScrollShadow>
                )}
              </div>
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
