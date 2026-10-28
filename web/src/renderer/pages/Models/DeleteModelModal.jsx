import { useEffect, useRef, useState } from 'react';
import { Button, Modal, Typography } from '@heroui/react';
import { getModelDeletionImpact } from '../../api';
import DeletionImpactBody from '../../components/DeletionImpactBody';
import {
  buildDeletionImpactView,
  createDeletionImpactController,
  emptyDeletionImpactState,
} from '../../utils/deletionImpact';

// 删除模型确认弹窗：先加载引用影响，影响未知时禁止确认；删除与刷新由父组件分别承担。
export default function DeleteModelModal({ model, isOpen, onOpenChange, onDelete, onDeleted }) {
  const callbacks = useRef({ onOpenChange, onDelete, onDeleted });
  callbacks.current = { onOpenChange, onDelete, onDeleted };
  const [state, setState] = useState(emptyDeletionImpactState);
  const [controller] = useState(() => createDeletionImpactController({
    load: (target, options) => getModelDeletionImpact(target.id, options),
    // 用预览确认过的 target 删除，弹窗打开期间换了对象也不会删掉未预览的那个。
    remove: (target) => callbacks.current.onDelete(target),
    onChange: setState,
    onDeleted: (target) => {
      callbacks.current.onOpenChange(false);
      callbacks.current.onDeleted?.(target);
    },
  }));

  const modelId = model?.id;
  useEffect(() => {
    if (!isOpen || modelId === undefined || modelId === null) {
      controller.cancel();
      return undefined;
    }
    controller.open({ kind: 'model', id: modelId, name: model?.name });
    return () => controller.cancel();
  }, [controller, isOpen, modelId]);

  const deleting = state.status === 'deleting';
  const canConfirm = state.status === 'ready' && state.impact !== null;
  const view = state.impact ? buildDeletionImpactView('model', state.impact) : null;
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
            <Modal.Heading>删除模型</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            <div className="flex flex-col gap-3">
              <Typography color="muted">
                确定删除模型「{model?.name}」吗？引用此模型的分组成员会被移除，分组名称和历史调用记录会保留；手动选定项清除后不会自动补位，删除后不可恢复。
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
