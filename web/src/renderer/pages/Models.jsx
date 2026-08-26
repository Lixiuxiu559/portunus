import { useEffect, useState } from 'react';
import { Button, Typography, Spinner } from '@heroui/react';
import { X } from 'lucide-react';
import { listModels, updateModel, createModel, deleteModel } from '../api';
import { listChannels } from '../api';

export default function Models() {
  const [models, setModels] = useState([]);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [togglingId, setTogglingId] = useState(null);
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({ channel_id: '', name: '', input_price: '', output_price: '', cache_read_price: '', cache_write_price: '' });
  const [submitting, setSubmitting] = useState(false);

  const fetchAll = async () => {
    setLoading(true);
    try {
      const [m, c] = await Promise.all([listModels(), listChannels()]);
      setModels(Array.isArray(m) ? m : []);
      setChannels(Array.isArray(c) ? c : []);
    } catch {
      setModels([]);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchAll(); }, []);

  const channelMap = Object.fromEntries(channels.map((c) => [c.id, c.name]));

  const handleToggle = async (m) => {
    setTogglingId(m.id);
    try {
      await updateModel(m.id, { enabled: !m.enabled });
      setModels((prev) => prev.map((x) => (x.id === m.id ? { ...x, enabled: !x.enabled } : x)));
    } finally {
      setTogglingId(null);
    }
  };

  const handleDelete = async (m) => {
    await deleteModel(m.id);
    setModels((prev) => prev.filter((x) => x.id !== m.id));
  };

  const resetForm = () => {
    setForm({ channel_id: '', name: '', input_price: '', output_price: '', cache_read_price: '', cache_write_price: '' });
    setEditId(null);
  };

  const openEdit = (m) => {
    setEditId(m.id);
    setForm({
      channel_id: String(m.channel_id || ''),
      name: m.name || '',
      input_price: m.input_price ?? '',
      output_price: m.output_price ?? '',
      cache_read_price: m.cache_read_price ?? '',
      cache_write_price: m.cache_write_price ?? '',
    });
    setShowForm(true);
  };

  const handleSubmit = async () => {
    if (!form.channel_id || !form.name) return;
    setSubmitting(true);
    const data = {
      channel_id: Number(form.channel_id),
      name: form.name,
      ...(form.input_price !== '' && { input_price: Number(form.input_price) }),
      ...(form.output_price !== '' && { output_price: Number(form.output_price) }),
      ...(form.cache_read_price !== '' && { cache_read_price: Number(form.cache_read_price) }),
      ...(form.cache_write_price !== '' && { cache_write_price: Number(form.cache_write_price) }),
    };
    try {
      if (editId) {
        await updateModel(editId, data);
      } else {
        await createModel(data);
      }
      setShowForm(false);
      resetForm();
      await fetchAll();
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <Typography type="h2">模型管理</Typography>
        <Button
          variant="primary"
          size="md"
          onPress={() => {
            resetForm();
            setShowForm(true);
          }}
        >
          新增模型
        </Button>
      </div>

      {loading ? (
        <div className="flex justify-center py-12">
          <Spinner />
        </div>
      ) : models.length === 0 ? (
        <div className="flex justify-center py-16 text-muted">
          <Typography>暂无模型</Typography>
        </div>
      ) : (
        <div className="rounded-xl border border-separator overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-separator bg-accent/5">
                <th className="text-left px-4 py-3 font-medium">名称</th>
                <th className="text-left px-4 py-3 font-medium">渠道</th>
                <th className="text-right px-4 py-3 font-medium">输入价格</th>
                <th className="text-right px-4 py-3 font-medium">输出价格</th>
                <th className="text-center px-4 py-3 font-medium">状态</th>
                <th className="text-right px-4 py-3 font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {models.map((m) => (
                <tr key={m.id} className="border-b border-separator/50 hover:bg-accent/5">
                  <td className="px-4 py-3 font-medium">{m.name}</td>
                  <td className="px-4 py-3 text-muted">{channelMap[m.channel_id] || m.channel_id}</td>
                  <td className="px-4 py-3 text-right font-mono">${m.input_price ?? '—'}</td>
                  <td className="px-4 py-3 text-right font-mono">${m.output_price ?? '—'}</td>
                  <td className="px-4 py-3 text-center">
                    <Button
                      size="sm"
                      variant={m.enabled ? 'primary' : 'ghost'}
                      isLoading={togglingId === m.id}
                      onPress={() => handleToggle(m)}
                    >
                      {m.enabled ? '启用' : '停用'}
                    </Button>
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex justify-end gap-1">
                      <Button size="sm" variant="ghost" onPress={() => openEdit(m)}>
                        编辑
                      </Button>
                      <Button size="sm" variant="ghost" className="text-red-500" onPress={() => handleDelete(m)}>
                        <X className="size-3.5" />
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* 新增/编辑弹窗 */}
      {showForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={() => setShowForm(false)}>
          <div className="bg-background rounded-xl shadow-xl p-6 w-full max-w-md space-y-4" onClick={(e) => e.stopPropagation()}>
            <Typography type="h3">{editId ? '编辑模型' : '新增模型'}</Typography>

            <select
              className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm"
              value={form.channel_id}
              onChange={(e) => setForm((f) => ({ ...f, channel_id: e.target.value }))}
            >
              <option value="">选择渠道</option>
              {channels.map((c) => (
                <option key={c.id} value={String(c.id)}>{c.name}</option>
              ))}
            </select>

            <input
              className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm"
              placeholder="模型名称"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />

            <div className="grid grid-cols-2 gap-3">
              <input className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm" placeholder="输入价格 ($/M)" type="number" value={form.input_price} onChange={(e) => setForm((f) => ({ ...f, input_price: e.target.value }))} />
              <input className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm" placeholder="输出价格 ($/M)" type="number" value={form.output_price} onChange={(e) => setForm((f) => ({ ...f, output_price: e.target.value }))} />
              <input className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm" placeholder="缓存读 ($/M)" type="number" value={form.cache_read_price} onChange={(e) => setForm((f) => ({ ...f, cache_read_price: e.target.value }))} />
              <input className="w-full rounded-lg border border-separator bg-background px-3 py-2 text-sm" placeholder="缓存写 ($/M)" type="number" value={form.cache_write_price} onChange={(e) => setForm((f) => ({ ...f, cache_write_price: e.target.value }))} />
            </div>

            <div className="flex justify-end gap-2">
              <Button variant="ghost" onPress={() => setShowForm(false)}>取消</Button>
              <Button variant="primary" isLoading={submitting} onPress={handleSubmit}>
                {editId ? '保存' : '创建'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}