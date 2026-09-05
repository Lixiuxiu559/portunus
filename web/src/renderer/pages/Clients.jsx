import { useEffect, useState } from 'react';
import { Typography, Button, Card, toast } from '@heroui/react';
import { RefreshCw, FolderInput, Info } from 'lucide-react';
import CodeEditor from '../components/CodeEditor';
import { listGroups } from '../api/group';
import { getAPIKey } from '../api/apikey';

// 是否在 Electron 桌面端（有 preload 注入 clientConfig 桥）；纯浏览器开发时不存在。
const hasElectron = typeof window !== 'undefined' && !!window.clientConfig;

// Claude Code 模型映射槽位（复刻 client-config-editor.html 的 MODEL_SLOTS）。
// key = env 键；keyName = 显示名键（_NAME）；has1m:false 不渲染「声明 1M」；
// nameDash = 显示名列放禁用「—」；solo = 独立成行（默认兜底模型）。
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

function escJson(s) {
  return s.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

function escToml(s) {
  return s.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

function rightNow() {
  const d = new Date();
  const p = (n) => (n < 10 ? '0' : '') + n;
  return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
}

// ─── 单个客户端的连接字段（base/token/模型）解析与写回 ───

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
    out = out.replace(/("ANTHROPIC_BASE_URL"\s*:\s*)"[^"]*"/, `$1"${escJson(baseUrl)}"`);
    if (token) out = out.replace(/("ANTHROPIC_AUTH_TOKEN"\s*:\s*)"[^"]*"/, `$1"${escJson(token)}"`);
  } else {
    out = out.replace(/^(\s*base_url\s*=\s*)"[^"]*"/m, `$1"${escToml(baseUrl)}"`);
    if (token) out = out.replace(/^(\s*experimental_bearer_token\s*=\s*)"[^"]*"/m, `$1"${escToml(token)}"`);
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

/**
 * 单个客户端面板：连接表单 + （Claude）模型映射表 + 源码编辑器。
 * config 传入 window.clientConfig 上的一组 read/save/rollback/路径方法。
 */
function ClientPanel({ id, title, sub, lang, fileName, path, config, modelSlots, defaultBase }) {
  const [text, setText] = useState('');
  const [savedText, setSavedText] = useState('');
  const [savedAt, setSavedAt] = useState('');
  const [baseUrl, setBaseUrl] = useState(defaultBase);
  const [token, setToken] = useState('');
  const [models, setModels] = useState({});
  const [modelOptions, setModelOptions] = useState([]);
  const [backupExists, setBackupExists] = useState(false);
  const [loading, setLoading] = useState(true);
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
        next[slot.id] = {
          base: pm.base,
          onem: pm.onem,
          name: slot.keyName ? env[slot.keyName] || '' : '',
        };
      });
      setModels(next);
    } else {
      setBaseUrl(readCodexBase(t));
      setToken(readCodexToken(t));
    }
  };

  const load = async () => {
    setLoading(true);
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
    setLoading(false);
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
      setBackupExists(true);
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

  const status = dirty
    ? { cls: 'warn', label: '有未保存改动' }
    : /3061|portunus/i.test(text)
      ? { cls: 'ok', label: '已指向 Portunus' }
      : { cls: 'mute', label: '未指向 Portunus' };

  return (
    <Card className="gap-5 p-5">
      {/* 头部 */}
      <div className="flex items-center gap-3">
        <div className="flex flex-col gap-0.5">
          <Typography className="font-semibold">{title}</Typography>
          <Typography type="body-xs" className="text-muted font-mono">
            {sub}
          </Typography>
        </div>
        <span className={`cc-chip ${status.cls} ml-auto`}>
          <span className="dot" />
          {status.label}
        </span>
      </div>

      {/* 路径行 */}
      <div className="flex items-center gap-2 text-muted">
        <FolderInput className="size-4 shrink-0" />
        <Typography type="body-xs" className="font-mono break-all flex-1">{path}</Typography>
        <span className={`cc-chip mute ${backupExists ? '' : 'opacity-60'}`}>
          <span className="dot" />
          {backupExists ? '已备份 .portunus.bak' : '尚未备份'}
        </span>
      </div>

      {/* 连接表单 */}
      <div>
        <Typography type="body-xs" className="text-muted mb-2">连接</Typography>
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <label className="w-16 text-sm text-muted shrink-0">base_url</label>
            <input
              className="flex-1 h-9 px-3 rounded-lg border border-separator bg-field text-foreground text-sm font-mono"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              spellCheck={false}
            />
            <Button variant="secondary" size="sm" onPress={handleFillUrl}>同步 Portunus</Button>
          </div>
          <div className="flex items-center gap-2">
            <label className="w-16 text-sm text-muted shrink-0">令牌</label>
            <input
              className="flex-1 h-9 px-3 rounded-lg border border-separator bg-field text-foreground text-sm font-mono"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              spellCheck={false}
              autoComplete="off"
            />
            <Button variant="secondary" size="sm" onPress={handleImportToken}>从 Portunus 导入</Button>
          </div>
        </div>
      </div>

      {/* 模型映射（仅 Claude） */}
      {modelSlots && (
        <div>
          <div className="flex items-center justify-between mb-2">
            <Typography type="body-xs" className="text-muted">模型映射</Typography>
            <Button variant="tertiary" size="sm" onPress={handleFetchModels} isPending={fetching}>
              <RefreshCw className="size-3.5" />
              获取模型
            </Button>
          </div>
          <div className="cc-mm-list">
            <div className="cc-mm-head">
              <span>角色</span>
              <span>显示名</span>
              <span>请求模型</span>
              <span>声明 1M</span>
            </div>
            {modelSlots.map((slot) => {
              const m = models[slot.id] || { base: '', onem: false, name: '' };
              const rowCls = slot.solo ? 'cc-mm-row cc-mm-row--fallback' : 'cc-mm-row';
              return (
                <div className={rowCls} key={slot.id}>
                  <div className="cc-mm-slot">
                    <span className="cc-mm-label">{slot.label}</span>
                    <span className="cc-mm-key">{slot.key}</span>
                  </div>
                  {!slot.solo && (
                    slot.keyName ? (
                      <input
                        className="cc-mm-input"
                        type="text"
                        placeholder="显示名"
                        value={m.name}
                        onChange={(e) => setSlot(slot.id, { name: e.target.value })}
                        spellCheck={false}
                      />
                    ) : slot.nameDash ? (
                      <input className="cc-mm-input" type="text" value="—" disabled />
                    ) : (
                      <span />
                    )
                  )}
                  <select
                    className="cc-mm-select"
                    value={m.base}
                    onChange={(e) => setSlot(slot.id, { base: e.target.value, name: e.target.value })}
                  >
                    <option value="">— 不映射 —</option>
                    {modelOptions.map((o) => (
                      <option key={o.id} value={o.id}>{o.label}</option>
                    ))}
                    {m.base && !modelOptions.some((o) => o.id === m.base) && (
                      <option value={m.base}>{m.base}</option>
                    )}
                  </select>
                  {slot.has1m !== false ? (
                    <label className="cc-mm-1m">
                      <input
                        type="checkbox"
                        checked={m.onem}
                        onChange={(e) => setSlot(slot.id, { onem: e.target.checked })}
                      />
                      <span>1M</span>
                    </label>
                  ) : (
                    <span />
                  )}
                </div>
              );
            })}
          </div>
          <Typography type="body-xs" className="text-muted mt-2">
            显示名只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明。
          </Typography>
        </div>
      )}

      {/* 源码编辑器 */}
      <div>
        <Typography type="body-xs" className="text-muted mb-2">配置文件（源码）</Typography>
        <CodeEditor
          lang={lang}
          fileName={fileName}
          text={text}
          onTextChange={setText}
          dirty={dirty}
          savedAt={savedAt}
          canRollback={backupExists}
          onSave={handleSave}
          onRollback={handleRollback}
          onReread={handleReread}
          saving={saving}
        />
      </div>
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
      sub: '~/.claude/settings.json · JSON',
      lang: 'json',
      fileName: 'settings.json',
      path: '~/.claude/settings.json',
      modelSlots: MODEL_SLOTS,
      defaultBase: serverUrl,
      config: hasElectron
        ? {
            read: () => window.clientConfig.readClaude(),
            save: (t) => window.clientConfig.saveClaude(t),
            rollback: () => window.clientConfig.rollbackClaude(),
          }
        : null,
    },
    {
      id: 'codex',
      title: 'Codex',
      sub: '~/.codex/config.toml · TOML',
      lang: 'toml',
      fileName: 'config.toml',
      path: '~/.codex/config.toml',
      modelSlots: null,
      defaultBase: `${serverUrl.replace(/\/+$/, '')}/v1`,
      config: hasElectron
        ? {
            read: () => window.clientConfig.readCodex(),
            save: (t) => window.clientConfig.saveCodex(t),
            rollback: () => window.clientConfig.rollbackCodex(),
          }
        : null,
    },
  ];

  if (!hasElectron) {
    return (
      <div className="space-y-6">
        <Typography type="h2">客户端配置</Typography>
        <Card className="gap-4 p-5">
          <Card.Content className="p-0">
            <div className="flex items-center gap-2 text-muted">
              <Info className="size-5" />
              <Typography type="body-sm">本功能需在 Portunus 桌面端使用（依赖本地文件访问），当前浏览器环境不可用。</Typography>
            </div>
          </Card.Content>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between">
        <div>
          <Typography type="h2">客户端配置</Typography>
          <Typography type="body-sm" className="text-muted mt-1">
            把 Portunus 配成 Claude Code / Codex 的上游；下方直接编辑配置文件源码，保存即原子写入。
          </Typography>
        </div>
        {/* 客户端标签页 */}
        <div className="inline-flex rounded-lg border border-separator p-0.5">
          {['claude', 'codex'].map((c) => (
            <button
              key={c}
              type="button"
              className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
                client === c ? 'bg-accent text-accent-foreground' : 'text-muted hover:text-foreground'
              }`}
              onClick={() => setClient(c)}
            >
              {c === 'claude' ? 'Claude Code' : 'Codex'}
            </button>
          ))}
        </div>
      </div>

      {panels
        .filter((p) => p.id === client)
        .map((p) => (
          <ClientPanel key={p.id} {...p} />
        ))}
    </div>
  );
}