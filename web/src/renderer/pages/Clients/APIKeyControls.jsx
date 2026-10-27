import { useState } from 'react';
import { Button, Modal, TextField, Input, Label, Dropdown, Select, ListBox } from '@heroui/react';
import ConfirmModal from '../../components/ConfirmModal';
import { Eye, EyeOff, MoreHorizontal, Plus, KeyRound, ArrowUpRight, Pencil, RotateCcw, Ban, Trash2, Play } from 'lucide-react';
import { Link } from 'react-router-dom';
import SearchField from '../../components/SearchField';
import CopyButton from '../../components/CopyButton';
import { formatDate } from '../../utils/dateTime';

function NameForm({ target, onCreate, onRename, onClose }) {
  const [name, setName] = useState(target?.name || '');
  const [saving, setSaving] = useState(false);
  const submit = async (event) => {
    event.preventDefault();
    if (!name.trim() || saving) return;
    setSaving(true);
    try {
      if (target) await onRename(target.id, name.trim());
      else await onCreate(name.trim());
      onClose();
    } catch { /* 请求层显示错误，保留名称供重试。 */ }
    finally { setSaving(false); }
  };
  return <form onSubmit={submit}>
    <Modal.Body>
      <TextField name="api-key-name" value={name} onChange={setName} isRequired isDisabled={saving}>
        <Label>名称</Label>
        <Input autoFocus maxLength={80} placeholder="例如 Claude Code、Codex" />
      </TextField>
      <p className="mt-3 text-xs text-muted">为每个工具使用独立的 API Key，便于按名称查看模型与 Token 用量。</p>
    </Modal.Body>
    <Modal.Footer>
      <Button slot="close" variant="secondary" isDisabled={saving}>取消</Button>
      <Button type="submit" variant="primary" isPending={saving} isDisabled={!name.trim()}>{target ? '保存名称' : '创建 API Key'}</Button>
    </Modal.Footer>
  </form>;
}

export default function APIKeyControls({ apiKeys, busy, onCreate, onRename, onReset, onStatus, onDelete, onConnect }) {
  const [editor, setEditor] = useState(null);
  const [action, setAction] = useState(null);
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('all');
  const [visibleKeys, setVisibleKeys] = useState({});
  const managedKeys = apiKeys.filter((key) => !key.deleted_at);
  const rows = managedKeys.filter((key) => (!search.trim() || key.name.toLowerCase().includes(search.trim().toLowerCase()))
    && (status === 'all' || key.revoked === (status === 'revoked')));
  const activeCount = managedKeys.filter((key) => !key.revoked).length;
  const filteredEmpty = managedKeys.length > 0 || Boolean(search.trim()) || status !== 'all';
  return <section className="key-manager" aria-label="API Key 管理" aria-busy={busy}>
    <div className="key-manager-heading">
      <div><h2>API Key <span className="key-count">{managedKeys.length}</span></h2><p>为每个工具创建独立密钥，分别查看模型调用与 Token 用量。</p></div>
      <Button variant="primary" isDisabled={busy} onPress={() => setEditor({ target: null })}><Plus className="size-4" />新建 API Key</Button>
    </div>
    <div className="key-manager-filters">
      <SearchField className="key-search" label="搜索 API Key 名称" placeholder="搜索名称…" value={search} onChange={setSearch} onClear={() => setSearch('')} clearLabel="清空 API Key 搜索" />
      <Select aria-label="API Key 状态" className="key-status-filter" selectedKey={status} onSelectionChange={(key) => { if (key !== null) setStatus(String(key)); }}>
        <Select.Trigger aria-label="API Key 状态" aria-labelledby={null} className="key-status-trigger"><Select.Value /><Select.Indicator /></Select.Trigger>
        <Select.Popover><ListBox aria-label="API Key 状态">
          <ListBox.Item id="active" textValue="有效密钥">有效密钥<ListBox.ItemIndicator /></ListBox.Item>
          <ListBox.Item id="all" textValue="全部状态">全部状态<ListBox.ItemIndicator /></ListBox.Item>
          <ListBox.Item id="revoked" textValue="已禁用">已禁用<ListBox.ItemIndicator /></ListBox.Item>
        </ListBox></Select.Popover>
      </Select>
      <span className="key-filter-note">{activeCount} 把有效密钥</span>
    </div>
    <div className="key-table-scroll">
      <table className="key-table" aria-label="API Key 列表">
        <thead><tr><th>名称</th><th>状态</th><th>密钥</th><th>创建时间</th><th className="key-actions-heading">操作</th></tr></thead>
        <tbody>{rows.map((key) => <tr key={key.id}>
          <td><div className="key-name-cell"><span className="key-name-icon"><KeyRound className="size-4" /></span><div><strong title={key.name}>{key.name}</strong><span>#{key.id}</span></div></div></td>
          <td><span className={`key-status-badge${key.revoked ? ' is-revoked' : ''}`}><i />{key.revoked ? '已禁用' : '有效'}</span></td>
          <td><div className="key-secret-cell"><code>{visibleKeys[key.id] && !key.revoked ? key.key : `sk-••••••••${(key.key || '').slice(-4)}`}</code>{!key.revoked && <>
            <Button isIconOnly size="sm" variant="tertiary" aria-label={`${visibleKeys[key.id] ? '隐藏' : '显示'} ${key.name} 密钥`} onPress={() => setVisibleKeys((current) => ({ ...current, [key.id]: !current[key.id] }))}>{visibleKeys[key.id] ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}</Button>
            <CopyButton value={key.key} label={`复制 ${key.name} 密钥`} />
          </>}</div></td>
          <td><time className="key-created" dateTime={key.created_at}>{formatDate(key.created_at)}</time></td>
          <td><div className="key-row-actions">
            <Link className="key-usage-link" to={`/logs?api_key_id=${key.id}`}>用量<ArrowUpRight className="size-3.5" /></Link>
            {!key.revoked && <Button size="sm" variant="secondary" isDisabled={busy} onPress={() => onConnect(key.id)}>接入</Button>}
            <Dropdown><Dropdown.Trigger className="key-more-button" aria-label={`${key.name} 更多操作`} isDisabled={busy}><MoreHorizontal className="size-4" /></Dropdown.Trigger>
              <Dropdown.Popover><Dropdown.Menu aria-label={`${key.name} 操作`} onAction={(id) => {
                if (id === 'rename') setEditor({ target: key });
                else setAction({ type: id, key });
              }}>
                {!key.revoked && <Dropdown.Item id="rename" textValue="重命名"><Pencil className="size-3.5" />重命名</Dropdown.Item>}
                {!key.revoked && <Dropdown.Item id="reset" textValue="重置密钥"><RotateCcw className="size-3.5" />重置密钥</Dropdown.Item>}
                {key.revoked
                  ? <Dropdown.Item id="enable" textValue="启用密钥"><Play className="size-3.5" />启用密钥</Dropdown.Item>
                  : <Dropdown.Item id="disable" textValue="禁用密钥"><Ban className="size-3.5" />禁用密钥</Dropdown.Item>}
                <Dropdown.Item id="delete" textValue="删除密钥" className="text-danger"><Trash2 className="size-3.5" />删除密钥</Dropdown.Item>
              </Dropdown.Menu></Dropdown.Popover>
            </Dropdown>
          </div></td>
        </tr>)}</tbody>
      </table>
      {!rows.length && <div className="key-table-empty"><KeyRound className="size-7" /><p>{filteredEmpty ? '没有符合条件的 API Key' : '创建第一把 API Key'}</p><span>{filteredEmpty ? '试试其他名称或状态；已禁用的 Key 可以重新启用。' : '例如 Claude Code、Codex，便于区分每个工具的用量。'}</span></div>}
    </div>
    <div className="key-table-footer"><span>共 {rows.length} 把 API Key</span><span>禁用可恢复；删除后历史调用记录和用量仍保留</span></div>
    <Modal.Backdrop isOpen={editor !== null} onOpenChange={(open) => { if (!open) setEditor(null); }}>
      <Modal.Container size="sm"><Modal.Dialog>
        <Modal.CloseTrigger />
        <Modal.Header><Modal.Heading>{editor?.target ? '重命名 API Key' : '新建 API Key'}</Modal.Heading></Modal.Header>
        {editor && <NameForm target={editor.target} onCreate={onCreate} onRename={onRename} onClose={() => setEditor(null)} />}
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop>
    <ConfirmModal
      isOpen={action !== null}
      onOpenChange={(open) => { if (!open) setAction(null); }}
      title={{ disable: '禁用 API Key', enable: '启用 API Key', delete: '删除 API Key', reset: '重置 API Key' }[action?.type] || 'API Key 操作'}
      description={action ? {
        disable: `禁用「${action.key.name}」后，使用它的工具将无法继续调用。可在列表中重新启用，原密钥和历史用量保留。`,
        enable: `启用「${action.key.name}」后，使用原密钥的工具可恢复调用。`,
        delete: `删除「${action.key.name}」后，它将从管理列表移除并无法继续调用。历史调用时的名称、记录和用量保留，删除后不可在此恢复。`,
        reset: `重置「${action.key.name}」后，旧密钥立即失效，使用它的工具需更新配置。名称和历史用量保留。`,
      }[action.type] : ''}
      confirmText={{ disable: '确认禁用', enable: '确认启用', delete: '确认删除', reset: '确认重置' }[action?.type] || '确认'}
      onConfirm={async () => {
        setVisibleKeys({});
        if (action.type === 'delete') return onDelete(action.key.id);
        if (action.type === 'reset') return onReset(action.key.id);
        return onStatus(action.key.id, action.type === 'disable');
      }}
    />
  </section>;
}
