import { useState } from 'react';
import { Button } from '@heroui/react';
import { Search } from 'lucide-react';
import SearchField from '../../components/SearchField';

// 查询草稿留在工具栏，输入时不重新渲染分组及其拖拽成员。
export default function GroupSearch({ onSearch, onClear, disabled, pending }) {
  const [draft, setDraft] = useState('');
  const search = () => onSearch(draft.trim());
  return <>
    <SearchField label="按分组名称查询" value={draft} onChange={setDraft} placeholder="按名称查询"
      onKeyDown={(event) => { if (event.key === 'Enter') search(); }} onClear={() => { setDraft(''); onClear(); }} clearLabel="清除名称查询" />
    <Button variant="secondary" size="sm" onPress={search} isDisabled={disabled} isPending={pending}>
      <Search className="size-4" />查询
    </Button>
  </>;
}
