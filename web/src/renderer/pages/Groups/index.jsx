import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { Link, useNavigate } from 'react-router-dom';
import { groupConfiguration, groupServicePath, strategyDescriptions } from '../../utils/groupService';
import { DragDropContext, Droppable, Draggable } from '@hello-pangea/dnd';
import { Button, Typography, Chip, Spinner, toast } from '@heroui/react';
import { Plus, Trash2, Pencil, RotateCw, X, GripVertical, Check, Layers2, ArrowUpRight, List, Info } from 'lucide-react';
import { listGroups, updateGroup, updateGroupItem, deleteGroupItem, listModels, listChannels } from '../../api';
import IconButton from '../../components/IconButton';
import ConfirmModal from '../../components/ConfirmModal';
import ExpandEditor from '../../components/ExpandEditor';
import { captureCardRect } from '../../utils/expandRect';
import CreateGroupModal from './CreateGroupModal';
import EditGroupEditor from './EditGroupEditor';
import DeleteGroupModal from './DeleteGroupModal';
import AddGroupItemModal from './AddGroupItemModal';
import './groups.css';

const STRATEGIES = [
  { key: 'manual', label: '手动选择' },
  { key: 'round_robin', label: '轮询' },
  { key: 'failover', label: '故障转移' },
];

function MemberRow({ provided, dragging, name, channelName, active, manual, sortable, index, onActivate, onRemove }) {
  const content = <><span className="group-member-name" title={name}>{name}</span><span className="group-member-channel">{channelName || '渠道信息未加载'}</span></>;
  const row = (
    <div ref={provided.innerRef} {...provided.draggableProps} className={`group-member ${active ? 'group-member--active' : ''} ${dragging ? 'group-member--dragging' : ''}`}>
      {sortable ? <button type="button" {...provided.dragHandleProps} aria-label={`拖拽调整 ${name} 的备用顺序`} className="group-member-order group-member-drag"><GripVertical className="size-3.5" /></button>
        : <span className="group-member-order" aria-hidden="true">{String(index + 1).padStart(2, '0')}</span>}
      {manual ? <button type="button" className="group-member-main group-member-select" aria-label={`设为当前模型 ${name}`} aria-pressed={active} onClick={onActivate}>{content}</button>
        : <div className="group-member-main">{content}</div>}
      {active && <span className="group-member-current"><Check className="size-3" />当前</span>}
      <IconButton label={`移除模型 ${name}`} size="sm" danger onClick={onRemove}><X className="size-3.5" /></IconButton>
    </div>
  );
  return dragging ? createPortal(row, document.body) : row;
}

export default function Groups() {
  const navigate = useNavigate();
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
      <div className="resource-page-heading">
        <div>
          <h1 className="text-xl font-semibold">模型分组</h1>
          <p className="mt-1 text-sm text-muted">按用途组织上游模型，让每个分组成为稳定的调用入口。</p>
        </div>
        <div className="resource-page-actions">
          <Button
            variant="secondary"
            size="md"
            aria-label="刷新分组"
            onPress={() => fetchAll(true)}
            isPending={refreshing}
          >
            <RotateCw className="size-4" />
          </Button>
          <Button variant="primary" size="md" isDisabled={loading || !models.length} onPress={() => setShowCreate(true)}>
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
          <span className="text-base font-medium text-foreground">创建你的第一个模型服务</span>
          <p>添加上游渠道 → 获取模型 → 创建分组 → 服务接入 → 查看调用记录</p>
          {channels.length === 0 ? (
            <Button variant="primary" size="sm" onPress={() => navigate('/channels')}>添加上游渠道</Button>
          ) : models.length === 0 ? (
            <Button variant="primary" size="sm" onPress={() => navigate('/models')}>获取或添加上游模型</Button>
          ) : null}
          <Button variant="secondary" size="sm" isDisabled={!models.length} onPress={() => setShowCreate(true)}>
            <Plus className="size-4" />
            新增分组
          </Button>
        </div>
      ) : (
        <div className="flex-1 min-h-0 overflow-y-auto thin-scrollbar">
        <DragDropContext onDragEnd={handleDragEnd}>
        <div className="group-services-grid">
          {groups.map((g) => {
            const configuration = groupConfiguration(g);
            return (
              <section key={g.id} data-expand-card className="group-service-card" aria-label={`模型分组 ${g.name}`}>
                <header className="group-service-header">
                  <span className="group-service-icon"><Layers2 className="size-4" /></span>
                  <div className="min-w-0 flex-1">
                    <h2 className="group-service-name" title={g.name}>{g.name}</h2>
                  </div>
                  <IconButton label={`编辑分组 ${g.name}`} onClick={(e) => {
                    setEditRect(captureCardRect(e.currentTarget.closest('[data-expand-card]')));
                    setEditTarget(g);
                  }}><Pencil className="size-3.5" /></IconButton>
                  <IconButton label={`删除分组 ${g.name}`} onClick={() => setDeleteTarget(g)} danger><Trash2 className="size-3.5" /></IconButton>
                </header>
                <div className="group-service-meta">
                  <span className="group-strategy" title={`${strategyDescriptions[g.strategy]} 流式已提交后不自动重放。`}>
                    {STRATEGIES.find((strategy) => strategy.key === g.strategy)?.label || g.strategy}
                    <Info className="size-3 text-muted" aria-hidden="true" />
                    <span className="group-meta-separator" aria-hidden="true">·</span>
                    <span>{g.items?.length || 0} 个成员</span>
                  </span>
                  <span className={`group-configuration ${configuration.complete ? '' : 'group-configuration--incomplete'}`} title={`${configuration.label}；配置状态不代表上游在线。`}>
                    <span className="group-configuration-dot" />{configuration.complete ? '已配置' : '待完善'}
                  </span>
                </div>
                {!configuration.complete && <p className="group-service-warning">{g.items?.length ? '选择一个当前模型以开始调用' : '添加上游模型以开始调用'}</p>}
                <div className="group-members-list thin-scrollbar">
                  {g.items?.length ? <Droppable droppableId={String(g.id)}>
                    {(droppableProvided) => <div ref={droppableProvided.innerRef} {...droppableProvided.droppableProps}>
                      {g.items.map((item, index) => {
                        const model = models.find((m) => m.id === item.model_id);
                        const name = modelMap[item.model_id] || `#${item.model_id}`;
                        const isActive = g.strategy === 'manual' && g.active_item_id === item.id;
                        return <Draggable key={item.id} draggableId={String(item.id)} index={index} disableInteractiveElementBlocking isDragDisabled={g.strategy !== 'failover'}>
                          {(provided, snapshot) => <MemberRow provided={provided} dragging={snapshot.isDragging} name={name}
                            channelName={model ? channelMap[model.channel_id] : null} index={index} active={isActive}
                            manual={g.strategy === 'manual'} sortable={g.strategy === 'failover'}
                            onActivate={() => handleSetActive(g.id, item.id)}
                            onRemove={() => setRemoveTarget({ groupId: g.id, groupName: g.name, itemId: item.id, name,
                              impact: g.items.length === 1 ? '这是最后一个成员，移除后分组将无法调用。' : isActive ? '这是当前手动选定项，移除后需要重新选择当前模型。' : '这会改变分组的候选模型或备用顺序。' })} />}
                        </Draggable>;
                      })}
                      {droppableProvided.placeholder}
                    </div>}
                  </Droppable> : <div className="group-members-empty"><Layers2 className="size-5" /><span>还没有上游模型</span></div>}
                </div>
                <footer className="group-service-footer">
                  <button type="button" className="group-add-member" onClick={() => setAddItemTarget(g)}><Plus className="size-3.5" />添加成员</button>
                  <div className="flex items-center gap-3">
                    <Link className="group-record-link" to={groupServicePath('/logs', g.name)} aria-label={`查看 ${g.name} 的调用记录`}><List className="size-3.5" />调用记录</Link>
                    <Link className="group-connect-link" to={groupServicePath('/clients', g.name)}>接入<ArrowUpRight className="size-3.5" /></Link>
                  </div>
                </footer>
              </section>
            );
          })}
        </div>
        <p className="mt-4 mb-2 text-xs text-muted">分组名称即客户端的 model。配置状态仅反映成员设置，上游可用性以实际调用为准。</p>
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
        description={`确定从分组「${removeTarget?.groupName}」移除「${removeTarget?.name}」吗？${removeTarget?.impact || ''} 上游模型仍保留，可重新添加。`}
        confirmText="移除"
        pendingText="移除中…"
        onConfirm={performRemoveItem}
      />
    </div>
  );
}
