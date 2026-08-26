import { useState, useEffect } from 'react';
import { Button, Modal, Label, Input, TextField, Select, ListBox, Typography, toast } from '@heroui/react';
import { updateGroup } from '../../api';

const strategyOptions = [
  { id: 'manual', label: '手动选择' },
  { id: 'round_robin', label: '轮询' },
  { id: 'failover', label: '故障转移' },
];

export default function EditGroupModal({ group, isOpen, onOpenChange, onUpdated }) {
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({ name: '', strategy: 'manual' });
  const [error, setError] = useState('');

  useEffect(() => {
    if (isOpen && group) {
      setForm({ name: group.name || '', strategy: group.strategy || 'manual' });
      setError('');
    }
  }, [isOpen, group]);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  const handleSubmit = async () => {
    setError('');
    if (!form.name.trim()) {
      setError('请填写分组名称');
      return;
    }
    setSaving(true);
    try {
      await updateGroup(group.id, { name: form.name.trim(), strategy: form.strategy });
      onOpenChange(false);
      onUpdated?.();
      toast.success('分组更新成功');
    } catch (e) {
      setError(e.message || '更新失败');
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
            <Modal.Heading>编辑分组</Modal.Heading>
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
                <Label>分组名称</Label>
                <Input placeholder="例如：GPT-4 负载均衡" />
              </TextField>

              <Select
                isRequired
                name="strategy"
                selectedKey={form.strategy}
                onSelectionChange={set('strategy')}
              >
                <Label>路由策略</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {strategyOptions.map((o) => (
                      <ListBox.Item key={o.id} id={o.id} textValue={o.label}>
                        {o.label}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>

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
