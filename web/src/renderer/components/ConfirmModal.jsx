/**
 * 通用二次确认弹窗：行内删除/移除等破坏性小操作的轻量确认。
 * 与各页 DeleteXxxModal 同一视觉模式，避免每处复制一份 Modal 样板。
 * onConfirm 内不自行 try/catch——抛错时此处接住并保持弹窗打开（toast 由 request 拦截器统一提示）。
 */
import { useState } from 'react';
import { Button, Modal, Typography } from '@heroui/react';

export default function ConfirmModal({
  isOpen,
  onOpenChange,
  title,
  description,
  confirmText = '确认',
  pendingText = '处理中…',
  onConfirm,
}) {
  const [pending, setPending] = useState(false);

  const handleConfirm = async () => {
    setPending(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } catch {
      // 失败保持弹窗打开，错误提示由 request 拦截器统一处理
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}>
      <Modal.Container size="sm">
        <Modal.Dialog>
          <Modal.CloseTrigger />
          <Modal.Header>
            <Modal.Heading>{title}</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            <Typography color="muted">{description}</Typography>
          </Modal.Body>
          <Modal.Footer>
            <Button slot="close" variant="secondary">
              取消
            </Button>
            <Button variant="danger" isPending={pending} onPress={handleConfirm}>
              {pending ? pendingText : confirmText}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
