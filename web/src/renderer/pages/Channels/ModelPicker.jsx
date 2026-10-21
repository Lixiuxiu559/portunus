import { useState } from 'react';
import { Button, Chip, Input, ScrollShadow } from '@heroui/react';
import { X } from 'lucide-react';

export default function ModelPicker({ state, existing = [], onDelete, existingError = '', onReload }) {
  const [search, setSearch] = useState('');
  const [manual, setManual] = useState('');
  const { candidates, selected, setSelected, pending, results, draft } = state;
  const existingNames = new Set(existing.map((model) => model.name));
  const imports = candidates.filter((name) => !existingNames.has(name));
  const add = () => { if (manual.trim()) { state.addModel(manual); setManual(''); } };
  return (
    <section className="flex flex-col gap-3 border-t border-separator pt-4" aria-label="模型选择">
      {onDelete && <>
        <div className="text-sm">已保存模型（{existing.length}）</div>
        {existingError && <div role="alert" className="text-xs text-danger">{existingError} <Button type="button" size="sm" variant="secondary" onPress={onReload}>重试加载</Button></div>}
        <ScrollShadow className="dialog-models flex flex-wrap gap-2 max-h-[140px] p-1" hideScrollBar>
          {existing.map((model) => <Chip key={model.id} variant="soft" size="sm"><span className="flex items-center gap-1">
            <span className="dialog-model-name" title={model.name}>{model.name}</span>
            <button type="button" onClick={() => onDelete(model)} className="rounded-full p-0.5 hover:bg-danger/20 cursor-pointer" aria-label={`删除模型 ${model.name}`}><X className="size-3" /></button>
          </span></Chip>)}
        </ScrollShadow>
        <p className="text-xs text-muted">取消下方待导入项不会删除已保存模型；删除使用上方独立操作。</p>
      </>}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm">待导入模型 · 已选 {selected.filter((name) => !existingNames.has(name)).length}</span>
        <Button type="button" size="sm" variant="secondary" isPending={pending.preview}
          isDisabled={!state.canConnect || draft.connection_options.model_discovery === 'manual'} onPress={state.fetchModels}>获取模型</Button>
      </div>
      <p className="text-xs text-muted">获取只生成候选列表，不创建模型，也不验证生成权限。保存时仅导入勾选项。</p>
      {results.preview?.error && <p role="alert" className="text-xs text-danger">获取失败：{results.preview.error}。可手动添加模型后保存。</p>}
      {results.preview?.models && <p role="status" className="text-xs text-muted">已获取 {results.preview.models.length} 个模型；调用权限需单独验证。</p>}
      <div className="flex items-center gap-2">
        <Input aria-label="手动添加模型名称" placeholder="手动填写上游模型名" value={manual} onChange={(event) => setManual(event.target.value)}
          onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); add(); } }} className="min-w-0 flex-1" />
        <Button type="button" size="sm" variant="secondary" isDisabled={!manual.trim()} onPress={add}>添加</Button>
      </div>
      {imports.length > 0 ? <>
        <div className="flex flex-wrap items-center gap-2">
          <Input aria-label="搜索待导入模型" placeholder="搜索模型…" value={search} onChange={(event) => setSearch(event.target.value)} className="min-w-0 flex-1" />
          <Button type="button" size="sm" variant="secondary" onPress={() => setSelected(imports)}>全选</Button>
          <Button type="button" size="sm" variant="secondary" onPress={() => setSelected([])}>清空选择</Button>
        </div>
        <ScrollShadow className="flex flex-col gap-2 max-h-[190px] p-1" hideScrollBar>
          {imports.filter((name) => name.toLowerCase().includes(search.toLowerCase())).map((name) => <label key={name} className="flex items-center gap-2 text-sm min-w-0">
            <input type="checkbox" checked={selected.includes(name)} onChange={(event) => setSelected((old) => event.target.checked ? [...old, name] : old.filter((item) => item !== name))} />
            <code className="truncate" title={name}>{name}</code>
          </label>)}
        </ScrollShadow>
      </> : <p className="text-xs text-muted">暂无待导入模型，可获取或手动添加。空渠道也可以保存。</p>}
    </section>
  );
}
