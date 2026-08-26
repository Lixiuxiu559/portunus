import { useState } from 'react';
import { Button, Modal, Typography, toast } from '@heroui/react';
import { deleteGroup } from '../../api';

export default function DeleteGroupModal({ group, isOpen, onOpenChange, onDeleted }) {
  const [deleting, setDeleting] = useState(false);

  const handleDelete = async () => {
    if (!group) return;
    setDeleting(true);
    try {
      await deleteGroup(group.id);
      onOpenChange(false);
      onDeleted?.();
      toast.success('分组已删除');
    } catch {
      // 错误提示由 request 拦截器统一处理
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
            <Modal.Heading>删除分组</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            <Typography color="muted">
              确定删除分组「{group?.name}」吗？分组内的所有模型关联将一并移除，删除后不可恢复。
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
