import { useEffect, useRef, useState } from 'react';
import { Button, Form, toast } from '@heroui/react';
import { deleteModel, listModels, updateChannel } from '../../api';
import ConfirmModal from '../../components/ConfirmModal';
import ConnectionFields from './ConnectionFields';
import ModelPicker from './ModelPicker';
import CallTestPanel from './CallTestPanel';
import useChannelDraft from './useChannelDraft';
import { savePayload } from './channelDraft';

export default function EditChannelEditor({ channel, onClose, onUpdated }) {
  const state = useChannelDraft(channel);
  const [saving, setSaving] = useState(false);
  const [models, setModels] = useState([]);
  const [modelsError, setModelsError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null);
  const formRef = useRef(null);
  const active = useRef(true);
  const loadModels = async () => {
    setModelsError('');
    try {
      // 按实际分页读取，避免模型多时只编辑到前 1000 项。
      const found = [];
      let page = 1;
      let total;
      do {
        const result = await listModels({ page, page_size: 100, channel_id: channel.id });
        if (!active.current) return;
        const items = Array.isArray(result?.data) ? result.data : [];
        found.push(...items);
        total = result.total ?? found.length;
        if (!items.length) break;
        page += 1;
      } while (found.length < total);
      if (active.current) setModels(found);
    } catch (error) { if (active.current) setModelsError(error.message || '已有模型加载失败'); }
  };
  useEffect(() => {
    active.current = true;
    loadModels();
    return () => { active.current = false; };
  }, [channel.id]);

  const handleDeleteModel = async () => {
    await deleteModel(deleteTarget.id);
    setModels((old) => old.filter((model) => model.id !== deleteTarget.id));
    setDeleteTarget(null);
    toast.success('模型已删除');
  };
  const handleSubmit = async (event) => {
    event.preventDefault();
    if (saving || state.iconPending || !formRef.current?.checkValidity()) return;
    setSaving(true);
    try {
      await updateChannel(channel.id, savePayload(state.draft, state.selected));
      onUpdated?.(); onClose();
      toast.success('渠道已更新，所选新增模型已导入');
    } catch {
      // 请求层统一显示错误，保留草稿。
    } finally { setSaving(false); }
  };
  return <>
    <Form ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
      <div className="flex flex-col gap-4">
        <fieldset disabled={saving} className="flex flex-col gap-4 border-0 p-0 m-0 min-w-0">
          <ConnectionFields state={state} editing />
          {state.draft.type !== channel.type && <p role="alert" className="text-sm text-danger">
            协议修改将影响本渠道全部{modelsError ? '' : ` ${models.length} 个`}模型及其分组调用；现有模型 ID 和分组引用保留。保存后才生效。
          </p>}
          <ModelPicker state={state} existing={models} onDelete={setDeleteTarget} existingError={modelsError} onReload={loadModels} />
          <CallTestPanel state={state} existing={models} />
        </fieldset>
      </div>
      <div className="flex justify-end gap-2 mt-2">
        <Button variant="secondary" isDisabled={saving} onPress={onClose}>取消</Button>
        <Button variant="primary" isPending={saving} isDisabled={state.iconPending} onPress={() => formRef.current?.requestSubmit()}>{saving ? '保存中…' : '保存并导入所选模型'}</Button>
      </div>
    </Form>
    <ConfirmModal isOpen={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)} title="删除模型"
      description={`确定删除模型「${deleteTarget?.name}」吗？其分组关联也会移除，删除后不可恢复。`}
      confirmText="删除" pendingText="删除中…" onConfirm={handleDeleteModel} />
  </>;
}
