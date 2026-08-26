import { useState } from 'react';
import { Button, Modal, Typography } from '@heroui/react';

export default function DeleteModelModal({ model, isOpen, onOpenChange, onDeleted }) {
  const [deleting, setDeleting] = useState(false);

  const handleDelete = async () => {
    if (!model) return;
    setDeleting(true);
    try {
      await onDeleted(model);
      onOpenChange(false);
    } catch {
      // toast 由 request 拦截器统一处理
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Modal.Backdrop isOpen={isOpen} onOpenChange={onOpenChange}>
      <Modal.Container size="sm">
        <Modal.Dialog>
          <Modal.CloseTrigger />
          <Modal.Header>
            <Modal.Heading>删除模型</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            <Typography color="muted">
              确定删除模型「{model?.name}」吗？删除后不可恢复。
            </Typography>
          </Modal.Body>
          <Modal.Footer>
            <Button slot="close" variant="secondary">
              取消
            </Button>
            <Button variant="danger" isPending={deleting} onPress={handleDelete}>
              {deleting ? '删除中…' : '删除'}
            </Button>
          </Modal.Footer>
        </Modal.Dialog>
      </Modal.Container>
    </Modal.Backdrop>
  );
}
