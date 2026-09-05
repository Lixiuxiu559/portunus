import { useEffect, useState } from 'react';
import { RefreshCw, FolderInput, Info, Terminal, Braces } from 'lucide-react';
import { toast } from '@heroui/react';
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

// 把表单的 base/token 写回源码（正则点改，保留其余内容）
function applyConn(t, lang, baseUrl, token) {
  let out = t;
  if (lang === 'json') {
    out = out.replace(/("ANTHROPIC_BASE_URL"\s*:\s*)"[^"]*"/, `$1"${esc(baseUrl)}"`);
    if (token) out = out.replace(/("ANTHROPIC_AUTH_TOKEN"\s*:\s*)"[^"]*"/, `$1"${esc(token)}"`);
  } else {
    out = out.replace(/^(\s*base_url\s*=\s*)"[^"]*"/m, `$1"${esc(baseUrl)}"`);
    if (token) out = out.replace(/^(\s*experimental_bearer_token\s*=\s*)"[^"]*"/m, `$1"${esc(token)}"`);
  }
  return out;
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

function ClientPanel({ title, lang, fileName, path, config, modelSlots, defaultBase, icon, hint }) {
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

  useEffect(() => {
    if (hasElectron) load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSave = async () => {
    let t = applyConn(text, lang, baseUrl.trim(), token.trim());
    if (modelSlots) t = applyModels(t, models);
    setText(t);
    setSaving(true);
    const r = await config.save(t);
    setSaving(false);
    if (r && r.ok) {
      setSavedText(t);
      setSavedAt(rightNow());
      setBackupExists(!!(r.data && r.data.backupExists));
      toast.success('配置已原子写入');
      parseForm(t);
    } else {
      toast.danger((r && r.error) || '保存失败');
    }
  };

  const handleRollback = async () => {
    const r = await config.rollback();
    if (r && r.ok) {
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
    setText((prev) => applyConn(prev, lang, defaultBase, token));
    toast.success('已填写 Portunus URL');
  };

  const handleImportToken = async () => {
    try {
      const k = await getAPIKey();
      if (!k || !k.key) {
        toast.error('暂无令牌，请先到「设置」获取');
        return;
      }
      setToken(k.key);
      setText((prev) => applyConn(prev, lang, baseUrl, k.key));
      toast.success('已导入 Portunus 令牌');
    } catch {
      // toast 由 request 拦截器统一提示
    }
  };

  const handleFetchModels = async () => {
    setFetching(true);
    try {
      const gs = await listGroups();
      setModelOptions((Array.isArray(gs) ? gs : []).map((g) => ({ id: g.name, label: g.name })));
      toast.success('已获取模型列表');
    } catch {
      // toast 由 request 拦截器统一提示
    } finally {
      setFetching(false);
    }
  };

  const setSlot = (slotId, patch) => {
    setModels((m) => ({ ...m, [slotId]: { ...(m[slotId] || { base: '', onem: false, name: '' }), ...patch } }));
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
          <span className="mm-key">{slot.key}</span>
        </div>
        {slot.keyName ? (
          <input
            className="mm-input"
            type="text"
            placeholder="显示名"
            value={m.name}
            onChange={(e) => setSlot(slot.id, { name: e.target.value })}
            spellCheck={false}
          />
        ) : slot.nameDash ? (
          <input className="mm-input" type="text" value="—" disabled />
        ) : (
          <span />
        )}
        <select
          className="mm-select"
          value={m.base}
          onChange={(e) => setSlot(slot.id, { base: e.target.value, name: e.target.value })}
        >
          <option value="">— 不映射 —</option>
          {opts.map((o) => (
            <option key={o.id} value={o.id}>{o.label}</option>
          ))}
        </select>
        {slot.has1m !== false ? (
          <label className="mm-1m">
            <input type="checkbox" checked={m.onem} onChange={(e) => setSlot(slot.id, { onem: e.target.checked })} />
            <span>1M</span>
          </label>
        ) : (
          <span />
        )}
      </div>
    );
  };

  const status = dirty
    ? { cls: 'warn', label: '有未保存改动' }
    : /3061|portunus/i.test(text)
      ? { cls: 'ok', label: '已指向 Portunus' }
      : { cls: 'mute', label: '未指向 Portunus' };

  return (
    <section className="card">
      {/* 卡片头 */}
      <div className="card-head">
        <div className="brandicon">{icon}</div>
        <div>
          <div className="ttl">{title}</div>
          <div className="sub">
            <span className="mono">{fileName}</span> · {lang === 'json' ? 'JSON' : 'TOML'}
          </div>
        </div>
        <div className="spacer" />
        <span className={`chip ${status.cls}`}>
          <span className="dot" />
          {status.label}
        </span>
      </div>

      {/* 文件路径行 */}
      <div className="pathrow">
        <FolderInput className="size-3.5" />
        <span className="p">{path}</span>
        <span className={`chip ${backupExists ? 'ok' : 'mute'} bak ${backupExists ? '' : 'opacity-60'}`}>
          <span className="dot" />
          {backupExists ? '已备份 .portunus.bak' : '尚未备份'}
        </span>
      </div>

      {/* 连接 */}
      <div className="section-label">连接</div>
      <div className="form">
        <div className="field">
          <label>base_url</label>
          <div className="link-field">
            <input type="text" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} spellCheck={false} />
            <button type="button" className="btn btn-secondary" onClick={handleFillUrl}>同步 Portunus</button>
          </div>
        </div>
        <div className="field">
          <label>令牌</label>
          <div className="link-field">
            <input
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              spellCheck={false}
              autoComplete="off"
            />
            <button type="button" className="btn btn-secondary" onClick={handleImportToken}>从 Portunus 导入</button>
          </div>
        </div>
        <p className="hint">{hint}</p>
      </div>

      {/* 模型映射（仅 Claude） */}
      {modelSlots && (
        <>
          <div className="flex items-center justify-between">
            <div className="section-label">模型映射</div>
            <button type="button" className="btn btn-tertiary" onClick={handleFetchModels} disabled={fetching}>
              <RefreshCw className={fetching ? 'animate-spin' : ''} />
              获取模型
            </button>
          </div>
          <div className="models-pane">
            <div className="mm-list">
              <div className="mm-head">
                <span>角色</span>
                <span>显示名</span>
                <span>请求模型</span>
                <span>声明 1M</span>
              </div>
              {modelSlots.map(renderSlot)}
            </div>
            <p className="hint">显示名只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明。</p>
          </div>
        </>
      )}

      {/* 源码编辑器 */}
      <div className="section-label">配置文件（源码）</div>
      <CodeEditor
        lang={lang}
        fileName={fileName}
        text={text}
        backupText={backupText}
        onTextChange={setText}
        dirty={dirty}
        savedAt={savedAt}
        canRollback={backupExists}
        onSave={handleSave}
        onRollback={handleRollback}
        onReread={handleReread}
        saving={saving}
      />
    </section>
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
          写入 <span className="mono">env.ANTHROPIC_BASE_URL</span> 与 <span className="mono">env.ANTHROPIC_AUTH_TOKEN</span>，其余字段原样保留。
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
          写入 <span className="mono">[model_providers.portunus]</span> 段并激活 <span className="mono">model_provider = &quot;portunus&quot;</span>，其余 provider 原样保留。
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
          <h1 className="text-[28px] font-bold leading-[1.3]">客户端配置</h1>
          <div className="card">
            <div className="flex items-center gap-2 text-muted">
              <Info className="size-5" />
              <span className="text-sm">本功能需在 Portunus 桌面端使用（依赖本地文件访问），当前浏览器环境不可用。</span>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
        <div className="px-1 flex flex-col gap-5">
          <div>
            <h1 className="text-[28px] font-bold leading-[1.3]">客户端配置</h1>
            <p className="lead text-sm text-muted mt-1.5 max-w-[65ch]">
              把 Portunus 配成 Claude Code / Codex 的上游；下方直接编辑配置文件源码，保存即原子写入，写前自动备份。
            </p>
          </div>

          <div className="client-tabs">
            {panels.map((p) => (
              <button
                key={p.id}
                type="button"
                className={`client-tab ${client === p.id ? 'active' : ''}`}
                onClick={() => setClient(p.id)}
              >
                <span className="client-logo">{p.icon}</span>
                {p.id === 'claude' ? 'Claude Code' : 'Codex'}
              </button>
            ))}
          </div>

          {panels
            .filter((p) => p.id === client)
            .map((p) => (
              <ClientPanel key={p.id} {...p} />
            ))}
        </div>
      </div>
    </div>
  );
}