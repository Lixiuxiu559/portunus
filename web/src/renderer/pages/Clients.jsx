import { useEffect, useRef, useState } from 'react';
import { RefreshCw, FolderInput, Info, Terminal, Braces, Eye, EyeOff } from 'lucide-react';
import { Button, Card, Chip, Input, Label, Modal, Tabs, TextField, Typography, toast } from '@heroui/react';
import CodeEditor from '../components/CodeEditor';
import { setNavBlock } from '../utils/navGuard';
import { readCodexKey, readActiveProviderId, upsertCodexKey, readTopLevelKey, upsertTopLevelKey } from '../utils/codexToml';
import { readAuthApiKey, applyAuthApiKey } from '../utils/authJson';
import { ClaudeMark, OpenAIMark } from '../components/BrandMarks';
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
// Codex 读取锚定激活 provider 段（model_provider 指向的表），不读全文第一个匹配
const readCodexBase = (t) => readCodexKey(t, 'base_url');

// 把单个 env 键写回 settings.json（JSON round-trip，与 applyModels 同套路）。
// 键缺失自动补进 env（避免正则不命中静默丢改动）；解析失败返回 null 由调用方报告。
function setClaudeEnv(t, key, value) {
  let obj;
  try {
    obj = JSON.parse(t);
  } catch {
    return null;
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) obj = {};
  const env = obj.env && typeof obj.env === 'object' && !Array.isArray(obj.env) ? obj.env : {};
  if (value) env[key] = value;
  else delete env[key];
  obj.env = env;
  return JSON.stringify(obj, null, 2);
}

// 统一的键写回：JSON 写 env.<jsonKey>，TOML 写激活段 <tomlKey>。
// 返回 { text, ok, reason }：ok=false 时 reason 说明原因，调用方须警告而非假成功。
function applyKey(t, lang, jsonKey, tomlKey, value) {
  if (lang === 'json') {
    const next = setClaudeEnv(t, jsonKey, value);
    return next === null
      ? { text: t, ok: false, reason: 'settings.json 不是合法 JSON，未能写入' }
      : { text: next, ok: true, reason: '' };
  }
  const r = upsertCodexKey(t, tomlKey, value);
  return r.ok
    ? { ...r, reason: '' }
    : { ...r, reason: '未找到激活的 [model_providers.*] 段，请先在编辑器里补全 provider 段骨架' };
}

const applyBaseUrl = (t, lang, baseUrl) => applyKey(t, lang, 'ANTHROPIC_BASE_URL', 'base_url', baseUrl);

// 令牌（仅 JSON/Claude）：即时写回 env。TOML/Codex 的 API Key 正典在 auth.json，
// 编辑只改本地 state、保存时经 saveCodexAuth 落盘（见 handleSave）。
const applyToken = (t, token) => {
  const next = setClaudeEnv(t, 'ANTHROPIC_AUTH_TOKEN', token);
  return next === null
    ? { text: t, ok: false, reason: 'settings.json 不是合法 JSON，未能写入' }
    : { text: next, ok: true, reason: '' };
};

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

function ClientPanel({ id, title, lang, fileName, path, config, modelSlots, modelSelect, defaultBase, icon, hint, active = true }) {
  const [text, setText] = useState('');
  const [savedText, setSavedText] = useState('');
  const [savedAt, setSavedAt] = useState('');
  const [baseUrl, setBaseUrl] = useState(defaultBase);
  const [token, setToken] = useState('');
  const [model, setModel] = useState('');
  const [models, setModels] = useState({});
  const [modelOptions, setModelOptions] = useState([]);
  // auth.json 源码（API Key 正典文件）：与 config.toml 同一「原文编辑 + 整写保存」模型
  const [authText, setAuthText] = useState('');
  const [savedAuthText, setSavedAuthText] = useState('');
  const [authSavedAt, setAuthSavedAt] = useState('');
  const [authBackupExists, setAuthBackupExists] = useState(false);
  const [authBackupText, setAuthBackupText] = useState(null);
  const [authSaving, setAuthSaving] = useState(false);
  // 模型映射表格（catalog 行）：本地编辑，保存 config.toml 时统一生成目录文件
  const [catalogRows, setCatalogRows] = useState([]);
  const [savedCatalogRows, setSavedCatalogRows] = useState([]);
  const [backupExists, setBackupExists] = useState(false);
  const [backupText, setBackupText] = useState(null);
  const [saving, setSaving] = useState(false);
  const [fetching, setFetching] = useState(false);
  const [showToken, setShowToken] = useState(false);
  const [confirmRollback, setConfirmRollback] = useState(null);
  const [confirmReread, setConfirmReread] = useState(null);

  // 源码 → 表单反解析的防抖计时器
  const parseTimer = useRef(null);

  const dirty = text !== savedText;
  const authDirty = authText !== savedAuthText;
  const catalogDirty = JSON.stringify(catalogRows) !== JSON.stringify(savedCatalogRows);

  // 有未保存改动时注册导航守卫：切页前 Layout 拦截确认，防误触丢失
  useEffect(() => {
    setNavBlock(`client-${id}`, dirty || authDirty || catalogDirty ? '客户端配置' : null);
    return () => setNavBlock(`client-${id}`, null);
  }, [dirty, authDirty, catalogDirty, id]);

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
      // API Key 框的值是 authText 的投影（readAuthApiKey），不随 config.toml 解析
      if (modelSelect) setModel(readTopLevelKey(t, 'model'));
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
    // Codex 面板：auth.json 源码与备份 + 模型映射表格（catalog 文件反解析）
    if (lang === 'toml') {
      await loadAuth();
      const rc = await window.clientConfig.readCodexCatalog();
      const rows = rc && rc.ok && Array.isArray(rc.rows) ? rc.rows : [];
      setCatalogRows(rows);
      setSavedCatalogRows(rows);
    }
  };

  // 读取 auth.json 原文与备份（独立于 config.toml，可单独重读/回滚）
  const loadAuth = async () => {
    const ra = await window.clientConfig.readCodexAuth();
    if (ra && ra.ok) {
      const { text: at } = ra.data;
      setAuthText(at);
      setSavedAuthText(at);
      setAuthBackupExists(ra.data.backupExists);
      setAuthSavedAt(rightNow());
    } else {
      toast.danger((ra && ra.error) || '读取 auth.json 失败');
    }
    const rb = await window.clientConfig.readBackupCodexAuth();
    setAuthBackupText(rb && rb.ok && rb.data && rb.data.exists ? rb.data.text : null);
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
      if (modelSlots || modelSelect) fetchModels();
    }
    return () => clearTimeout(parseTimer.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSave = async () => {
    setSaving(true);
    // Codex：映射表格非空则生成模型目录（Codex /model 菜单的数据源）并注入指针键；
    // 目录生成失败仅告警，不阻塞 config.toml 保存；表格为空则不动用户自管的目录
    let t = text;
    if (lang === 'toml' && catalogRows.some((r) => r.model)) {
      const rc = await window.clientConfig.writeCodexCatalog(catalogRows);
      if (rc && rc.ok) {
        t = upsertTopLevelKey(t, 'model_catalog_json', rc.data.path).text;
        // 网关不保证支持 web_search hosted tool，声明目录即禁用，别给 Codex 一个死工具
        t = upsertTopLevelKey(t, 'web_search', 'disabled').text;
        setText(t);
        setSavedCatalogRows(catalogRows);
      } else {
        toast.danger(`${(rc && rc.error) || '模型目录生成失败'} · config.toml 保存不受影响`);
      }
    }
    const r = await config.save(t);
    setSaving(false);
    if (r && r.ok) {
      setSavedText(t);
      setSavedAt(rightNow());
      setBackupExists(!!(r.data && r.data.backupExists));
      toast.success('配置已原子写入');
      parseForm(t);
    } else {
      toast.danger(`${(r && r.error) || '保存失败'} · 可修正后重试，或点「重新读取」恢复磁盘版本`);
    }
  };

  // auth.json 独立保存：JSON 校验 + 首写备份 + 原子写（主进程）
  const handleAuthSave = async () => {
    setAuthSaving(true);
    const r = await window.clientConfig.saveCodexAuth(authText);
    setAuthSaving(false);
    if (r && r.ok) {
      setSavedAuthText(authText);
      setAuthSavedAt(rightNow());
      setAuthBackupExists(!!(r.data && r.data.backupExists));
      toast.success('auth.json 已原子写入');
    } else {
      toast.danger(`${(r && r.error) || 'auth.json 保存失败'} · 可修正后重试，或点「重新读取」恢复磁盘版本`);
    }
  };

  const performRollback = async (target) => {
    const r = target === 'auth' ? await window.clientConfig.rollbackCodexAuth() : await config.rollback();
    if (r && r.ok) {
      setConfirmRollback(null);
      toast.success(target === 'auth' ? '已从 .portunus.bak 回滚 auth.json' : '已从 .portunus.bak 回滚到接入前配置');
      if (target === 'auth') await loadAuth();
      else await load();
    } else {
      toast.danger((r && r.error) || '回滚失败');
    }
  };

  const performReread = async (target) => {
    setConfirmReread(null);
    if (target === 'auth') {
      await loadAuth();
      toast.success('已从磁盘重新读取 auth.json');
    } else {
      await load();
      toast.success('已从磁盘重新读取');
    }
  };

  const handleFillUrl = () => {
    setBaseUrl(defaultBase);
    const r = applyBaseUrl(text, lang, defaultBase);
    writeText(() => r.text);
    if (r.ok) toast.success('已填写 Portunus URL');
    else toast.danger(r.reason);
  };

  const handleImportToken = async () => {
    try {
      const k = await getAPIKey();
      if (!k || !k.key) {
        toast.danger('暂无令牌，请先到「设置」获取');
        return;
      }
      if (lang === 'json') {
        setToken(k.key);
        const r = applyToken(text, k.key);
        writeText(() => r.text);
        if (!r.ok) {
          toast.danger(r.reason);
          return;
        }
        toast.success('已导入 Portunus 令牌');
      } else {
        setAuthText((prev) => applyAuthApiKey(prev, k.key));
        toast.success('已点改 auth.json 源码，保存后生效');
      }
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

  // Codex 请求模型下拉：写入顶层 model 键；清空 = 删除该键（回落 Codex 内置默认）
  const handleModelSelect = (v) => {
    setModel(v);
    writeText((prev) => upsertTopLevelKey(prev, 'model', v === '' ? null : v).text);
  };

  // 模型映射表格：增删改均为本地态，保存 config.toml 时统一落成目录文件
  const handleAddRow = () => setCatalogRows((prev) => [...prev, { model: '', displayName: '', contextWindow: null }]);

  const handleAddAllGroups = () =>
    setCatalogRows((prev) => {
      const have = new Set(prev.map((r) => r.model));
      return [
        ...prev,
        ...modelOptions.filter((o) => !have.has(o.id)).map((o) => ({ model: o.id, displayName: '', contextWindow: null })),
      ];
    });

  const handleRemoveRow = (i) => setCatalogRows((prev) => prev.filter((_, j) => j !== i));

  const handleRowChange = (i, patch) =>
    setCatalogRows((prev) => prev.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  // 默认请求模型下拉选项 = 模型映射行 ∪ 分组列表（cc-switch 同源合并，映射行优先去重）
  const defaultModelOptions = (() => {
    const seen = new Set();
    const opts = [];
    for (const r of catalogRows) {
      if (!r.model || seen.has(r.model)) continue;
      seen.add(r.model);
      opts.push({ id: r.model, label: r.displayName || r.model });
    }
    for (const o of modelOptions) {
      if (seen.has(o.id)) continue;
      seen.add(o.id);
      opts.push(o);
    }
    return opts;
  })();

  const setSlot = (slotId, patch) => {
    const next = { ...models, [slotId]: { ...(models[slotId] || { base: '', onem: false, name: '' }), ...patch } };
    setModels(next);
    // 模型映射即改即写回源码，编辑器所见即所存
    if (modelSlots) writeText((prev) => applyModels(prev, next));
  };

  // 请求模型下拉 + 1M 勾选（各槽位共用）；无 1M 的槽位用等宽隐形占位保持对齐
  const renderModelControls = (slot, m) => {
    // 选项只有"不映射 + 分组"。当前映射值不在分组里（分组已删 / env 手写）时，
    // 以禁用项展示悬空状态——可见、不可再选，不自动清洗用户手写的配置。
    const dangling = m.base && !modelOptions.some((o) => o.id === m.base);
    return (
      <div className="mm-model">
        <div className="mm-select-wrap">
          <select
            className="mm-select"
            aria-label={`${slot.label} 请求模型`}
            value={m.base}
            onChange={(e) => {
              const v = e.target.value;
              // 选择请求模型后显示名同步跟随所选模型；清空时一并清空（之后手改显示名不会反向影响请求模型）
              setSlot(slot.id, v === '' ? { base: '', name: '' } : { base: v, name: v });
            }}
          >
            <option value="">— 不映射 —</option>
            {dangling && (
              <option value={m.base} disabled>
                {m.base}（分组不存在）
              </option>
            )}
            {modelOptions.map((o) => (
              <option key={o.id} value={o.id}>{o.label}</option>
            ))}
          </select>
        </div>
        {slot.has1m !== false ? (
          <label className="mm-1m">
            <input type="checkbox" checked={m.onem} onChange={(e) => setSlot(slot.id, { onem: e.target.checked })} />
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
            onChange={(e) => setSlot(slot.id, { name: e.target.value })}
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

  // solo 槽（默认兜底模型）独立于映射表：无显示名概念，只有请求模型 + 1M
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

  const curBase = lang === 'json' ? readClaudeBase(text) : readCodexBase(text);
  const activeProvider = lang === 'toml' ? readActiveProviderId(text) : null;
  const status = dirty
    ? { color: 'warning', label: '有未保存改动' }
    : activeProvider && activeProvider !== 'portunus'
      ? { color: 'warning', label: `激活 provider：${activeProvider}（非 portunus）` }
      : /306[01]|13060|portunus/i.test(curBase)
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
                writeText((prev) => applyBaseUrl(prev, lang, v).text);
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
              value={lang === 'json' ? token : readAuthApiKey(authText)}
              onChange={(v) => {
                // JSON 即时写回 env；TOML 点改 auth.json 源码里的 OPENAI_API_KEY 值
                if (lang === 'json') {
                  setToken(v);
                  writeText((prev) => applyToken(prev, v).text);
                } else {
                  setAuthText((prev) => applyAuthApiKey(prev, v));
                }
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
          {modelSelect && (
            <div className="flex items-end gap-2">
              <div className="flex flex-1 flex-col gap-1">
                <Label>默认请求模型</Label>
                <div className="mm-select-wrap">
                  <select
                    className="mm-select"
                    aria-label="Codex 默认请求模型"
                    value={model}
                    onChange={(e) => handleModelSelect(e.target.value)}
                  >
                    <option value="">— 不设置 —</option>
                    {model && !defaultModelOptions.some((o) => o.id === model) && (
                      <option value={model} disabled>
                        {model}（不在分组/映射中）
                      </option>
                    )}
                    {defaultModelOptions.map((o) => (
                      <option key={o.id} value={o.id}>{o.label}</option>
                    ))}
                  </select>
                </div>
              </div>
            </div>
          )}
          {modelSelect && model && catalogRows.length > 0 && !catalogRows.some((r) => r.model === model) && (
            <div className="flex items-center gap-2">
              <Typography type="body-sm" className="text-muted">
                该模型不在模型映射中，Codex 的 /model 菜单不会列出它（直接请求仍然有效）。
              </Typography>
              <Button
                variant="tertiary"
                size="sm"
                onPress={() => setCatalogRows((prev) => [...prev, { model, displayName: model, contextWindow: null }])}
              >
                加入模型映射
              </Button>
            </div>
          )}
          {modelSelect && (
            <div className="flex flex-col gap-2">
              <div className="flex items-center justify-between">
                <Typography type="body-sm" className="text-muted">
                  模型映射（Codex /model 菜单，保存 config.toml 时生成）
                </Typography>
                <div className="flex gap-2">
                  <Button variant="tertiary" size="sm" onPress={handleFetchModels} isDisabled={fetching}>
                    <RefreshCw className={`size-4 ${fetching ? 'animate-spin' : ''}`} />
                    获取模型
                  </Button>
                  <Button variant="tertiary" size="sm" onPress={handleAddAllGroups} isDisabled={!modelOptions.length}>
                    全部添加
                  </Button>
                  <Button variant="tertiary" size="sm" onPress={handleAddRow}>添加行</Button>
                </div>
              </div>
              {catalogRows.length === 0 ? (
                <Typography type="body-sm" className="text-muted">
                  先「获取模型」再「全部添加」，或「添加行」逐个选择；显示名 / 上下文留空用默认值。
                </Typography>
              ) : (
                <div className="flex flex-col gap-2">
                  {catalogRows.map((row, i) => (
                    <div className="flex items-center gap-2" key={i}>
                      <div className="mm-select-wrap w-48 shrink-0">
                        <select
                          className="mm-select"
                          aria-label={`映射行 ${i + 1} 请求模型`}
                          value={row.model}
                          onChange={(e) => handleRowChange(i, { model: e.target.value })}
                        >
                          <option value="">— 请选择 —</option>
                          {row.model && !modelOptions.some((o) => o.id === row.model) && (
                            <option value={row.model} disabled>
                              {row.model}（分组不存在）
                            </option>
                          )}
                          {modelOptions.map((o) => (
                            <option key={o.id} value={o.id}>{o.label}</option>
                          ))}
                        </select>
                      </div>
                      <input
                        className="mm-input flex-1"
                        placeholder="显示名（默认=模型名）"
                        aria-label={`映射行 ${i + 1} 显示名`}
                        value={row.displayName}
                        onChange={(e) => handleRowChange(i, { displayName: e.target.value })}
                        spellCheck={false}
                      />
                      <input
                        className="mm-input w-32 shrink-0"
                        type="number"
                        min={1}
                        placeholder="上下文 128k"
                        aria-label={`映射行 ${i + 1} 上下文窗口`}
                        value={row.contextWindow ?? ''}
                        onChange={(e) => handleRowChange(i, { contextWindow: e.target.value === '' ? null : Number(e.target.value) || null })}
                      />
                      <Button
                        variant="tertiary"
                        size="sm"
                        onPress={() => handleRemoveRow(i)}
                        className="shrink-0"
                        aria-label={`删除映射行 ${i + 1}`}
                      >
                        删除
                      </Button>
                    </div>
                  ))}
                  <Typography type="body-sm" className="text-muted">
                    显示名只影响 /model 菜单；上下文留空按 128k 生成。
                  </Typography>
                </div>
              )}
            </div>
          )}
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
                {modelSlots.filter((s) => !s.solo).map(renderSlot)}
              </div>
              {modelSlots.filter((s) => s.solo).map(renderSolo)}
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
          onRollback={() => setConfirmRollback('config')}
          onReread={dirty ? () => setConfirmReread('config') : () => performReread('config')}
          saving={saving}
        />

        {/* auth.json 源码（API Key 正典，仅 Codex）：与 config.toml 同一编辑模型 */}
        {lang === 'toml' && (
          <>
            <Typography type="body-sm" className="text-muted">
              auth.json（API Key 正典源码）· 上方 API Key 框改写的就是这里的 OPENAI_API_KEY 值
            </Typography>
            <CodeEditor
              lang="json"
              fileName="auth.json"
              text={authText}
              backupText={authBackupText}
              onTextChange={(t) => setAuthText(t)}
              dirty={authDirty}
              savedAt={authSavedAt}
              canRollback={authBackupExists}
              active={active}
              onSave={handleAuthSave}
              onRollback={() => setConfirmRollback('auth')}
              onReread={authDirty ? () => setConfirmReread('auth') : () => performReread('auth')}
              saving={authSaving}
            />
          </>
        )}
      </Card.Content>

      {/* 重新读取二次确认：有未保存改动时拦截，防止被磁盘内容静默覆盖（config / auth 双目标） */}
      <Modal.Backdrop isOpen={!!confirmReread} onOpenChange={(o) => !o && setConfirmReread(null)}>
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.CloseTrigger />
            <Modal.Header>
              <Modal.Heading>{confirmReread === 'auth' ? '重新读取 auth.json' : '重新读取配置'}</Modal.Heading>
            </Modal.Header>
            <Modal.Body>
              <Typography color="muted">
                当前{confirmReread === 'auth' ? ' auth.json ' : ' 配置文件 '}有未保存改动，重新读取将用磁盘内容覆盖并丢弃这些改动，确定继续吗？
              </Typography>
            </Modal.Body>
            <Modal.Footer>
              <Button slot="close" variant="secondary">
                取消
              </Button>
              <Button variant="danger" onPress={() => performReread(confirmReread)}>
                丢弃并重新读取
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>

      {/* 回滚二次确认（config / auth 双目标） */}
      <Modal.Backdrop isOpen={!!confirmRollback} onOpenChange={(o) => !o && setConfirmRollback(null)}>
        <Modal.Container size="sm">
          <Modal.Dialog>
            <Modal.CloseTrigger />
            <Modal.Header>
              <Modal.Heading>从备份回滚</Modal.Heading>
            </Modal.Header>
            <Modal.Body>
              <Typography color="muted">
                {confirmRollback === 'auth'
                  ? `${authDirty ? '当前 auth.json 有未保存改动，回滚后将一并丢弃。' : ''}确定把 auth.json 恢复为备份（.portunus.bak）吗？`
                  : `${dirty ? '当前有未保存改动，回滚后将一并丢弃。' : ''}确定把配置恢复为接入 Portunus 之前的备份（.portunus.bak）吗？`}
              </Typography>
            </Modal.Body>
            <Modal.Footer>
              <Button slot="close" variant="secondary">
                取消
              </Button>
              <Button variant="danger" onPress={() => performRollback(confirmRollback)}>
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
  // 纯浏览器开发时 window.api 不存在，兜底空串避免下方 defaultBase 拼 URL 时崩溃
  const serverUrl = window.api?.getServerUrl?.() ?? '';

  const panels = [
    {
      id: 'claude',
      title: 'Claude Code CLI',
      tabLabel: 'claude-cli',
      mark: <ClaudeMark className="size-3.5" />,
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
      tabLabel: 'codex-cli',
      mark: <OpenAIMark className="size-3.5" />,
      fileName: 'config.toml',
      lang: 'toml',
      path: '~/.codex/config.toml',
      modelSlots: null,
      modelSelect: true,
      defaultBase: `${serverUrl.replace(/\/+$/, '')}/v1`,
      icon: <Braces className="size-4" />,
      hint: (
        <>
          API Key 框改写的是 <span className="font-mono">~/.codex/auth.json</span> 源码里的 <span className="font-mono">OPENAI_API_KEY</span> 值（下方源码可见，保存整写落盘）；「模型映射」表格策展 Codex /model 菜单（保存 config.toml 时生成 <span className="font-mono">portunus-model-catalog.json</span>）；config.toml 的 <span className="font-mono">experimental_bearer_token</span> 优先级高于 auth.json，手写了它会盖过 API Key 框的值。
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
          <Typography type="body" className="text-muted max-w-[65ch]">
            把 Portunus 配成 Claude Code / Codex 的上游；下方直接编辑配置文件源码，保存即原子写入，写前自动备份。
          </Typography>

          <Tabs selectedKey={client} onSelectionChange={setClient} className="w-fit">
            <Tabs.ListContainer>
              <Tabs.List aria-label="客户端">
                {panels.map((p) => (
                  <Tabs.Tab id={p.id} key={p.id} className="gap-1.5 min-w-max whitespace-nowrap">
                    {p.mark}
                    {p.tabLabel}
                    <Tabs.Indicator />
                  </Tabs.Tab>
                ))}
              </Tabs.List>
            </Tabs.ListContainer>
          </Tabs>

          {/* 双面板常驻：切换 Tab 不卸载，未保存改动得以保留；切换时 page-in 弱化高度跳变 */}
          {panels.map((p) => (
            <div
              key={p.id}
              className={p.id === client ? 'page-in' : 'hidden'}
              aria-hidden={p.id !== client}
            >
              <ClientPanel {...p} active={p.id === client} />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}