import { useState } from 'react';
import { Button, Modal, Label, Input, TextField, Select, ListBox, Typography, toast } from '@heroui/react';
import { addGroupItem } from '../../api';

export default function AddGroupItemModal({ group, models, isOpen, onOpenChange, onAdded }) {
  const [saving, setSaving] = useState(false);
  const [modelId, setModelId] = useState('');
  const [priority, setPriority] = useState('0');
  const [error, setError] = useState('');

  // 过滤掉分组中已有的模型
  const existingIds = new Set((group?.items || []).map((i) => i.model_id));
  const available = models.filter((m) => !existingIds.has(m.id));

  const handleSubmit = async () => {
    setError('');
    if (!modelId) {
      setError('请选择模型');
      return;
    }
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
      setError(e.message || '添加失败');
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
            <div className="flex flex-col gap-4">
              <Select
                isRequired
                name="model_id"
                selectedKey={modelId}
                onSelectionChange={setModelId}
              >
                <Label>选择模型</Label>
                <Select.Trigger>
                  <Select.Value placeholder="请选择模型" />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {available.map((m) => (
                      <ListBox.Item key={m.id} id={String(m.id)} textValue={m.name}>
                        {m.name}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
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
              {saving ? '添加中…' : '添加'}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
