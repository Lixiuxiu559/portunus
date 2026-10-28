import { useEffect, useRef, useState } from 'react';
import { Button, Modal, Typography, toast } from '@heroui/react';
import { deleteChannel, getChannelDeletionImpact } from '../../api';
import DeletionImpactBody from '../../components/DeletionImpactBody';
import {
  buildDeletionImpactView,
  createDeletionImpactController,
  emptyDeletionImpactState,
} from '../../utils/deletionImpact';

// 删除渠道确认弹窗：先加载引用影响（分组、模型数、余额来源），影响未知时禁止确认。
export default function DeleteChannelModal({ channel, isOpen, onOpenChange, onDeleted }) {
  const callbacks = useRef({ onOpenChange, onDeleted });
  callbacks.current = { onOpenChange, onDeleted };
  const [state, setState] = useState(emptyDeletionImpactState);
  const [controller] = useState(() => createDeletionImpactController({
    load: (target, options) => getChannelDeletionImpact(target.id, options),
    remove: (target) => deleteChannel(target.id),
    onChange: setState,
    onDeleted: () => {
      callbacks.current.onOpenChange(false);
      callbacks.current.onDeleted?.();
      toast.success('渠道已删除');
    },
  }));

  const channelId = channel?.id;
  useEffect(() => {
    if (!isOpen || channelId === undefined || channelId === null) {
      controller.cancel();
      return undefined;
    }
    controller.open({ kind: 'channel', id: channelId, name: channel?.name });
    return () => controller.cancel();
  }, [controller, isOpen, channelId]);

  const deleting = state.status === 'deleting';
  const canConfirm = state.status === 'ready' && state.impact !== null;
  const view = state.impact ? buildDeletionImpactView('channel', state.impact) : null;
  const handleOpenChange = (open) => {
    if (open) return;
    if (deleting) return;
    controller.cancel();
    callbacks.current.onOpenChange(false);
  };

  return (
    <Modal.Backdrop
      isOpen={isOpen}
      onOpenChange={handleOpenChange}
      isDismissable={!deleting}
      isKeyboardDismissDisabled={deleting}
    >
      <Modal.Container size="sm">
        <Modal.Dialog>
          <Modal.CloseTrigger isDisabled={deleting} />
          <Modal.Header>
            <Modal.Heading>删除渠道</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            <div className="flex flex-col gap-3">
              <Typography color="muted">
                确定删除渠道「{channel?.name}」吗？该渠道下的模型及其分组关联也会移除，分组名称和历史调用记录会保留；手动选定项清除后不会自动补位，删除后不可恢复。
              </Typography>
              <DeletionImpactBody state={state} view={view} onRetry={() => controller.retry()} />
            </div>
          </Modal.Body>
          <Modal.Footer>
            <Button slot="close" variant="secondary" isDisabled={deleting}>
              取消
            </Button>
            <Button variant="danger" isPending={deleting} isDisabled={!canConfirm} onPress={() => controller.confirm()}>
              {deleting ? '删除中…' : '删除'}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
