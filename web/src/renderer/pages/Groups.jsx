import { useEffect, useState } from 'react';
import { Button, Typography, Spinner, Chip } from '@heroui/react';
import { Plus, X, Trash2 } from 'lucide-react';
import {
  listGroups, createGroup, updateGroup, deleteGroup,
  addGroupItem, updateGroupItem, deleteGroupItem,
} from '../api';
import { listModels } from '../api';

const STRATEGIES = [
  { key: 'manual', label: '手动选择' },
  { key: 'round_robin', label: '轮询' },
  { key: 'failover', label: '故障转移' },
];

export default function Groups() {
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({ name: '', strategy: 'manual' });
  const [submitting, setSubmitting] = useState(false);
  const [addItemGroupId, setAddItemGroupId] = useState(null);
  const [itemForm, setItemForm] = useState({ model_id: '', priority: '0' });

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

  const resetForm = () => {
    setForm({ name: '', strategy: 'manual' });
    setEditId(null);
  };

  const openEdit = (g) => {
    setEditId(g.id);
    setForm({ name: g.name, strategy: g.strategy || 'manual' });
    setShowForm(true);
  };

  const handleSubmit = async () => {
    if (!form.name) return;
    setSubmitting(true);
    try {
      if (editId) {
        await updateGroup(editId, form);
      } else {
        await createGroup(form);
      }
      setShowForm(false);
      resetForm();
      await fetchAll();
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (g) => {
    await deleteGroup(g.id);
    setGroups((prev) => prev.filter((x) => x.id !== g.id));
  };

  const handleAddItem = async () => {
    if (!itemForm.model_id) return;
    await addGroupItem(addItemGroupId, {
      model_id: Number(itemForm.model_id),
      priority: Number(itemForm.priority) || 0,
    });
    setAddItemGroupId(null);
    setItemForm({ model_id: '', priority: '0' });
    await fetchAll();
  };

  const handleRemoveItem = async (groupId, itemId) => {
    await deleteGroupItem(groupId, itemId);
    await fetchAll();
  };

  const handleSetActive = async (groupId, itemId) => {
    await updateGroup(groupId, { active_item_id: itemId });
    await fetchAll();
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">分组管理</Typography>
        <Button
          variant="primary"
          size="md"
          onPress={() => {
            resetForm();
            setShowForm(true);
          }}
        >
          <Plus className="size-4" />
          新增分组
        </Button>
      </div>

      {loading ? (
        <div className="flex justify-center py-12"><Spinner /></div>
      ) : groups.length === 0 ? (
        <div className="flex justify-center py-16 text-muted">
          <Typography>暂无分组</Typography>
        </div>
      ) : (
        <div className="space-y-4">
          {groups.map((g) => (
            <div key={g.id} className="rounded-xl border border-separator p-5">
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-3">
                  <Typography className="font-medium text-lg">{g.name}</Typography>
                  <Chip size="sm" variant="flat">
                    {STRATEGIES.find((s) => s.key === g.strategy)?.label || g.strategy}
                  </Chip>
                </div>
                <div className="flex items-center gap-1">
                  <Button size="sm" variant="ghost" onPress={() => openEdit(g)}>编辑</Button>
                  <Button size="sm" variant="ghost" className="text-red-500" onPress={() => handleDelete(g)}>
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
              </div>

              {/* 分组项列表 */}
              {g.items && g.items.length > 0 ? (
                <div className="space-y-1">
                  {g.items.map((item) => (
                    <div
                      key={item.id}
                      className={`flex items-center justify-between rounded-lg px-3 py-2 text-sm ${
                        g.active_item_id === item.id
                          ? 'bg-primary/10 border border-primary/30'
                          : 'bg-accent/5'
                      }`}
                    >
                      <div className="flex items-center gap-2">
                        <span className="font-medium">{modelMap[item.model_id] || `#${item.model_id}`}</span>
                        <span className="text-muted">优先级 {item.priority ?? 0}</span>
                        {g.active_item_id === item.id && (
                          <Chip size="sm" variant="primary" className="text-xs">当前活跃</Chip>
                        )}
                      </div>
                      <div className="flex items-center gap-1">
                        {g.active_item_id !== item.id && (
                          <Button size="sm" variant="ghost" onPress={() => handleSetActive(g.id, item.id)}>
                            设为活跃
                          </Button>
                        )}
                        <Button
                          size="sm" variant="ghost" className="text-red-500"
                          onPress={() => handleRemoveItem(g.id, item.id)}
                        >
                          <X className="size-3" />
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <Typography type="body-sm" className="text-muted">暂无模型</Typography>
              )}

              <Button
                size="sm" variant="ghost" className="mt-2"
                onPress={() => {
                  setAddItemGroupId(g.id);
                  setItemForm({ model_id: '', priority: '0' });
                }}
              >
                <Plus className="size-3.5" /> 添加模型
              </Button>
            </div>
          ))}
        </div>
      )}

      {/* 新增/编辑分组弹窗 */}
      {showForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={() => setShowForm(false)}>
          <div className="bg-background rounded-xl shadow-xl p-6 w-full max-w-sm space-y-4" onClick={(e) => e.stopPropagation()}>
            <Typography type="h3">{editId ? '编辑分组' : '新增分组'}</Typography>
            <input
              className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm"
              placeholder="分组名称"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
            <select
              className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm"
              value={form.strategy}
              onChange={(e) => setForm((f) => ({ ...f, strategy: e.target.value }))}
            >
              {STRATEGIES.map((s) => (
                <option key={s.key} value={s.key}>{s.label}</option>
              ))}
            </select>
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onPress={() => setShowForm(false)}>取消</Button>
              <Button variant="primary" isLoading={submitting} onPress={handleSubmit}>
                {editId ? '保存' : '创建'}
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* 添加分组项弹窗 */}
      {addItemGroupId && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={() => setAddItemGroupId(null)}>
          <div className="bg-background rounded-xl shadow-xl p-6 w-full max-w-sm space-y-4" onClick={(e) => e.stopPropagation()}>
            <Typography type="h3">添加模型到分组</Typography>
            <select
              className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm"
              value={itemForm.model_id}
              onChange={(e) => setItemForm((f) => ({ ...f, model_id: e.target.value }))}
            >
              <option value="">选择模型</option>
              {models
                .filter((m) => {
                  const group = groups.find((g) => g.id === addItemGroupId);
                  return !group?.items?.some((i) => i.model_id === m.id);
                })
                .map((m) => (
                  <option key={m.id} value={String(m.id)}>{m.name}</option>
                ))}
            </select>
            <input
              className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm"
              type="number"
              placeholder="优先级"
              value={itemForm.priority}
              onChange={(e) => setItemForm((f) => ({ ...f, priority: e.target.value }))}
            />
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onPress={() => setAddItemGroupId(null)}>取消</Button>
              <Button variant="primary" isLoading={submitting} onPress={handleAddItem}>添加</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}