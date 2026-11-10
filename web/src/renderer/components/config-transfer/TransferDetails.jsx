import { useMemo, useState } from 'react';
import { Button, Chip, Tabs } from '@heroui/react';
import { pageOf, previewRelations, renamedItems } from './model.js';

export function Counts({ counts }) {
  return <div className="transfer-counts" aria-label="配置数量">{[['channels', '渠道'], ['models', '模型'], ['groups', '分组']].map(([key, label]) =>
    <div key={key}><strong>{counts?.[key] ?? 0}</strong><span>{label}</span></div>)}</div>;
}

export function Pager({ view, onChange, label }) {
  if (view.pages <= 1) return null;
  return <nav className="transfer-pager" aria-label={`${label}分页`}><span>共 {view.total} 项 · 第 {view.page} / {view.pages} 页</span>
    <Button size="sm" variant="tertiary" isDisabled={view.page === 1} onPress={() => onChange(view.page - 1)}>上一页</Button>
    <Button size="sm" variant="tertiary" isDisabled={view.page === view.pages} onPress={() => onChange(view.page + 1)}>下一页</Button></nav>;
}

export function RenameList({ data, completed = false }) {
  const [page, setPage] = useState(1);
  const renames = useMemo(() => renamedItems(data), [data]);
  const view = pageOf(renames, page);
  return renames.length ? <section aria-label="同名改名清单"><h3 className="transfer-section-title text-warning">{renames.length} 项同名配置{completed ? '已' : '将'}自动改名</h3>
    <div className="transfer-table-wrap"><table><thead><tr><th>类型</th><th>原名称</th><th>{completed ? '最终名称' : '导入后名称'}</th></tr></thead><tbody>
      {view.items.map((item) => <tr key={`${item.kind}-${item.ref}`}><td>{item.kind}</td><td className="text-muted">{item.original_name}</td><td>{item.name}</td></tr>)}
    </tbody></table></div><Pager view={view} onChange={setPage} label="改名清单" /></section>
    : <div className="transfer-notice transfer-notice--success"><strong>无需改名</strong><p>{completed ? '已按文件中的原名称新增配置。' : '渠道和分组名称均可直接使用，仍将全部新增。'}</p></div>;
}

function GroupMembers({ group, relations }) {
  const [page, setPage] = useState(1);
  const view = pageOf(group.items, page);
  return <div className="transfer-group"><div className="transfer-group-title"><strong>{group.name}</strong>
    <Chip size="sm" variant="soft">{group.strategy === 'manual' ? '手动指定' : '故障转移'}</Chip></div>
    {group.original_name !== group.name && <p className="transfer-detail-sub">原名称：{group.original_name}</p>}
    <p className="transfer-detail-sub">{group.strategy === 'manual' ? (group.active_item_ref ? '保留当前指定模型' : '尚未指定当前模型') : '按以下顺序尝试，保留同优先级成员的先后顺序'}</p>
    {!group.items.length && <p className="transfer-detail-sub">暂无成员，导入后可继续配置</p>}
    <ol className="transfer-members" start={(view.page - 1) * 20 + 1}>{view.items.map((item) => {
      const member = relations.member(item, group);
      return <li key={item.ref}><span className="transfer-member-position">{member.position}.</span><span className="transfer-member-copy"><span className="font-mono">{member.modelName}</span>
        <span className="text-muted">{member.channelName}</span></span>{member.active && <Chip size="sm" variant="soft">{group.strategy === 'manual' ? '当前' : '保留激活引用'}</Chip>}</li>;
    })}</ol><Pager view={view} onChange={setPage} label={`${group.name}成员`} /></div>;
}

export default function TransferDetails({ preview, disabled = false }) {
  const [tab, setTab] = useState('channels');
  const [pages, setPages] = useState({ channels: 1, models: 1, groups: 1 });
  const relations = useMemo(() => previewRelations(preview), [preview]);
  const view = pageOf(preview[tab], pages[tab]);
  return <section aria-label="全部导入配置详情"><Tabs selectedKey={tab} onSelectionChange={(key) => setTab(String(key))} className="transfer-tabs">
    <Tabs.ListContainer><Tabs.List aria-label="配置详情">{[['channels', '渠道'], ['models', '模型'], ['groups', '分组']].map(([key, label]) =>
      <Tabs.Tab key={key} id={key} isDisabled={disabled}>{label} {preview.counts[key]}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
  </Tabs>
    {tab === 'channels' ? <div className="transfer-detail-list">{view.items.map((channel) => <div className="transfer-detail-row" key={channel.ref}>
      <div><strong>{channel.name}</strong>{channel.original_name !== channel.name && <p className="transfer-detail-sub">原名称：{channel.original_name}</p>}
        <p className="transfer-detail-sub">{channel.type} · 已保存连接配置将恢复 · {channel.has_key ? '已保存上游凭据' : '上游凭据为空（保留原值）'}</p></div><span className="text-xs text-muted">{channel.model_count} 个模型</span>
    </div>)}</div> : tab === 'models' ? <div className="transfer-table-wrap"><table><thead><tr><th>模型名称</th><th>所属导入渠道</th></tr></thead><tbody>
      {view.items.map((model) => <tr key={model.ref}><td className="font-mono">{model.name}</td><td>{relations.channelName(model.channel_ref)}</td></tr>)}
    </tbody></table></div> : <div className="transfer-detail-list">{view.items.map((group) => <GroupMembers key={group.ref} group={group} relations={relations} />)}</div>}
    {!view.total && <p className="text-sm text-muted">文件中没有此类配置</p>}
    <Pager view={view} onChange={(page) => setPages((old) => ({ ...old, [tab]: page }))} label="配置详情" />
  </section>;
}

export function ResultGroups({ groups = [] }) {
  const [page, setPage] = useState(1);
  const view = pageOf(groups, page);
  if (!groups.length) return null;
  return <section><h3 className="transfer-section-title">本次新增分组的最终名称</h3><div className="transfer-detail-list">{view.items.map((group) =>
    <div className="transfer-detail-row" key={group.ref}><span>{group.name}</span></div>)}</div><Pager view={view} onChange={setPage} label="新增分组" /></section>;
}
