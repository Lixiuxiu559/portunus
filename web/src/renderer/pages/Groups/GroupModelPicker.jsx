import { useEffect, useId, useRef, useState } from 'react';
import { Button, Input, Label, ListBox, Select, Spinner, TextField } from '@heroui/react';
import { listModels } from '../../api';
import { createModelPageLoader } from '../../utils/groupModelSelection';

const PAGE_SIZE = 20;

/** 创建和添加成员共用服务端目录；已选项由父组件保留，不依赖当前页。 */
export default function GroupModelPicker({ channels, selected, onSelect, multiple = false, excludedIds = [], enabled, disabled = false }) {
  const [channelId, setChannelId] = useState('all');
  const [query, setQuery] = useState('');
  const [page, setPage] = useState(1);
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState({ key: '', models: [], total: 0, status: 'loading' });
  const loader = useRef(null);
  if (!loader.current) loader.current = createModelPageLoader(listModels);
  const inputName = useId();
  const key = JSON.stringify([channelId, query.trim(), page, retry, enabled]);

  useEffect(() => {
    const current = loader.current;
    current.invalidate();
    if (!enabled) return undefined;
    setState({ key, models: [], total: 0, status: 'loading' });
    const timer = setTimeout(async () => {
      try {
        const result = await current.load({
          ...(channelId !== 'all' ? { channel_id: Number(channelId) } : {}),
          ...(query.trim() ? { name: query.trim() } : {}),
          page, page_size: PAGE_SIZE,
        });
        if (result) setState({ key, ...result, status: 'ready' });
      } catch (error) {
        setState({ key, models: [], total: 0, status: 'error', error: error.message });
      }
    }, query.trim() ? 250 : 0);
    return () => { clearTimeout(timer); current.invalidate(); };
  }, [key, enabled, channelId, query, page]);

  const loading = state.key !== key || state.status === 'loading';
  const excluded = new Set(excludedIds.map(Number));
  const visible = loading ? [] : state.models.filter((model) => !excluded.has(Number(model.id)));
  const names = Object.fromEntries(channels.map((channel) => [channel.id, channel.name]));
  const selectedIds = new Set(selected.map((model) => Number(model.id)));
  const chooseChannel = (value) => { setChannelId(value || 'all'); setPage(1); };

  return <section className="group-model-picker" aria-label="选择上游模型">
    <div className="group-picker-fields">
      <Select aria-label="筛选渠道" selectedKey={channelId} onSelectionChange={chooseChannel} isDisabled={disabled}>
        <Label>渠道</Label>
        <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
        <Select.Popover className="dialog-options"><ListBox>
          <ListBox.Item id="all" textValue="全部渠道">全部渠道<ListBox.ItemIndicator /></ListBox.Item>
          {channels.map((channel) => <ListBox.Item key={channel.id} id={String(channel.id)} textValue={channel.name}>{channel.name}<ListBox.ItemIndicator /></ListBox.Item>)}
        </ListBox></Select.Popover>
      </Select>
      <TextField aria-label="搜索模型" value={query} onChange={(value) => { setQuery(value); setPage(1); }} isDisabled={disabled}>
        <Label>搜索模型</Label><Input placeholder="按名称搜索全部模型…" autoComplete="off" />
      </TextField>
    </div>
    <div className="group-picker-results" aria-busy={loading}>
      {loading ? <div className="group-picker-message" role="status"><Spinner size="sm" />加载模型…</div>
        : state.status === 'error' ? <div className="group-picker-message" role="alert"><span>模型加载失败，请重试</span><Button type="button" size="sm" variant="secondary" onPress={() => setRetry((value) => value + 1)}>重试模型查询</Button></div>
          : visible.length === 0 ? <div className="group-picker-message">{state.total === 0 ? '无匹配模型' : '本页模型均已在分组中，可翻页或搜索'}</div>
            : visible.map((model) => <label className={`group-picker-option ${selectedIds.has(Number(model.id)) ? 'group-picker-option--selected' : ''}`} key={model.id}>
              <input type={multiple ? 'checkbox' : 'radio'} name={inputName} checked={selectedIds.has(Number(model.id))} disabled={disabled} onChange={() => onSelect(model)} aria-label={`选择 ${model.name}（${names[model.channel_id] || `渠道 #${model.channel_id}`}）`} />
              <span className="group-picker-identity"><span className="font-mono">{model.name}</span><span className="text-xs text-muted">{names[model.channel_id] || `渠道 #${model.channel_id}`}</span></span>
            </label>)}
    </div>
    <div className="group-picker-pagination">
      <span className="text-xs text-muted" aria-live="polite">第 {page} 页{!loading && state.status === 'ready' ? ` · 共 ${state.total} 个模型` : ''}</span>
      <div className="flex gap-2">
        <Button type="button" size="sm" variant="secondary" aria-label="模型上一页" isDisabled={disabled || loading || page <= 1} onPress={() => setPage((value) => value - 1)}>上一页</Button>
        <Button type="button" size="sm" variant="secondary" aria-label="模型下一页" isDisabled={disabled || loading || state.status !== 'ready' || page * PAGE_SIZE >= state.total} onPress={() => setPage((value) => value + 1)}>下一页</Button>
      </div>
    </div>
  </section>;
}
