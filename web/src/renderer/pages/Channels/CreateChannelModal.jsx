import { useRef, useState } from 'react';
import { Button, Form, Modal, toast } from '@heroui/react';
import { createChannel } from '../../api';
import ConnectionFields from './ConnectionFields';
import ModelPicker from './ModelPicker';
import CallTestPanel from './CallTestPanel';
import useChannelDraft from './useChannelDraft';
import { savePayload } from './channelDraft';

function CreateChannelForm({ onOpenChange, onCreated }) {
  const state = useChannelDraft();
  const [saving, setSaving] = useState(false);
  const formRef = useRef(null);
  const handleSubmit = async (event) => {
    event.preventDefault();
    if (saving || !state.draft.type || !formRef.current?.checkValidity()) return;
    setSaving(true);
    try {
      await createChannel(savePayload(state.draft, state.selected));
      toast.success(state.selected.length ? '渠道与所选模型已保存，请到模型分组中添加使用' : '渠道已保存，尚未导入模型');
      onOpenChange(false);
      onCreated?.();
    } catch {
      // 请求层统一显示错误；保持草稿便于修正。
    } finally { setSaving(false); }
  };
  return <>
    <Modal.Body>
      <Form id="create-channel-form" ref={formRef} onSubmit={handleSubmit} className="flex flex-col gap-4">
        <fieldset disabled={saving} className="flex flex-col gap-4 border-0 p-0 m-0 min-w-0">
          <ConnectionFields state={state} />
          <ModelPicker state={state} />
          <CallTestPanel state={state} />
        </fieldset>
      </Form>
    </Modal.Body>
    <Modal.Footer>
      <Button slot="close" variant="secondary" isDisabled={saving}>取消</Button>
      <Button variant="primary" isPending={saving} isDisabled={!state.draft.type} onPress={() => formRef.current?.requestSubmit()}>
        {saving ? '保存中…' : '保存渠道与所选模型'}
      </Button>
    </Modal.Footer>
  </>;
}

export default function CreateChannelModal({ isOpen, onOpenChange, onCreated }) {
  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}>
      <Modal.Container size="xl"><Modal.Dialog className="w-[700px]">
        <Modal.CloseTrigger />
        <Modal.Header><Modal.Heading>新增渠道</Modal.Heading></Modal.Header>
        {isOpen && <CreateChannelForm onOpenChange={onOpenChange} onCreated={onCreated} />}
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop>
  );
}
