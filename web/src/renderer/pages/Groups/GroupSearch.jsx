import { useState } from 'react';
import { Button, Input, TextField } from '@heroui/react';
import { Search, X } from 'lucide-react';
import IconButton from '../../components/IconButton';

// 查询草稿留在工具栏，输入时不重新渲染分组及其拖拽成员。
export default function GroupSearch({ onSearch, onClear, disabled, pending }) {
  const [draft, setDraft] = useState('');
  const search = () => onSearch(draft.trim());
  return <>
    <TextField size="sm" aria-label="按分组名称查询" value={draft} onChange={setDraft}
      placeholder="按名称查询" className="w-56"
      onKeyDown={(event) => { if (event.key === 'Enter') search(); }}>
      <Input />
    </TextField>
    {draft && <IconButton size="sm" label="清除名称查询" onClick={() => { setDraft(''); onClear(); }}><X className="size-3.5" /></IconButton>}
    <Button variant="secondary" size="sm" onPress={search} isDisabled={disabled} isPending={pending}>
      <Search className="size-4" />查询
    </Button>
  </>;
}
