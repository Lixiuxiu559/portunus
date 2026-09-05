import { useMemo, useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, ComboBox, toast } from '@heroui/react';
import { createGroup } from '../../api';

const strategyOptions = [
  { id: 'manual', label: '手动选择' },
  { id: 'round_robin', label: '轮询' },
  { id: 'failover', label: '故障转移' },
];

const emptyForm = { name: '', strategy: 'manual', channel_id: '', model_id: '' };

export default function CreateGroupModal({ isOpen, onOpenChange, onCreated, channels, models }) {
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState(emptyForm);
  const [modelSearch, setModelSearch] = useState('');
  const formRef = useRef(null);
  const modelInputRef = useRef(null);
  // 选择后菜单关闭动画结束时会恢复焦点到输入框并重开菜单，
  // 用一次性标志拦截这次 focus：立即 blur 让弹层真正收起。
  const suppressFocusRef = useRef(false);

  const set = (field) => (value) => setForm((f) => ({ ...f, [field]: value }));

  // 每次打开弹窗重置表单，确保不残留上次的值
  useEffect(() => {
    if (isOpen) {
      setForm(emptyForm);
      setModelSearch('');
      setSaving(false);
    }
  }, [isOpen]);

  // 切换渠道时清空已选模型（渠道变了，原模型不再属于新渠道）
  const handleChannelChange = (value) => {
    setForm((f) => ({ ...f, channel_id: value, model_id: '' }));
    setModelSearch('');
  };

  // 当前渠道下的模型
  const channelModels = useMemo(
    () => models.filter((m) => m.channel_id === Number(form.channel_id)),
    [models, form.channel_id],
  );

  // 按名称模糊匹配过滤（不区分大小写）；搜索词等于已选模型名时视为未过滤
  const selectedName = models.find((m) => String(m.id) === String(form.model_id))?.name;
  const searching = modelSearch !== '' && modelSearch !== selectedName;
  const filteredModels = searching
    ? channelModels.filter((m) => m.name.toLowerCase().includes(modelSearch.toLowerCase()))
    : channelModels;

  // 选中后把搜索词设为模型名：输入框直接显示所选模型。
  // 并让输入框失焦：焦点已在其上，再点击不会触发 focus 打不开弹层，blur 后重新点击即可打开完整列表。
  const handleModelSelect = (v) => {
    // blur 触发的 commitSelection 会用旧闭包里的 selectedKey(null) 二次回调，忽略
    if (!v) return;
    set('model_id')(v);
    const m = models.find((x) => String(x.id) === String(v));
    setModelSearch(m ? m.name : '');
    suppressFocusRef.current = true;
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!formRef.current?.checkValidity()) return;
    if (!form.channel_id || !form.model_id) return;
    setSaving(true);
    try {
      await createGroup({
        name: form.name.trim(),
        strategy: form.strategy,
        items: [{ model_id: Number(form.model_id), priority: 0 }],
      });
      setForm(emptyForm);
      onOpenChange(false);
      onCreated?.();
      toast.success('分组创建成功');
    } catch (e) {
      toast.danger(e.message || '创建失败');
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
            <Modal.Heading>新增分组</Modal.Heading>
          </Modal.Header>

          <Modal.Body>
            <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
              <TextField
                isRequired
                name="name"
                value={form.name}
                onChange={set('name')}
                autoFocus
                autoComplete="off"
                validate={(v) => (!v || !v.trim()) ? '请填写分组名称' : null}
              >
                <Label>分组名称</Label>
                <Input placeholder="例如：GPT-4 负载均衡" />
                <FieldError />
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

              <Select
                isRequired
                name="channel_id"
                placeholder="请选择渠道"
                selectedKey={form.channel_id}
                onSelectionChange={handleChannelChange}
                validate={(v) => (!v) ? '请选择渠道' : null}
              >
                <Label>渠道</Label>
                <Select.Trigger>
                  <Select.Value />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {channels.map((c) => (
                      <ListBox.Item key={String(c.id)} id={String(c.id)} textValue={c.name}>
                        {c.name}
                        <ListBox.ItemIndicator />
                      </ListBox.Item>
                    ))}
                  </ListBox>
                </Select.Popover>
                <FieldError />
              </Select>

              <ComboBox
                isRequired
                name="model_id"
                selectedKey={form.model_id}
                onSelectionChange={handleModelSelect}
                inputValue={modelSearch}
                onInputChange={setModelSearch}
                isDisabled={!form.channel_id}
                validate={(v) => (!v) ? '请选择模型' : null}
              >
                <Label>模型</Label>
                <ComboBox.InputGroup>
                  <Input
                    ref={modelInputRef}
                    onFocus={() => {
                      if (suppressFocusRef.current) {
                        suppressFocusRef.current = false;
                        modelInputRef.current?.blur();
                        // 焦点转移到分组名称输入框，
                        // 避免 Modal 焦点管理把焦点拉回模型输入框重开菜单。
                        formRef.current?.querySelector('input[name="name"]')?.focus();
                      }
                    }}
                    placeholder={form.channel_id ? '输入关键字模糊搜索模型…' : '请先选择渠道'}
                  />
                  <ComboBox.Trigger />
                </ComboBox.InputGroup>
                <FieldError />
                <ComboBox.Popover>
                  {filteredModels.length === 0 ? (
                    <div className="p-3 text-center text-sm text-muted">无匹配模型</div>
                  ) : (
                    <ListBox>
                      {filteredModels.map((m) => (
                        <ListBox.Item key={String(m.id)} id={String(m.id)} textValue={m.name}>
                          {m.name}
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      ))}
                    </ListBox>
                  )}
                </ComboBox.Popover>
              </ComboBox>
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
