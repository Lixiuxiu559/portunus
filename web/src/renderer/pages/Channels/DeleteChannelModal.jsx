import { useState } from 'react';
import { Button, Modal, Typography } from '@heroui/react';
import { deleteChannel } from '../../api';

// 删除渠道确认弹窗：由渠道卡片右上角的 Close 图标触发
export default function DeleteChannelModal({ channel, isOpen, onOpenChange, onDeleted }) {
  const [deleting, setDeleting] = useState(false);

  const handleDelete = async () => {
    if (!channel) return;
    setDeleting(true);
    try {
      await deleteChannel(channel.id);
      onOpenChange(false);
      onDeleted?.();
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
            <Modal.Heading>删除渠道</Modal.Heading>
          </Modal.Header>
          <Modal.Body>
            <Typography color="muted">
              确定删除渠道「{channel?.name}」吗？删除后不可恢复。
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