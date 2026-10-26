import { useState } from 'react';
import { Button, Chip, Input, ScrollShadow } from '@heroui/react';
import { X } from 'lucide-react';
import FieldHelp from './FieldHelp';

export default function ModelPicker({ state, existing = [], onDelete, existingError = '', onReload }) {
  const [search, setSearch] = useState('');
  const [manual, setManual] = useState('');
  const { candidates, selected, setSelected, pending, results, draft } = state;
  const existingNames = new Set(existing.map((model) => model.name));
  const imports = candidates.filter((name) => !existingNames.has(name));
  const selectedCount = selected.filter((name) => !existingNames.has(name)).length;
  const add = () => { if (manual.trim()) { state.addModel(manual); setManual(''); } };
  return <section className="channel-form-block" aria-label="模型选择">
    <header className="channel-block-heading"><h3>模型</h3><span className="channel-block-count">待导入 {selectedCount}</span><FieldHelp title="模型导入">
      <p>获取模型只读取候选列表，不创建模型，也不验证生成权限。保存时仅导入勾选的模型；可以先保存空渠道。</p>
      <p>不支持模型列表接口时，可以手动填写服务商提供的模型名称。</p>
      {onDelete && <p>取消待导入项不会删除已保存模型。已保存模型旁的删除按钮是独立操作，会移除其分组关联。</p>}
    </FieldHelp></header>
    <div className="channel-block-content">
      {onDelete && <>
        <div className="channel-model-subheading"><span>已保存模型</span><span>{existing.length}</span></div>
        {existingError && <div role="alert" className="channel-form-error">{existingError}<Button type="button" size="sm" variant="secondary" onPress={onReload}>重试加载</Button></div>}
        <ScrollShadow className="dialog-models flex flex-wrap gap-2 max-h-[140px] p-1" hideScrollBar>
          {existing.map((model) => <Chip key={model.id} variant="soft" size="sm"><span className="flex items-center gap-1"><span className="dialog-model-name" title={model.name}>{model.name}</span><button type="button" onClick={() => onDelete(model)} className="rounded-full p-0.5 hover:bg-danger/20 cursor-pointer" aria-label={`删除模型 ${model.name}`}><X className="size-3" /></button></span></Chip>)}
        </ScrollShadow>
      </>}
      <div className="channel-model-subheading"><span>待导入模型</span><Button type="button" size="sm" variant="secondary" isPending={pending.preview} isDisabled={!state.canConnect || draft.connection_options.model_discovery === 'manual'} onPress={state.fetchModels}>获取模型</Button></div>
      {results.preview?.error && <p role="alert" className="channel-form-error">获取失败：{results.preview.error}</p>}
      {results.preview?.models && <p role="status" className="channel-form-result">已获取 {results.preview.models.length} 个模型</p>}
      <div className="channel-inline-actions"><Input aria-label="手动添加模型名称" placeholder="输入上游模型名称" value={manual} onChange={(event) => setManual(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); add(); } }} /><Button type="button" size="sm" variant="secondary" isDisabled={!manual.trim()} onPress={add}>添加</Button></div>
      {imports.length > 0 ? <>
        <div className="channel-inline-actions"><Input aria-label="搜索待导入模型" placeholder="搜索模型" value={search} onChange={(event) => setSearch(event.target.value)} /><Button type="button" size="sm" variant="tertiary" onPress={() => setSelected(imports)}>全选</Button><Button type="button" size="sm" variant="tertiary" onPress={() => setSelected([])}>清空</Button></div>
        <ScrollShadow className="flex flex-col gap-2 max-h-[220px] p-1" hideScrollBar>{imports.filter((name) => name.toLowerCase().includes(search.toLowerCase())).map((name) => <label key={name} className="flex items-center gap-2 text-sm min-w-0"><input type="checkbox" checked={selected.includes(name)} onChange={(event) => setSelected((old) => event.target.checked ? [...old, name] : old.filter((item) => item !== name))} /><code className="truncate" title={name}>{name}</code></label>)}</ScrollShadow>
      </> : <div className="channel-model-empty">暂无待导入模型</div>}
    </div>
  </section>;
}
