import { useState, useEffect, useRef } from 'react';
import { Button, Modal, Label, Input, TextField, FieldError, Form, Select, ListBox, ComboBox, toast } from '@heroui/react';
import { addGroupItem } from '../../api';

export default function AddGroupItemModal({ group, models, channels, isOpen, onOpenChange, onAdded }) {
  const [saving, setSaving] = useState(false);
  const [channelId, setChannelId] = useState('');
  const [modelId, setModelId] = useState('');
  const [priority, setPriority] = useState('0');
  const [modelSearch, setModelSearch] = useState('');
  const formRef = useRef(null);
  const modelInputRef = useRef(null);
  // 选择后菜单关闭动画结束时会恢复焦点到输入框并重开菜单，
  // 用一次性标志拦截这次 focus：立即 blur 让弹层真正收起。
  const suppressFocusRef = useRef(false);

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
  // 搜索词等于已选模型名时视为未过滤，保证再次打开下拉能看到完整列表
  const selectedName = models.find((m) => String(m.id) === String(modelId))?.name;
  const searching = modelSearch !== '' && modelSearch !== selectedName;
  const filtered = searching
    ? channelFiltered.filter((m) => m.name.toLowerCase().includes(modelSearch.toLowerCase()))
    : channelFiltered;

  // 选中后把搜索词设为模型名：输入框直接显示所选模型。
  // 并让输入框失焦：焦点已在其上，再点击不会触发 focus 打不开弹层，blur 后重新点击即可打开完整列表。
  const handleModelSelect = (v) => {
    // blur 触发的 commitSelection 会用旧闭包里的 selectedKey(null) 二次回调，忽略
    if (!v) return;
    setModelId(v);
    const m = models.find((x) => String(x.id) === String(v));
    setModelSearch(m ? m.name : '');
    suppressFocusRef.current = true;
  };

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
      toast.danger(e.message || '添加失败');
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

              <ComboBox
                isRequired
                name="model_id"
                selectedKey={modelId}
                onSelectionChange={handleModelSelect}
                inputValue={modelSearch}
                onInputChange={setModelSearch}
                validate={(v) => (!v) ? '请选择模型' : null}
              >
                <Label>选择模型</Label>
                <ComboBox.InputGroup>
                  <Input
                    ref={modelInputRef}
                    onFocus={() => {
                      if (suppressFocusRef.current) {
                        suppressFocusRef.current = false;
                        modelInputRef.current?.blur();
                        // 焦点转移到优先级输入框：选择完模型自然进入下一步，
                        // 也避免 Modal 焦点管理把焦点拉回模型输入框重开菜单。
                        formRef.current?.querySelector('input[name="priority"]')?.focus();
                      }
                    }}
                    placeholder="输入关键字模糊搜索模型…"
                  />
                  <ComboBox.Trigger />
                </ComboBox.InputGroup>
                <FieldError />
                <ComboBox.Popover>
                  {filtered.length === 0 ? (
                    <div className="p-3 text-center text-sm text-muted">无匹配模型</div>
                  ) : (
                    <ListBox>
                      {filtered.map((m) => (
                        <ListBox.Item key={m.id} id={String(m.id)} textValue={m.name}>
                          {m.name}
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      ))}
                    </ListBox>
                  )}
                </ComboBox.Popover>
              </ComboBox>

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
