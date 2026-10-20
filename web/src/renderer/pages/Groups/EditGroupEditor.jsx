import { useState, useRef } from 'react';
import { Button, Label, Input, TextField, FieldError, Form, Select, ListBox, toast } from '@heroui/react';
import { updateGroup } from '../../api';
import ConfirmModal from '../../components/ConfirmModal';
import { strategyDescriptions } from '../../utils/groupService';

const strategyOptions = [
  { id: 'manual', label: '手动选择' },
  { id: 'round_robin', label: '轮询' },
  { id: 'failover', label: '故障转移' },
];

// 编辑表单：由 ExpandEditor 挂载，group 恒非空；onClose 走反向收起动画，onUpdated 通知父组件刷新
export default function EditGroupEditor({ group, onClose, onUpdated }) {
  const [saving, setSaving] = useState(false);
  const [pendingUpdate, setPendingUpdate] = useState(null);
  const [form, setForm] = useState({
    name: group?.name || '',
    strategy: group?.strategy || 'manual',
  });
  const formRef = useRef(null);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  const save = async (data) => {
    setSaving(true);
    try {
      await updateGroup(group.id, data);
      onUpdated?.();
      onClose();
      toast.success('分组更新成功');
    } catch (e) {
      toast.danger(e.message || '更新失败');
      throw e;
    } finally {
      setSaving(false);
    }
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    const data = { name: form.name.trim(), strategy: form.strategy };
    if (data.name !== group.name) {
      setPendingUpdate(data);
      return;
    }
    try { await save(data); } catch { /* 提示由 save 处理 */ }
  };

  return (
    <>
    <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className="dialog-form-content">
      <TextField
        isRequired
        name="name"
        value={form.name}
        onChange={set('name')}
        autoFocus
        autoComplete="off"
        validate={(v) => (!v || !v.trim()) ? '请填写分组名称' : null}
      >
        <Label>分组名称（客户端 model）</Label>
        <Input placeholder="例如：coding、daily" />
        <FieldError />
        <p className="text-xs text-muted">更换组内模型保持此名称不变；改名后需要更新使用旧 model 的客户端。</p>
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
        <Select.Popover className="dialog-options">
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
      <p className="text-xs text-muted">{strategyDescriptions[form.strategy]}</p>

      </div>
        <div className="dialog-form-actions">
        <Button variant="secondary" onPress={onClose}>
          取消
        </Button>
        <Button variant="primary" isPending={saving} onClick={() => formRef.current?.requestSubmit()}>
          {saving ? '保存中…' : '保存'}
        </Button>
      </div>
    </Form>
    <ConfirmModal
      isOpen={pendingUpdate !== null}
      onOpenChange={(open) => !open && setPendingUpdate(null)}
      title="更改客户端模型名"
      description={`将「${group.name}」改为「${pendingUpdate?.name || ''}」后，使用旧 model 的客户端将无法调用，请同步更新它们的配置。仅切换上游时不需要改名。`}
      confirmText="确认改名"
      onConfirm={() => save(pendingUpdate)}
    />
    </>
  );
}
