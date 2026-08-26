import { useState } from 'react';
import { Button, Modal, Label, Input, TextField, Select, ListBox, Typography, Chip, toast, ScrollShadow } from '@heroui/react';
import { Plus, X, RefreshCw } from 'lucide-react';
import { createChannel, listModels, deleteModel, syncChannel } from '../../api';

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

  // 同步模型：先保存渠道，再调用同步接口拉取上游模型
  const handleSyncModels = async () => {
    setSyncing(true);
    try {
      const ch = await createChannel({
        name: form.name.trim(),
        type: form.type,
        base_url: form.base_url.trim(),
        key: form.key.trim(),
        enabled: form.enabled,
        auto_sync: form.auto_sync,
      });
      const res = await syncChannel(ch.id);
      toast.success(`同步完成，新增 ${res.added ?? 0} 个模型`);
      const ms = await listModels();
      setModels(Array.isArray(ms) ? ms : []);
      onCreated?.();
    } catch (e) {
      toast.error(e.message || '同步失败');
    } finally {
      setSyncing(false);
    }
  };

  const handleDeleteModel = async (m) => {
    try {
      await deleteModel(m.id);
      setModels((prev) => prev.filter((x) => x.id !== m.id));
      toast.success('模型已删除');
    } catch {
      // toast 由 request 拦截器统一提示
    }
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
                    {models.map((m) => (
                      <Chip
                        key={m.id}
                        variant="soft"
                        size="sm"
                      >
                        <span className="flex items-center gap-1">
                          {m.name}
                          <button
                            onClick={() => handleDeleteModel(m)}
                            className="ml-0.5 rounded-full p-0.5 hover:bg-danger/20 cursor-pointer"
                            aria-label={`删除模型 ${m.name}`}
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
