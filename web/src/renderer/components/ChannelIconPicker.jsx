import { useEffect, useRef, useState } from 'react';
import { ChevronDown, Upload } from 'lucide-react';
import ProviderIcon from './ProviderIcon';
import { providerIcons } from './providerIcons';
import { filterIcons, ICON_ACCEPT, normalizeIconFile } from '../utils/channelIcon';

export default function ChannelIconPicker({ draft, onChange, onProcessingChange }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [processing, setProcessing] = useState(false);
  const [error, setError] = useState('');
  const fileInput = useRef(null);
  const sequence = useRef(0);
  useEffect(() => () => { sequence.current += 1; }, []);

  const select = (icon) => {
    sequence.current += 1;
    setProcessing(false); onProcessingChange(false);
    setError(''); onChange(icon); setOpen(false);
  };
  const upload = async (event) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!file) return;
    const current = ++sequence.current;
    setError(''); setProcessing(true); onProcessingChange(true);
    try {
      const icon = await normalizeIconFile(file);
      if (current === sequence.current) select(icon);
    } catch (err) {
      if (current === sequence.current) setError(err.message);
    } finally {
      if (current === sequence.current) { setProcessing(false); onProcessingChange(false); }
    }
  };
  const matches = filterIcons(providerIcons, query);
  return <div className="col-span-2 flex min-w-0 flex-col gap-2">
    <div className="flex items-center gap-3">
      <button type="button" aria-label="选择渠道图标" aria-expanded={open} onClick={() => setOpen(!open)}
        className="flex size-11 shrink-0 items-center justify-center rounded-xl border border-separator bg-surface-secondary text-accent hover:bg-default focus-visible:outline-2 focus-visible:outline-accent">
        <ProviderIcon type={draft.type} providerId={draft.provider_id} icon={draft.icon} name={draft.name} className="size-7" />
      </button>
      <div className="min-w-0 text-sm">
        <button type="button" className="flex items-center gap-1 font-medium" aria-expanded={open} onClick={() => setOpen(!open)}>
          渠道图标<ChevronDown size={14} />
        </button>
        <p className="text-xs text-muted">{processing ? '正在处理图片…' : draft.icon ? '已设置自定义图标，保存渠道后生效' : '自动跟随厂商，可手动选择或上传'}</p>
      </div>
    </div>
    {open && <div className="flex flex-col gap-3 rounded-xl border border-separator p-3" aria-label="渠道图标选择器">
      <input type="search" aria-label="搜索图标" placeholder="搜索品牌名称，例如 DeepSeek、硅基流动" value={query}
        onKeyDown={(event) => { if (event.key === 'Enter') event.preventDefault(); }}
        onChange={(event) => setQuery(event.target.value)} className="w-full rounded-lg border border-separator bg-surface px-3 py-2 text-sm" />
      <div className="flex flex-wrap gap-2">
        <button type="button" aria-pressed={!draft.icon} onClick={() => select('')}
          className={`rounded-lg border px-3 py-2 text-xs ${!draft.icon ? 'border-accent text-accent' : 'border-separator'}`}>自动跟随厂商</button>
        <button type="button" disabled={processing} onClick={() => fileInput.current?.click()}
          className="flex items-center gap-1 rounded-lg border border-separator px-3 py-2 text-xs disabled:opacity-50">
          <Upload size={14} />{processing ? '处理中…' : '上传图片'}
        </button>
      </div>
      <div className="grid grid-cols-3 gap-2 sm:grid-cols-6">
        {matches.map((entry) => <button type="button" key={entry.id} aria-label={`使用 ${entry.name} 图标`}
          aria-pressed={draft.icon === entry.id} onClick={() => select(entry.id)}
          className={`flex min-w-0 flex-col items-center gap-2 rounded-lg border p-2 text-xs hover:bg-default ${draft.icon === entry.id ? 'border-accent text-accent' : 'border-separator'}`}>
          <ProviderIcon icon={entry.id} className="size-7" /><span className="max-w-full truncate">{entry.name}</span>
        </button>)}
      </div>
      {!matches.length && <p className="text-xs text-muted">没有匹配的图标，可以上传自己的图片。</p>}
      <p className="text-xs text-muted">支持 PNG、JPEG、WebP、SVG，最大 2 MB。</p>
    </div>}
    <input ref={fileInput} type="file" accept={ICON_ACCEPT} aria-label="上传渠道图标" className="hidden" onChange={upload} />
    {error && <p role="alert" className="text-xs text-danger">{error}</p>}
  </div>;
}
