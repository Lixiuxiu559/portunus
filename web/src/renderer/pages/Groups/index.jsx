import { useEffect, useState } from 'react';
import { DragDropContext, Droppable, Draggable } from '@hello-pangea/dnd';
import { Button, Typography, Chip, Card, toast } from '@heroui/react';
import { Plus, Trash2, Pencil, RotateCw, X, GripVertical, CircleCheck } from 'lucide-react';
import { listGroups, updateGroup, deleteGroup, updateGroupItem, deleteGroupItem, listModels } from '../../api';
import CreateGroupModal from './CreateGroupModal';
import EditGroupModal from './EditGroupModal';
import DeleteGroupModal from './DeleteGroupModal';
import AddGroupItemModal from './AddGroupItemModal';

const STRATEGIES = [
  { key: 'manual', label: '手动选择' },
  { key: 'round_robin', label: '轮询' },
  { key: 'failover', label: '故障转移' },
];

export default function Groups() {
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [editTarget, setEditTarget] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [addItemTarget, setAddItemTarget] = useState(null);
  const [showCreate, setShowCreate] = useState(false);

  const fetchAll = async () => {
    setLoading(true);
    try {
      const [g, m] = await Promise.all([listGroups(), listModels()]);
      setGroups(Array.isArray(g) ? g : []);
      setModels(Array.isArray(m) ? m : []);
    } catch {
      setGroups([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchAll(); }, []);

  const modelMap = Object.fromEntries(models.map((m) => [m.id, m.name]));

  const handleSetActive = async (groupId, itemId) => {
    try {
      await updateGroup(groupId, { active_item_id: itemId });
      toast.success('已切换活跃模型');
      await fetchAll();
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  const handleRemoveItem = async (groupId, itemId) => {
    try {
      await deleteGroupItem(groupId, itemId);
      toast.success('已移除模型');
      await fetchAll();
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  // 拖拽排序：组内重排后，按新顺序重写优先级并落库
  const handleDragEnd = async (result) => {
    const { source, destination } = result;
    if (!destination || source.droppableId !== destination.droppableId || source.index === destination.index) {
      return;
    }
    const groupId = Number(destination.droppableId);
    const group = groups.find((g) => g.id === groupId);
    if (!group?.items) return;

    // 乐观更新：本地先按新顺序渲染
    const items = Array.from(group.items);
    const [moved] = items.splice(source.index, 1);
    items.splice(destination.index, 0, moved);
    setGroups((prev) => prev.map((g) => (g.id === groupId ? { ...g, items } : g)));

    try {
      // 只提交优先级有变化的项
      await Promise.all(
        items.map((item, index) =>
          item.priority === index ? Promise.resolve() : updateGroupItem(groupId, item.id, { priority: index }),
        ),
      );
      toast.success('排序已保存');
      await fetchAll();
    } catch {
      // 失败则重新拉取，回滚为服务端顺序
      await fetchAll();
    }
  };

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">分组管理</Typography>
        <div className="flex items-center gap-2">
          <Button variant="secondary" size="md" onPress={fetchAll} isPending={loading}>
            <RotateCw className="size-4" />
          </Button>
          <Button variant="primary" size="md" onPress={() => setShowCreate(true)}>
            <Plus className="size-4" />
            新增分组
          </Button>
        </div>
      </div>

      {loading ? (
        <div className="flex justify-center py-12">
          <span className="text-muted">加载中…</span>
        </div>
      ) : groups.length === 0 ? (
        <div className="flex justify-center py-16 text-muted">
          暂无分组，点击右上角「新增分组」创建
        </div>
      ) : (
        <DragDropContext onDragEnd={handleDragEnd}>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 items-start">
          {groups.map((g) => (
            <Card key={g.id} className="gap-4 p-5">
              <Card.Header className="flex-row items-center justify-between gap-3">
                <div className="flex items-center gap-3">
                  <Typography className="font-medium text-lg">{g.name}</Typography>
                  <Chip size="sm" variant="soft">
                    {STRATEGIES.find((s) => s.key === g.strategy)?.label || g.strategy}
                  </Chip>
                </div>
                <div className="flex items-center gap-1">
                  <button
                    aria-label={`编辑分组 ${g.name}`}
                    onClick={() => setEditTarget(g)}
                    className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-accent/10 hover:text-accent cursor-pointer"
                  >
                    <Pencil className="size-4" />
                  </button>
                  <button
                    aria-label={`删除分组 ${g.name}`}
                    onClick={() => setDeleteTarget(g)}
                    className="flex size-7 items-center justify-center rounded-lg text-muted transition-colors hover:bg-danger/10 hover:text-danger cursor-pointer"
                  >
                    <Trash2 className="size-4" />
                  </button>
                </div>
              </Card.Header>

              <Card.Content>
                {/* 分组项列表（可拖拽排序） */}
                {g.items && g.items.length > 0 ? (
                  <Droppable droppableId={String(g.id)}>
                    {(droppableProvided) => (
                      <div ref={droppableProvided.innerRef} {...droppableProvided.droppableProps} className="space-y-1">
                        {g.items.map((item, index) => (
                          <Draggable key={item.id} draggableId={String(item.id)} index={index}>
                            {(draggableProvided, snapshot) => (
                              <div
                                ref={draggableProvided.innerRef}
                                {...draggableProvided.draggableProps}
                                onClick={() => handleSetActive(g.id, item.id)}
                                className={`flex items-center justify-between rounded-lg px-3 py-2 text-sm cursor-pointer shadow-sm ${
                                  g.active_item_id === item.id
                                    ? 'bg-accent/10'
                                    : ''
                                } ${snapshot.isDragging ? 'shadow-lg' : ''}`}
                              >
                                <div className="flex items-center gap-2">
                                  <button
                                    aria-label="拖拽排序"
                                    {...draggableProvided.dragHandleProps}
                                    onClick={(e) => e.stopPropagation()}
                                    className="flex size-6 items-center justify-center rounded text-muted cursor-grab active:cursor-grabbing outline-none focus:outline-none transition-colors hover:text-foreground"
                                  >
                                    <GripVertical className="size-4" />
                                  </button>
                                  <span className="font-medium">{modelMap[item.model_id] || `#${item.model_id}`}</span>
                                </div>
                                <div className="flex items-center gap-1">
                                  {g.active_item_id === item.id && (
                                    <CircleCheck className="size-4 text-success" />
                                  )}
                                  <button
                                    aria-label="移除模型"
                                    onClick={(e) => { e.stopPropagation(); handleRemoveItem(g.id, item.id); }}
                                    className="flex size-6 items-center justify-center rounded text-muted transition-colors hover:bg-danger/10 hover:text-danger cursor-pointer"
                                  >
                                    <X className="size-3.5" />
                                  </button>
                                </div>
                              </div>
                            )}
                          </Draggable>
                        ))}
                        {droppableProvided.placeholder}
                      </div>
                    )}
                  </Droppable>
                ) : (
                  <Typography type="body-sm" className="text-muted">暂无模型</Typography>
                )}
              </Card.Content>

              <Card.Footer>
                <Button
                  size="sm" variant="tertiary"
                  onPress={() => setAddItemTarget(g)}
                >
                  <Plus className="size-3.5" /> 添加模型
                </Button>
              </Card.Footer>
            </Card>
          ))}
        </div>
        </DragDropContext>
      )}

      <CreateGroupModal
        isOpen={showCreate}
        onOpenChange={setShowCreate}
        onCreated={fetchAll}
      />
      <EditGroupModal
        group={editTarget}
        isOpen={editTarget !== null}
        onOpenChange={(open) => { if (!open) setEditTarget(null); }}
        onUpdated={fetchAll}
      />
      <DeleteGroupModal
        group={deleteTarget}
        isOpen={deleteTarget !== null}
        onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}
        onDeleted={fetchAll}
      />
      <AddGroupItemModal
        group={addItemTarget}
        models={models}
        isOpen={addItemTarget !== null}
        onOpenChange={(open) => { if (!open) setAddItemTarget(null); }}
        onAdded={fetchAll}
      />
    </div>
  );
}
