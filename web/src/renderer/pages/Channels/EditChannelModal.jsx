import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, Typography, Chip, toast, ScrollShadow } from '@heroui/react';
import { X, RefreshCw } from 'lucide-react';
import { updateChannel, listModels, deleteModel, syncChannel } from '../../api';

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
  const [models, setModels] = useState([]);
  const [syncing, setSyncing] = useState(false);
  const [modelSearch, setModelSearch] = useState('');
  const formRef = useRef(null);

  // 打开弹窗时用渠道数据填充表单，并加载已有模型
  useEffect(() => {
    if (!isOpen || !channel) return;
    setForm({
      name: channel.name || '',
      type: channel.type || 'openai',
      base_url: channel.base_url || '',
      key: '',
    });
    setModels([]);
    listModels({ page: 1, page_size: 1000, channel_id: channel.id })
      .then((res) => {
        const all = Array.isArray(res?.data) ? res.data : [];
        setModels(all);
      })
      .catch(() => setModels([]));
  }, [isOpen, channel]);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  const handleSyncModels = async () => {
    if (!channel) return;
    setSyncing(true);
    try {
      const res = await syncChannel(channel.id);
      toast.success(`同步完成，新增 ${res.added ?? 0} 个模型`);
      const ms = await listModels({ page: 1, page_size: 1000, channel_id: channel.id });
      const all = Array.isArray(ms?.data) ? ms.data : [];
      setModels(all);
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

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
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
      toast.error(e.message || '更新失败');
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
            <Modal.Heading>编辑渠道</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
              <div className="grid grid-cols-2 gap-3">
                <TextField
                  isRequired
                  name="name"
                  value={form.name}
                  onChange={set('name')}
                  autoFocus
                  autoComplete="off"
                  validate={(v) => (!v || !v.trim()) ? '请填写渠道名称' : null}
                >
                  <Label>渠道名称</Label>
                  <Input placeholder="例如：OpenAI 官方" />
                  <FieldError />
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
                  validate={(v) => (!v || !v.trim()) ? '请填写 Base URL' : null}
                >
                  <Label>Base URL</Label>
                  <Input placeholder="https://api.openai.com" />
                  <FieldError />
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
              </div>

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
                    isDisabled={!channel}
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
                  <>
                    <Input
                      type="text"
                      size="xs"
                      placeholder="搜索模型…"
                      value={modelSearch}
                      onChange={(e) => setModelSearch(e.target.value)}
                      className="w-full mb-2"
                    />
                    <ScrollShadow
                      className="flex flex-wrap gap-2 max-h-[190px] p-1"
                      orientation="vertical"
                      hideScrollBar
                    >
                      {models
                        .filter((m) => !modelSearch || m.name.toLowerCase().includes(modelSearch.toLowerCase()))
                        .map((m) => (
                        <Chip key={m.id} variant="soft" size="sm">
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
                  </>
                )}
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
