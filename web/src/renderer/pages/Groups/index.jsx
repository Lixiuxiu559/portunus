import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { DragDropContext, Droppable, Draggable } from '@hello-pangea/dnd';
import { Button, Typography, Chip, Card, ScrollShadow, Spinner, toast } from '@heroui/react';
import { Plus, Trash2, Pencil, RotateCw, X, GripVertical, CircleCheck } from 'lucide-react';
import { listGroups, updateGroup, deleteGroup, updateGroupItem, deleteGroupItem, listModels, listChannels } from '../../api';
import IconButton from '../../components/IconButton';
import ConfirmModal from '../../components/ConfirmModal';
import ExpandEditor from '../../components/ExpandEditor';
import { captureCardRect } from '../../utils/expandRect';
import CreateGroupModal from './CreateGroupModal';
import EditGroupEditor from './EditGroupEditor';
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
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [editTarget, setEditTarget] = useState(null);
  // 展开编辑器的起点：点编辑按钮时记录的卡片矩形
  const [editRect, setEditRect] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [addItemTarget, setAddItemTarget] = useState(null);
  // 待确认移除的分组项（行内 X 触发确认，防误触丢配置）
  const [removeTarget, setRemoveTarget] = useState(null);
  const [showCreate, setShowCreate] = useState(false);
  // 首次加载失败标记：区分"真没数据"和"加载失败"，后者给重试入口
  const [loadError, setLoadError] = useState(false);

  const fetchAll = async (isRefresh = false) => {
    // 有数据时使用 refreshing（不遮挡内容），无数据时使用 loading（显示加载状态）
    const setRefreshState = isRefresh || groups.length > 0 ? setRefreshing : setLoading;
    setRefreshState(true);
    try {
      const [g, m, c] = await Promise.all([listGroups(), listModels({ page: 1, page_size: 1000 }), listChannels()]);
      setGroups(Array.isArray(g) ? g : []);
      setModels(Array.isArray(m?.data) ? m.data : []);
      setChannels(Array.isArray(c) ? c : []);
      setLoadError(false);
    } catch {
      // 无数据时进错误态而非伪造空态；已有数据时保留旧列表（toast 由拦截器提示）
      if (groups.length === 0) setLoadError(true);
    } finally {
      setRefreshState(false);
    }
  };

  useEffect(() => { fetchAll(); }, []);

  const modelMap = Object.fromEntries(models.map((m) => [m.id, m.name]));
  const channelMap = Object.fromEntries(channels.map((c) => [c.id, c.name]));

  const handleSetActive = async (groupId, itemId) => {
    try {
      await updateGroup(groupId, { active_item_id: itemId });
      toast.success('已切换活跃模型');
      await fetchAll();
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  // 行内 X 确认后的实际移除：不自行 catch，失败时 ConfirmModal 保持打开（拦截器统一提示）
  const performRemoveItem = async () => {
    await deleteGroupItem(removeTarget.groupId, removeTarget.itemId);
    toast.success('已移除模型');
    await fetchAll();
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
      <div className="flex items-center justify-end mb-4 shrink-0">
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="md"
            aria-label="刷新分组"
            onPress={() => fetchAll(true)}
            isPending={refreshing}
          >
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
          <Spinner size="sm" />
        </div>
      ) : loadError && groups.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>分组加载失败，请检查后端服务后重试</span>
          <Button variant="secondary" size="sm" onPress={() => fetchAll()}>
            重试
          </Button>
        </div>
      ) : groups.length === 0 ? (
        <div className="flex flex-col items-center gap-3 py-16 text-muted">
          <span>暂无分组</span>
          <Button variant="primary" size="sm" onPress={() => setShowCreate(true)}>
            <Plus className="size-4" />
            新增分组
          </Button>
        </div>
      ) : (
        <div className="flex-1 min-h-0 overflow-y-auto thin-scrollbar">
        <DragDropContext onDragEnd={handleDragEnd}>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 items-start">
          {groups.map((g) => (
            <Card key={g.id} data-expand-card className="gap-4 p-5 h-[24rem]">
              <Card.Header className="flex-row items-center justify-between gap-3 shrink-0">
                <div className="flex items-center gap-3">
                  <Typography className="font-medium text-lg">{g.name}</Typography>
                  <Chip size="sm" variant="soft">
                    {STRATEGIES.find((s) => s.key === g.strategy)?.label || g.strategy}
                  </Chip>
                </div>
                <div className="flex items-center gap-1">
                  <IconButton
                    label={`编辑分组 ${g.name}`}
                    onClick={(e) => {
                      setEditRect(captureCardRect(e.currentTarget.closest('[data-expand-card]')));
                      setEditTarget(g);
                    }}
                  >
                    <Pencil className="size-4" />
                  </IconButton>
                  <IconButton label={`删除分组 ${g.name}`} onClick={() => setDeleteTarget(g)} danger>
                    <Trash2 className="size-4" />
                  </IconButton>
                </div>
              </Card.Header>

              <Card.Content className="flex-1 min-h-0 p-0">
                <ScrollShadow orientation="vertical" className="h-full thin-scrollbar">
                {/* 分组项列表（可拖拽排序） */}
                {g.items && g.items.length > 0 ? (
                  <Droppable droppableId={String(g.id)}>
                    {(droppableProvided) => (
                      <div ref={droppableProvided.innerRef} {...droppableProvided.droppableProps} className="space-y-2">
                        {g.items.map((item, index) => {
                          const model = models.find((m) => m.id === item.model_id);
                          const channelName = model ? channelMap[model.channel_id] : null;
                          const name = modelMap[item.model_id] || `#${item.model_id}`;
                          const isActive = g.strategy === 'manual' && g.active_item_id === item.id;
                          return (
                            <Draggable key={item.id} draggableId={String(item.id)} index={index}>
                              {(provided, snapshot) => {
                                // 视觉层与机械层分离：外层只承载 dnd 定位/位移（rbd 会写内联
                                // transform 与 transition），浮起的 scale/阴影放在内层，
                                // 两者互不覆盖，掉落滑行动画也不受影响。
                                const row = (
                                  <div
                                    ref={provided.innerRef}
                                    {...provided.draggableProps}
                                    className={snapshot.isDragging ? 'relative z-50' : undefined}
                                  >
                                    <div
                                      // 手动策略：点击行切换活跃项；键盘同样可达
                                      role={g.strategy === 'manual' ? 'button' : undefined}
                                      tabIndex={g.strategy === 'manual' ? 0 : undefined}
                                      aria-current={isActive}
                                      onClick={() => g.strategy === 'manual' && handleSetActive(g.id, item.id)}
                                      onKeyDown={(e) => {
                                        if (g.strategy === 'manual' && (e.key === 'Enter' || e.key === ' ')) {
                                          e.preventDefault();
                                          handleSetActive(g.id, item.id);
                                        }
                                      }}
                                      className={`flex items-center justify-between gap-2 rounded-xl border px-3 py-2 text-sm transition-[scale,box-shadow,border-color,background-color] duration-150 ease-out ${
                                        snapshot.isDragging
                                          ? // 按下拖拽：浮起（微放大 + 轻倾斜 + 重投影 + accent 描边）；reduced-motion 保持平面
                                            'rotate-[0.5deg] scale-[1.03] cursor-grabbing border-accent/40 bg-surface-secondary shadow-[0_20px_48px_rgb(0_0_0/0.24)] ring-1 ring-accent/30 motion-reduce:rotate-0 motion-reduce:scale-100 motion-reduce:transition-none'
                                          : isActive
                                            ? 'border-accent/30 bg-accent/10'
                                            : `border-foreground/10 bg-surface-secondary ${
                                                g.strategy === 'manual'
                                                  ? 'cursor-pointer hover:border-foreground/20 hover:bg-surface-tertiary'
                                                  : 'hover:border-foreground/20'
                                              }`
                                      }`}
                                    >
                                      <div className="flex min-w-0 items-center gap-2">
                                        <button
                                          aria-label="拖拽排序"
                                          {...provided.dragHandleProps}
                                          onClick={(e) => e.stopPropagation()}
                                          className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted cursor-grab active:cursor-grabbing transition-colors hover:bg-foreground/5 hover:text-foreground focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
                                        >
                                          <GripVertical className="size-4" aria-hidden="true" />
                                        </button>
                                        {/* 长模型名截断，悬浮显示全名 */}
                                        <span className="truncate font-medium" title={name}>
                                          {name}
                                        </span>
                                        {channelName && (
                                          <span className="shrink-0 text-xs text-muted">{channelName}</span>
                                        )}
                                      </div>
                                      <div className="flex shrink-0 items-center gap-1">
                                        {isActive && <CircleCheck className="size-4 text-success" aria-hidden="true" />}
                                        <IconButton
                                          label="移除模型"
                                          size="sm"
                                          danger
                                          onClick={(e) => {
                                            e.stopPropagation();
                                            setRemoveTarget({
                                              groupId: g.id,
                                              groupName: g.name,
                                              itemId: item.id,
                                              name,
                                            });
                                          }}
                                        >
                                          <X className="size-3.5" aria-hidden="true" />
                                        </IconButton>
                                      </div>
                                    </div>
                                  </div>
                                );
                                // 玻璃卡 .card 的 backdrop-filter 会成为 fixed 后代的包含块，
                                // rbd 拖拽元素的 position:fixed 坐标随之错乱（视觉上"直接消失"）。
                                // 官方修法：拖拽中传送到 document.body 渲染，落回原列表。
                                return snapshot.isDragging ? createPortal(row, document.body) : row;
                              }}
                            </Draggable>
                          );
                        })}
                        {droppableProvided.placeholder}
                      </div>
                    )}
                  </Droppable>
                ) : (
                  <Typography type="body-sm" className="text-muted">暂无模型</Typography>
                )}
                </ScrollShadow>
              </Card.Content>

              <Card.Footer className="shrink-0">
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
        </div>
      )}

      <CreateGroupModal
        isOpen={showCreate}
        onOpenChange={setShowCreate}
        onCreated={() => fetchAll(true)}
        channels={channels}
        models={models}
      />
      {editTarget && (
        <ExpandEditor
          triggerRect={editRect}
          onExited={() => setEditTarget(null)}
          width={420}
          ariaLabel="编辑分组"
          title={
            <Typography className="font-medium text-lg">{editTarget.name}</Typography>
          }
          status={
            <Chip size="sm" variant="soft">
              {STRATEGIES.find((s) => s.key === editTarget.strategy)?.label || editTarget.strategy}
            </Chip>
          }
        >
          {({ requestClose }) => (
            <EditGroupEditor
              group={editTarget}
              onClose={requestClose}
              onUpdated={() => fetchAll(true)}
            />
          )}
        </ExpandEditor>
      )}
      <DeleteGroupModal
        group={deleteTarget}
        isOpen={deleteTarget !== null}
        onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}
        onDeleted={() => fetchAll(true)}
      />
      <AddGroupItemModal
        group={addItemTarget}
        models={models}
        channels={channels}
        isOpen={addItemTarget !== null}
        onOpenChange={(open) => { if (!open) setAddItemTarget(null); }}
        onAdded={() => fetchAll(true)}
      />
      <ConfirmModal
        isOpen={removeTarget !== null}
        onOpenChange={(open) => !open && setRemoveTarget(null)}
        title="移除模型"
        description={`确定从分组「${removeTarget?.groupName}」移除「${removeTarget?.name}」吗？移除后可随时重新添加。`}
        confirmText="移除"
        pendingText="移除中…"
        onConfirm={performRemoveItem}
      />
    </div>
  );
}
