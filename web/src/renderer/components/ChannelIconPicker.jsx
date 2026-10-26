import { useEffect, useRef, useState } from 'react';
import { Camera, Check, Upload } from 'lucide-react';
import { Button, Popover } from '@heroui/react';
import './channel-icon-picker.css';
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
  return <div className="channel-icon-compact">
    <Popover isOpen={open} onOpenChange={setOpen}>
      <Button type="button" variant="tertiary" className="channel-icon-preview" aria-label="选择渠道图片" isDisabled={processing}>
        <ProviderIcon type={draft.type} providerId={draft.provider_id} icon={draft.icon} name={draft.name} className="channel-icon-preview-image" />
        <span className="channel-icon-preview-overlay"><Camera className="size-3" /><span>{processing ? '处理中' : '选择图片'}</span></span>
      </Button>
      <Popover.Content placement="bottom start" className="channel-icon-popover"><Popover.Dialog aria-label="选择渠道图片" className="channel-icon-popover-dialog">
        <Popover.Heading>渠道图片</Popover.Heading>
        <p className="channel-icon-help">选择内置图标，或上传自己的图片。</p>
        <input type="search" autoFocus aria-label="搜索渠道图片" placeholder="搜索图标，例如 DeepSeek" value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') event.preventDefault(); }} className="channel-icon-search" />
        <div className="channel-icon-grid">
          {matches.map((entry) => <button type="button" key={entry.id} title={entry.name} aria-label={`使用 ${entry.name} 图标`} aria-pressed={draft.icon === entry.id} onClick={() => select(entry.id)}>
            <ProviderIcon icon={entry.id} className="size-6" />{draft.icon === entry.id && <Check className="channel-icon-selected" />}
          </button>)}
        </div>
        {!matches.length && <p className="channel-icon-help">没有匹配的图标，可以上传图片。</p>}
        <div className="channel-icon-upload-row"><Button type="button" size="sm" variant="secondary" isDisabled={processing} onPress={() => fileInput.current?.click()}><Upload className="size-3.5" />上传图片</Button><Button type="button" size="sm" variant="tertiary" onPress={() => select('')}>自动图标</Button></div>
        <p className="channel-icon-help">PNG、JPEG、WebP、SVG · 最大 2 MB</p>
      </Popover.Dialog></Popover.Content>
    </Popover>
    <input ref={fileInput} type="file" accept={ICON_ACCEPT} aria-label="上传渠道图标" className="hidden" onChange={upload} />
    {error && <p role="alert" className="channel-icon-error">{error}</p>}
  </div>;
}
