import { useEffect, useRef, useState } from 'react';
import { Button, Chip, Input, Label, TextField, Typography, toast } from '@heroui/react';
import { Eye, EyeOff, FolderInput, Info } from 'lucide-react';
import AppSelect from '../../components/AppSelect';
import CodeEditor from '../../components/CodeEditor';
import ConfirmModal from '../../components/ConfirmModal';
import { ClaudeMark } from '../../components/BrandMarks';
import { setNavBlock } from '../../utils/navGuard';
import {
  MODEL_SLOTS,
  applyClaudeBaseUrl,
  applyClaudeModels,
  applyClaudeToken,
  isPortunusBase,
  readClaudeBase,
  readClaudeModels,
  readClaudeToken,
} from '../../utils/claudeConfig';

function rightNow() {
  const d = new Date();
  const p = (n) => (n < 10 ? '0' : '') + n;
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
}

/**
 * 客户端配置（仅 Claude Code）：读取、编辑、原子保存 ~/.claude/settings.json。
 * 首次写入前由主进程备份为 .portunus.bak，可从备份回滚。
 * 非桌面端（没有 window.api.clientConfig）只展示不可用提示，不发起文件 IPC。
 */
export default function ClaudeConfigPanel({ groups, apiKey, serverUrl, onDirtyChange }) {
  const clientConfig = window.api?.clientConfig;

  const [path, setPath] = useState('~/.claude/settings.json');
  const [text, setText] = useState('');
  const [savedText, setSavedText] = useState('');
  const [savedAt, setSavedAt] = useState('');
  const [backupExists, setBackupExists] = useState(false);
  const [backupText, setBackupText] = useState(null);
  const [baseUrl, setBaseUrl] = useState('');
  const [token, setToken] = useState('');
  const [models, setModels] = useState({});
  const [showToken, setShowToken] = useState(false);
  const [saving, setSaving] = useState(false);
  const [confirmRollback, setConfirmRollback] = useState(false);
  const [confirmReread, setConfirmReread] = useState(false);
  const parseTimer = useRef(null);

  const dirty = text !== savedText;
  const modelOptions = groups.map((g) => ({ id: g.name, label: g.name }));
  const status = dirty
    ? { color: 'warning', label: '有未保存改动' }
    : isPortunusBase(baseUrl, serverUrl)
      ? { color: 'success', label: '已指向 Portunus' }
      : { color: 'default', label: '未指向 Portunus' };

  const parseForm = (value) => {
    setBaseUrl(readClaudeBase(value));
    setToken(readClaudeToken(value));
    setModels(readClaudeModels(value));
  };

  // 源码是唯一真相：写源码时取消未决的反解析，避免表单覆盖用户刚输入的内容。
  const writeText = (value) => {
    clearTimeout(parseTimer.current);
    setText(value);
  };

  const load = async () => {
    try {
      const r = await clientConfig.readClaude();
      if (r?.ok) {
        const data = r.data;
        setPath(data.path || '~/.claude/settings.json');
        setText(data.text);
        setSavedText(data.text);
        setBackupExists(Boolean(data.backupExists));
        parseForm(data.text);
        setSavedAt(rightNow());
      } else {
        toast.danger(r?.error || '读取配置失败');
      }
      const rb = await clientConfig.readBackupClaude();
      setBackupText(rb?.ok && rb.data?.exists ? rb.data.text : null);
      return Boolean(r?.ok);
    } catch (e) {
      toast.danger(e?.message || '读取配置失败');
      return false;
    }
  };

  useEffect(() => {
    if (clientConfig) load();
    return () => clearTimeout(parseTimer.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 未保存守卫：顶部导航切页由 Layout 拦截；树内切换由页面用 onDirtyChange 拦截
  useEffect(() => {
    setNavBlock('client-claude', dirty ? 'Claude Code 配置有未保存改动' : null);
    onDirtyChange?.(dirty);
    return () => {
      setNavBlock('client-claude', null);
      onDirtyChange?.(false);
    };
  }, [dirty, onDirtyChange]);

  const handleTextChange = (value) => {
    setText(value);
    clearTimeout(parseTimer.current);
    parseTimer.current = setTimeout(() => parseForm(value), 300);
  };

  const handleFillUrl = () => {
    const r = applyClaudeBaseUrl(text, serverUrl);
    if (!r.ok) {
      toast.danger(r.reason);
      return;
    }
    setBaseUrl(serverUrl);
    writeText(r.text);
    toast.success('已填写 Portunus 地址');
  };

  const handleImportToken = () => {
    if (!apiKey) {
      toast.danger('暂无 Portunus 令牌，请先在上方刷新连接或生成令牌');
      return;
    }
    const r = applyClaudeToken(text, apiKey);
    if (!r.ok) {
      toast.danger(r.reason);
      return;
    }
    setToken(apiKey);
    writeText(r.text);
    toast.success('已导入 Portunus 令牌，保存后生效');
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      const r = await clientConfig.saveClaude(text);
      if (r?.ok) {
        setSavedText(text);
        setSavedAt(rightNow());
        setBackupExists(Boolean(r.data?.backupExists));
        setPath(r.data?.path || path);
        parseForm(text);
        toast.success('配置已原子写入');
        const rb = await clientConfig.readBackupClaude();
        setBackupText(rb?.ok && rb.data?.exists ? rb.data.text : null);
      } else {
        toast.danger(`${r?.error || '保存失败'} · 可修正后重试，或点「重新读取」恢复磁盘版本`);
      }
    } catch (e) {
      toast.danger(e?.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const performRollback = async () => {
    try {
      const r = await clientConfig.rollbackClaude();
      if (r?.ok) {
        await load();
        toast.success('已从 .portunus.bak 回滚到接入前配置');
      } else {
        toast.danger(r?.error || '回滚失败');
      }
    } catch (e) {
      toast.danger(e?.message || '回滚失败');
    }
  };

  const performReread = async () => {
    if (await load()) toast.success('已从磁盘重新读取');
  };

  const setSlot = (slotId, patch) => {
    const next = { ...models, [slotId]: { ...(models[slotId] || { base: '', onem: false, name: '' }), ...patch } };
    setModels(next);
    const r = applyClaudeModels(text, next);
    if (r.ok) writeText(r.text);
    else toast.danger(r.reason);
  };

  const renderModelControls = (slot, m) => {
    // 当前映射值不在分组里（分组已删 / env 手写）时用禁用项展示悬空状态，不自动清洗用户配置。
    const dangling = m.base && !modelOptions.some((o) => o.id === m.base);
    return (
      <div className="mm-model">
        <div className="mm-select-wrap">
          <AppSelect label={`${slot.label} 请求模型`} className="mm-model-choice" hideLabel value={m.base}
            options={[{ id: '', name: '— 不映射 —' }, ...(dangling ? [{ id: m.base, name: `${m.base}（分组不存在）`, disabled: true }] : []), ...modelOptions.map((o) => ({ id: o.id, name: o.label }))]}
            onChange={(value) => setSlot(slot.id, value === '' ? { base: '', name: '' } : { base: value, name: value })} />
        </div>
        {slot.has1m !== false ? (
          <label className="mm-1m">
            <input type="checkbox" checked={m.onem} onChange={(event) => setSlot(slot.id, { onem: event.target.checked })} />
            <span>1M</span>
          </label>
        ) : (
          <span className="mm-1m mm-1m--ghost" aria-hidden="true">
            <span className="mm-1m-box" />
            <span>1M</span>
          </span>
        )}
      </div>
    );
  };

  const renderSlot = (slot) => {
    const m = models[slot.id] || { base: '', onem: false, name: '' };
    return (
      <div className="mm-row" key={slot.id}>
        <div className="mm-slot">
          <span className="mm-label">{slot.label}</span>
          <span className="mm-key" title={slot.key}>{slot.key}</span>
        </div>
        {slot.keyName ? (
          <input
            className="mm-input"
            type="text"
            placeholder="显示名"
            aria-label={`${slot.label} 显示名`}
            value={m.name}
            onChange={(event) => setSlot(slot.id, { name: event.target.value })}
            spellCheck={false}
          />
        ) : slot.nameDash ? (
          <span className="mm-na" aria-hidden="true">—</span>
        ) : (
          <span />
        )}
        {renderModelControls(slot, m)}
      </div>
    );
  };

  // solo 槽（默认兜底模型）独立于映射表：没有显示名概念，只有请求模型 + 1M
  const renderSolo = (slot) => {
    const m = models[slot.id] || { base: '', onem: false, name: '' };
    return (
      <div className="mm-solo" key={slot.id}>
        <div className="mm-slot">
          <span className="mm-label">{slot.label}</span>
          <span className="mm-key" title={slot.key}>{slot.key}</span>
        </div>
        {renderModelControls(slot, m)}
      </div>
    );
  };

  const header = (
    <div className="flex min-w-0 items-center justify-between gap-3">
      <div className="flex min-w-0 items-center gap-3">
        <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-surface-secondary text-foreground"><ClaudeMark className="size-4" /></div>
        <div className="min-w-0">
          <Typography className="font-medium">Claude Code</Typography>
          <Typography type="body-xs" className="text-muted"><span className="font-mono">settings.json</span> · JSON</Typography>
        </div>
      </div>
      {clientConfig && <Chip variant="soft" size="sm" color={status.color}>{status.label}</Chip>}
    </div>
  );
  if (!clientConfig) {
    return (
      <div className="space-y-4">
        {header}
        <div className="flex items-start gap-2.5 rounded-xl border border-separator bg-surface-secondary/40 p-4 text-sm leading-6 text-muted">
          <Info className="mt-0.5 size-4 shrink-0" />
          <span>浏览器预览无法读写本地配置文件，请在 Portunus 桌面端使用。</span>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {header}

      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <FolderInput className="size-3.5 shrink-0" aria-hidden="true" />
        <span className="min-w-0 truncate font-mono" title={path}>{path}</span>
        <Chip variant="soft" size="sm" color={backupExists ? 'success' : 'default'} className="shrink-0">{backupExists ? '已备份 .portunus.bak' : '尚未备份'}</Chip>
      </div>

      {window.api?.isDevelopment && (
        <p className="rounded-lg border border-warning/30 bg-warning/10 px-3 py-2 text-xs leading-5 text-warning">
          开发版会读写真实用户目录下的 <code>{path}</code>；首次写入前会生成 .portunus.bak。
        </p>
      )}

      <div className="service-client-fields">
        <Typography type="body-sm" className="text-muted">连接</Typography>
        <div className="flex flex-col gap-3">
          <div className="flex items-end gap-2">
            <TextField
              value={baseUrl}
              onChange={(value) => {
                setBaseUrl(value);
                const r = applyClaudeBaseUrl(text, value);
                if (r.ok) writeText(r.text);
              }}
              className="flex-1"
            >
              <Label>base_url</Label>
              <Input spellCheck={false} />
            </TextField>
            <Button variant="secondary" onPress={handleFillUrl} className="shrink-0">同步 Portunus</Button>
          </div>
          <div className="flex items-end gap-2">
            <TextField
              type={showToken ? 'text' : 'password'}
              value={token}
              onChange={(value) => {
                setToken(value);
                const r = applyClaudeToken(text, value);
                if (r.ok) writeText(r.text);
              }}
              autoComplete="off"
              className="flex-1"
            >
              <Label>API Key</Label>
              <Input spellCheck={false} />
            </TextField>
            <Button
              variant="secondary"
              onPress={() => setShowToken((v) => !v)}
              className="shrink-0"
              aria-label={showToken ? '隐藏令牌' : '显示令牌'}
              title={showToken ? '隐藏令牌' : '显示令牌'}
            >
              {showToken ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </Button>
            <Button variant="secondary" onPress={handleImportToken} className="shrink-0">从 Portunus 导入</Button>
          </div>
        </div>
      </div>

      <div className="flex items-center justify-between gap-2">
        <Typography type="body-sm" className="text-muted">模型映射</Typography>
        <span className="text-[11px] text-muted">来自 {groups.length} 个分组</span>
      </div>
      <div className="models-pane">
        <div className="mm-list">
          <div className="mm-head">
            <span>角色</span>
            <span>显示名</span>
            <span>请求模型</span>
          </div>
          {MODEL_SLOTS.filter((slot) => !slot.solo).map(renderSlot)}
        </div>
        {MODEL_SLOTS.filter((slot) => slot.solo).map(renderSolo)}
        <Typography type="body-sm" className="text-muted">
          显示名只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明。分组来自服务接入页，刷新连接后更新。
        </Typography>
      </div>

      <Typography type="body-sm" className="text-muted">配置文件（源码）</Typography>
      <CodeEditor
        lang="json"
        fileName="settings.json"
        text={text}
        backupText={backupText}
        onTextChange={handleTextChange}
        dirty={dirty}
        savedAt={savedAt}
        canRollback={backupExists}
        active
        onSave={handleSave}
        onRollback={() => setConfirmRollback(true)}
        onReread={dirty ? () => setConfirmReread(true) : performReread}
        saving={saving}
      />

      <ConfirmModal
        isOpen={confirmRollback}
        onOpenChange={setConfirmRollback}
        title="从备份回滚？"
        description="将用 .portunus.bak 覆盖当前的 settings.json，未保存的改动会丢失。备份文件会保留，可再次回滚。"
        confirmText="回滚"
        pendingText="正在回滚…"
        onConfirm={performRollback}
      />
      <ConfirmModal
        isOpen={confirmReread}
        onOpenChange={setConfirmReread}
        title="重新读取磁盘配置？"
        description="当前有未保存改动，重新读取会丢弃这些修改并使用磁盘上的 settings.json。"
        confirmText="丢弃并重读"
        pendingText="正在读取…"
        onConfirm={performReread}
      />
    </div>
  );
}
