import { useEffect, useRef, useState } from 'react';
import { RefreshCw, FolderInput, Info, Terminal, Braces, Eye, EyeOff } from 'lucide-react';
import { Button, Card, Chip, Input, Label, Modal, Tabs, TextField, Typography, toast } from '@heroui/react';
import CodeEditor from '../components/CodeEditor';
import { listGroups } from '../api/group';
import { getAPIKey } from '../api/apikey';

// 是否在 Electron 桌面端（有 preload 注入 clientConfig 桥）；纯浏览器开发时不存在。
const hasElectron = typeof window !== 'undefined' && !!window.clientConfig;

// Claude Code 模型映射槽位（复刻 client-config-editor.html 的 MODEL_SLOTS）。
const MODEL_SLOTS = [
  { id: 'sonnet', label: 'Sonnet', key: 'ANTHROPIC_DEFAULT_SONNET_MODEL', keyName: 'ANTHROPIC_DEFAULT_SONNET_MODEL_NAME', has1m: true },
  { id: 'opus', label: 'Opus', key: 'ANTHROPIC_DEFAULT_OPUS_MODEL', keyName: 'ANTHROPIC_DEFAULT_OPUS_MODEL_NAME', has1m: true },
  { id: 'fable', label: 'Fable', key: 'ANTHROPIC_DEFAULT_FABLE_MODEL', keyName: 'ANTHROPIC_DEFAULT_FABLE_MODEL_NAME', has1m: true },
  { id: 'haiku', label: 'Haiku', key: 'ANTHROPIC_DEFAULT_HAIKU_MODEL', keyName: 'ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME', has1m: false },
  { id: 'subagent', label: 'Subagent', key: 'CLAUDE_CODE_SUBAGENT_MODEL', nameDash: true, has1m: true },
  { id: 'default', label: '默认兜底模型', key: 'ANTHROPIC_MODEL', has1m: true, solo: true },
];

function parseModel(val) {
  if (typeof val === 'string' && val.slice(-4) === '[1M]') return { base: val.slice(0, -4), onem: true };
  return { base: val || '', onem: false };
}

function esc(s) {
  return s.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

function rightNow() {
  const d = new Date();
  const p = (n) => (n < 10 ? '0' : '') + n;
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
}

// ─── 解析 ───
function readClaudeBase(t) {
  const m = t.match(/"ANTHROPIC_BASE_URL"\s*:\s*"([^"]*)"/);
  return m ? m[1] : '';
}
function readClaudeToken(t) {
  const m = t.match(/"ANTHROPIC_AUTH_TOKEN"\s*:\s*"([^"]*)"/);
  return m ? m[1] : '';
}
function readClaudeEnv(t) {
  try {
    const o = JSON.parse(t);
    return o && o.env && typeof o.env === 'object' ? o.env : {};
  } catch {
    return {};
  }
}
function readCodexBase(t) {
  const m = t.match(/^\s*base_url\s*=\s*"([^"]*)"/m);
  return m ? m[1] : '';
}
function readCodexToken(t) {
  const m = t.match(/^\s*experimental_bearer_token\s*=\s*"([^"]*)"/m);
  return m ? m[1] : '';
}

// 把 base_url 写回源码（正则点改，保留其余内容）
function applyBaseUrl(t, lang, baseUrl) {
  if (lang === 'json') {
    return t.replace(/("ANTHROPIC_BASE_URL"\s*:\s*)"[^"]*"/, `$1"${esc(baseUrl)}"`);
  }
  return t.replace(/^(\s*base_url\s*=\s*)"[^"]*"/m, `$1"${esc(baseUrl)}"`);
}

// 把令牌写回源码（支持显式清空）
function applyToken(t, lang, token) {
  if (lang === 'json') {
    return t.replace(/("ANTHROPIC_AUTH_TOKEN"\s*:\s*)"[^"]*"/, `$1"${esc(token)}"`);
  }
  return t.replace(/^(\s*experimental_bearer_token\s*=\s*)"[^"]*"/m, `$1"${esc(token)}"`);
}

// 把模型映射写回源码 env（JSON round-trip）
function applyModels(t, models) {
  let obj;
  try {
    obj = JSON.parse(t);
  } catch {
    return t;
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) obj = {};
  const env = obj.env && typeof obj.env === 'object' && !Array.isArray(obj.env) ? obj.env : {};
  MODEL_SLOTS.forEach((slot) => {
    const m = models[slot.id] || {};
    const val = m.base ? m.base + (m.onem ? '[1M]' : '') : '';
    if (val) env[slot.key] = val;
    else delete env[slot.key];
    if (slot.keyName) {
      if (m.name) env[slot.keyName] = m.name;
      else delete env[slot.keyName];
    }
  });
  obj.env = env;
  return JSON.stringify(obj, null, 2);
}

function ClientPanel({ title, lang, fileName, path, config, modelSlots, defaultBase, icon, hint, active = true }) {
  const [text, setText] = useState('');
  const [savedText, setSavedText] = useState('');
  const [savedAt, setSavedAt] = useState('');
  const [baseUrl, setBaseUrl] = useState(defaultBase);
  const [token, setToken] = useState('');
  const [models, setModels] = useState({});
  const [modelOptions, setModelOptions] = useState([]);
  const [backupExists, setBackupExists] = useState(false);
  const [backupText, setBackupText] = useState(null);
  const [saving, setSaving] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [showToken, setShowToken] = useState(false);
  const [confirmRollback, setConfirmRollback] = useState(false);

  // 源码 → 表单反解析的防抖计时器
  const parseTimer = useRef(null);

  const dirty = text !== savedText;

  const parseForm = (t) => {
    if (lang === 'json') {
      setBaseUrl(readClaudeBase(t));
      setToken(readClaudeToken(t));
      const env = readClaudeEnv(t);
      const next = {};
      MODEL_SLOTS.forEach((slot) => {
        const pm = parseModel(env[slot.key]);
        next[slot.id] = { base: pm.base, onem: pm.onem, name: slot.keyName ? env[slot.keyName] || '' : '' };
      });
      setModels(next);
    } else {
      setBaseUrl(readCodexBase(t));
      setToken(readCodexToken(t));
    }
  };

  // 表单 → 源码写回：立即生效并取消未决的反解析，保证源码是唯一真相
  const writeText = (updater) => {
    clearTimeout(parseTimer.current);
    setText(updater);
  };

  // 编辑器 → 表单同步：立即更新 text，停顿后把表单重解析为源码的投影
  const handleTextChange = (t) => {
    setText(t);
    clearTimeout(parseTimer.current);
    parseTimer.current = setTimeout(() => parseForm(t), 300);
  };

  const load = async () => {
    const r = await config.read();
    if (r && r.ok) {
      const { text: t } = r.data;
      setText(t);
      setSavedText(t);
      setBackupExists(r.data.backupExists);
      parseForm(t);
      setSavedAt(rightNow());
    } else {
      toast.danger((r && r.error) || '读取配置失败');
    }
    // 读取备份内容（供「备份」视图预览）
    const rb = await config.readBackup();
    if (rb && rb.ok && rb.data && rb.data.exists) {
      setBackupText(rb.data.text);
    } else {
      setBackupText(null);
    }
  };

  // 获取分组列表作为模型映射选项；返回是否成功（挂载时静默调用，失败不打扰）
  const fetchModels = async () => {
    try {
      const gs = await listGroups();
      setModelOptions((Array.isArray(gs) ? gs : []).map((g) => ({ id: g.name, label: g.name })));
      return true;
    } catch {
      return false;
    }
  };

  useEffect(() => {
    if (hasElectron) {
      load();
      if (modelSlots) fetchModels();
    }
    return () => clearTimeout(parseTimer.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSave = async () => {
    setSaving(true);
    const r = await config.save(text);
    setSaving(false);
    if (r && r.ok) {
      setSavedText(text);
      setSavedAt(rightNow());
      setBackupExists(!!(r.data && r.data.backupExists));
      toast.success('配置已原子写入');
      parseForm(text);
    } else {
      toast.danger((r && r.error) || '保存失败');
    }
  };

  const performRollback = async () => {
    const r = await config.rollback();
    if (r && r.ok) {
      setConfirmRollback(false);
      toast.success('已从 .portunus.bak 回滚到接入前配置');
      await load();
    } else {
      toast.danger((r && r.error) || '回滚失败');
    }
  };

  const handleReread = async () => {
    await load();
    toast.success('已从磁盘重新读取');
  };

  const handleFillUrl = () => {
    setBaseUrl(defaultBase);
    writeText((prev) => applyBaseUrl(prev, lang, defaultBase));
    toast.success('已填写 Portunus URL');
  };

  const handleImportToken = async () => {
    try {
      const k = await getAPIKey();
      if (!k || !k.key) {
        toast.danger('暂无令牌，请先到「设置」获取');
        return;
      }
      setToken(k.key);
      writeText((prev) => applyToken(prev, lang, k.key));
      toast.success('已导入 Portunus 令牌');
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  const handleFetchModels = async () => {
    setFetching(true);
    const ok = await fetchModels();
    if (ok) toast.success('已获取模型列表');
    setFetching(false);
  };

  const setSlot = (slotId, patch) => {
    const next = { ...models, [slotId]: { ...(models[slotId] || { base: '', onem: false, name: '' }), ...patch } };
    setModels(next);
    // 模型映射即改即写回源码，编辑器所见即所存
    if (modelSlots) writeText((prev) => applyModels(prev, next));
  };

  const renderSlot = (slot) => {
    const m = models[slot.id] || { base: '', onem: false, name: '' };
    const opts = [
      ...modelOptions,
      ...(m.base && !modelOptions.some((o) => o.id === m.base) ? [{ id: m.base, label: m.base }] : []),
    ];
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
            onChange={(e) => setSlot(slot.id, { name: e.target.value })}
            spellCheck={false}
          />
        ) : slot.nameDash ? (
          <span className="mm-na" aria-hidden="true">—</span>
        ) : (
          <span />
        )}
        <div className="mm-model">
          <div className="mm-select-wrap">
            <select
              className="mm-select"
              aria-label={`${slot.label} 请求模型`}
              value={m.base}
              onChange={(e) => {
                const v = e.target.value;
                // 仅在显示名为空时用所选模型填充，避免覆盖用户自定义显示名
                setSlot(slot.id, v === '' ? { base: '', name: '' } : { base: v, name: m.name || v });
              }}
            >
              <option value="">— 不映射 —</option>
              {opts.map((o) => (
                <option key={o.id} value={o.id}>{o.label}</option>
              ))}
            </select>
          </div>
          {slot.has1m !== false ? (
            <label className="mm-1m">
              <input type="checkbox" checked={m.onem} onChange={(e) => setSlot(slot.id, { onem: e.target.checked })} />
              <span>1M</span>
            </label>
          ) : null}
        </div>
      </div>
    );
  };

  const curBase = lang === 'json' ? readClaudeBase(text) : readCodexBase(text);
  const status = dirty
    ? { color: 'warning', label: '有未保存改动' }
    : /3061|portunus/i.test(curBase)
      ? { color: 'success', label: '已指向 Portunus' }
      : { color: 'default', label: '未指向 Portunus' };

  return (
    <Card className="gap-4 p-5">
      {/* 卡片头 */}
      <Card.Header className="flex-row items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-surface-secondary text-foreground">
            {icon}
          </div>
          <div className="min-w-0">
            <Typography className="font-medium">{title}</Typography>
            <Typography type="body-xs" className="text-muted">
              <span className="font-mono">{fileName}</span> · {lang === 'json' ? 'JSON' : 'TOML'}
            </Typography>
          </div>
        </div>
        <Chip variant="soft" size="sm" color={status.color}>{status.label}</Chip>
      </Card.Header>

      <Card.Content className="flex flex-col gap-4">
        {/* 文件路径行 */}
        <div className="flex items-center gap-2 text-xs text-muted">
          <FolderInput className="size-3.5 shrink-0" />
          <span className="truncate font-mono">{path}</span>
          <Chip variant="soft" size="sm" color={backupExists ? 'success' : 'default'} className="shrink-0">
            {backupExists ? '已备份 .portunus.bak' : '尚未备份'}
          </Chip>
        </div>

        {/* 连接 */}
        <Typography type="body-sm" className="text-muted">连接</Typography>
        <div className="flex flex-col gap-3">
          <div className="flex items-end gap-2">
            <TextField
              value={baseUrl}
              onChange={(v) => {
                setBaseUrl(v);
                writeText((prev) => applyBaseUrl(prev, lang, v));
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
              onChange={(v) => {
                setToken(v);
                writeText((prev) => applyToken(prev, lang, v));
              }}
              autoComplete="off"
              className="flex-1"
            >
              <Label>令牌</Label>
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
          <Typography type="body-sm" className="text-muted">{hint}</Typography>
        </div>

        {/* 模型映射（仅 Claude） */}
        {modelSlots && (
          <>
            <div className="flex items-center justify-between">
              <Typography type="body-sm" className="text-muted">模型映射</Typography>
              <Button variant="tertiary" size="sm" onPress={handleFetchModels} isDisabled={fetching}>
                <RefreshCw className={`size-4 ${fetching ? 'animate-spin' : ''}`} />
                获取模型
              </Button>
            </div>
            <div className="models-pane">
              <div className="mm-list">
                <div className="mm-head">
                  <span>角色</span>
                  <span>显示名</span>
                  <span>请求模型</span>
                </div>
                {modelSlots.map(renderSlot)}
              </div>
              <Typography type="body-sm" className="text-muted">
                显示名只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明。
              </Typography>
            </div>
          </>
        )}

        {/* 源码编辑器 */}
        <Typography type="body-sm" className="text-muted">配置文件（源码）</Typography>
        <CodeEditor
          lang={lang}
          fileName={fileName}
          text={text}
          backupText={backupText}
          onTextChange={handleTextChange}
          dirty={dirty}
          savedAt={savedAt}
          canRollback={backupExists}
          active={active}
          onSave={handleSave}
          onRollback={() => setConfirmRollback(true)}
          onReread={handleReread}
          saving={saving}
        />
      </Card.Content>

      {/* 回滚二次确认 */}
      <Modal.Backdrop isOpen={confirmRollback} onOpenChange={setConfirmRollback}>
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.CloseTrigger />
            <Modal.Header>
              <Modal.Heading>从备份回滚</Modal.Heading>
            </Modal.Header>
            <Modal.Body>
              <Typography color="muted">
                {dirty ? '当前有未保存改动，回滚后将一并丢弃。' : ''}
                确定把配置恢复为接入 Portunus 之前的备份（.portunus.bak）吗？
              </Typography>
            </Modal.Body>
            <Modal.Footer>
              <Button slot="close" variant="secondary">
                取消
              </Button>
              <Button variant="danger" onPress={performRollback}>
                回滚
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Card>
  );
}

export default function Clients() {
  const [client, setClient] = useState('claude');
  const serverUrl = window.api?.getServerUrl?.() || 'http://localhost:3061';

  const panels = [
    {
      id: 'claude',
      title: 'Claude Code CLI',
      fileName: 'settings.json',
      lang: 'json',
      path: '~/.claude/settings.json',
      modelSlots: MODEL_SLOTS,
      defaultBase: serverUrl,
      icon: <Terminal className="size-4" />,
      hint: (
        <>
          写入 <span className="font-mono">env.ANTHROPIC_BASE_URL</span> 与 <span className="font-mono">env.ANTHROPIC_AUTH_TOKEN</span>，其余字段原样保留。
        </>
      ),
      config: hasElectron
        ? {
            read: () => window.clientConfig.readClaude(),
            readBackup: () => window.clientConfig.readBackupClaude(),
            save: (t) => window.clientConfig.saveClaude(t),
            rollback: () => window.clientConfig.rollbackClaude(),
          }
        : null,
    },
    {
      id: 'codex',
      title: 'Codex',
      fileName: 'config.toml',
      lang: 'toml',
      path: '~/.codex/config.toml',
      modelSlots: null,
      defaultBase: `${serverUrl.replace(/\/+$/, '')}/v1`,
      icon: <Braces className="size-4" />,
      hint: (
        <>
          写入 <span className="font-mono">[model_providers.portunus]</span> 段并激活 <span className="font-mono">model_provider = &quot;portunus&quot;</span>，其余 provider 原样保留。
        </>
      ),
      config: hasElectron
        ? {
            read: () => window.clientConfig.readCodex(),
            readBackup: () => window.clientConfig.readBackupCodex(),
            save: (t) => window.clientConfig.saveCodex(t),
            rollback: () => window.clientConfig.rollbackCodex(),
          }
        : null,
    },
  ];

  if (!hasElectron) {
    return (
      <div className="flex flex-col flex-1 min-h-0">
        <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar px-1 space-y-4">
          <Typography type="h2">客户端配置</Typography>
          <Card className="gap-4 p-5">
            <Card.Content className="flex items-center gap-2 text-muted">
              <Info className="size-5 shrink-0" />
              <Typography type="body" className="text-sm">
                本功能需在 Portunus 桌面端使用（依赖本地文件访问），当前浏览器环境不可用。
              </Typography>
            </Card.Content>
          </Card>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
        <div className="px-1 flex flex-col gap-5">
          <div>
            <Typography type="h2">客户端配置</Typography>
            <Typography type="body" className="text-muted mt-1.5 max-w-[65ch]">
              把 Portunus 配成 Claude Code / Codex 的上游；下方直接编辑配置文件源码，保存即原子写入，写前自动备份。
            </Typography>
          </div>

          <Tabs selectedKey={client} onSelectionChange={setClient} className="w-fit">
            <Tabs.ListContainer>
              <Tabs.List aria-label="客户端">
                {panels.map((p) => (
                  <Tabs.Tab id={p.id} key={p.id} className="gap-1.5">
                    {p.icon}
                    {p.id === 'claude' ? 'Claude Code' : 'Codex'}
                    <Tabs.Indicator />
                  </Tabs.Tab>
                ))}
              </Tabs.List>
            </Tabs.ListContainer>
          </Tabs>

          {/* 双面板常驻：切换 Tab 不卸载，未保存改动得以保留 */}
          {panels.map((p) => (
            <div key={p.id} className={p.id === client ? '' : 'hidden'} aria-hidden={p.id !== client}>
              <ClientPanel {...p} active={p.id === client} />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}