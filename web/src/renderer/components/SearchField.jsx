import { Search, X } from 'lucide-react';
import './search-field.css';

// 只负责外观与输入事件。何时执行查询、如何清除筛选由页面决定。
export default function SearchField({ value, onChange, onClear, onKeyDown, label, placeholder, clearLabel = '清空搜索', className = '', type = 'text' }) {
  return <label className={`search-field ${className}`}>
    <Search className="size-4 shrink-0" aria-hidden="true" />
    <input type={type} aria-label={label} placeholder={placeholder} value={value} onChange={(event) => onChange(event.target.value)} onKeyDown={onKeyDown} />
    {value && onClear && <button type="button" aria-label={clearLabel} title={clearLabel} onClick={onClear}><X className="size-3.5" aria-hidden="true" /></button>}
  </label>;
}
