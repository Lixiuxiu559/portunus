import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, toast } from '@heroui/react';
import { addGroupItem } from '../../api';

export default function AddGroupItemModal({ group, models, channels, isOpen, onOpenChange, onAdded }) {
  const [saving, setSaving] = useState(false);
  const [channelId, setChannelId] = useState('');
  const [modelId, setModelId] = useState('');
  const [priority, setPriority] = useState('0');
  const [modelSearch, setModelSearch] = useState('');
  const formRef = useRef(null);

  // 每次打开弹窗重置，确保不残留上次的选择
  useEffect(() => {
    if (isOpen) {
      setChannelId('');
      setModelId('');
      setPriority('0');
      setModelSearch('');
      setSaving(false);
    }
  }, [isOpen]);

  // 过滤掉分组中已有的模型
  const existingIds = new Set((group?.items || []).map((i) => i.model_id));
  const available = models.filter((m) => !existingIds.has(m.id));
  const channelFiltered = channelId
    ? available.filter((m) => String(m.channel_id) === channelId)
    : available;
  const filtered = modelSearch
    ? channelFiltered.filter((m) => m.name.toLowerCase().includes(modelSearch.toLowerCase()))
    : channelFiltered;

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    if (!modelId) return;
    setSaving(true);
    try {
      await addGroupItem(group.id, {
        model_id: Number(modelId),
        priority: Number(priority) || 0,
      });
      onOpenChange(false);
      onAdded?.();
      toast.success('模型已添加到分组');
    } catch (e) {
      toast.error(e.message || '添加失败');
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
            <Modal.Heading>添加模型到「{group?.name}」</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
              <Select
                name="channel_id"
                placeholder="全部渠道"
                selectedKey={channelId}
                onSelectionChange={(v) => { setChannelId(v); setModelId(''); }}
              >
                <Label>选择渠道</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    <ListBox.Item key="" id="" textValue="全部渠道">
                      全部渠道
                      <ListBox.ItemIndicator />
                    </ListBox.Item>
                    {(channels || []).map((c) => (
                      <ListBox.Item key={String(c.id)} id={String(c.id)} textValue={c.name}>
                        {c.name}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>

              <Select
                isRequired
                name="model_id"
                placeholder="请选择模型"
                selectedKey={modelId}
                onSelectionChange={setModelId}
                validate={(v) => (!v) ? '请选择模型' : null}
              >
                <Label>选择模型</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <div className="p-2">
                    <Input
                      type="text"
                      size="xs"
                      placeholder="搜索模型…"
                      value={modelSearch}
                      onChange={(e) => setModelSearch(e.target.value)}
                      className="w-full"
                      onClick={(e) => e.stopPropagation()}
                    />
                  </div>
                  <ListBox>
                    {filtered.map((m) => (
                      <ListBox.Item key={m.id} id={String(m.id)} textValue={m.name}>
                        {m.name}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
                <FieldError />
              </Select>

              <TextField
                name="priority"
                type="number"
                value={priority}
                onChange={setPriority}
              >
                <Label>优先级</Label>
                <Input placeholder="0" />
              </TextField>
            </Form>
          </Modal.Body>

          <Modal.Footer>
            <Button slot="close" variant="secondary">
              取消
            </Button>
            <Button variant="primary" isPending={saving} onClick={() => formRef.current?.requestSubmit()}>
              {saving ? '添加中…' : '添加'}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
