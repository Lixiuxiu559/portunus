import { useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { BookOpen, Copy, Eye, EyeOff, ExternalLink, Info, RefreshCw } from 'lucide-react';
import { Button, Typography, Modal, Select, ListBox, Label, toast } from '@heroui/react';
import { ClaudeMark, OpenAIMark } from '../components/BrandMarks';
import { listGroups } from '../api/group';
import { getAPIKey, regenerateAPIKey } from '../api/apikey';
import { buildConnectionGuide } from '../utils/connectionGuide';
import { additionalClientGuides } from '../utils/clientGuides';
import { buildServiceExamples, selectServiceGroup } from '../utils/serviceAccess';
import './service-access.css';

async function copy(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast.success('已复制');
  } catch {
    toast.danger('复制失败，请手动选择并复制');
  }
}

function CopyValue({ label, value, display = value, disabled = false }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 py-3">
      <div className="min-w-0 flex-1">
        <Typography type="body-xs" className="text-muted mb-1">{label}</Typography>
        <code className="block truncate text-xs" title={String(display)}>{display}</code>
      </div>
      <Button isIconOnly size="sm" variant="tertiary" aria-label={`复制${label}`} isDisabled={disabled} onPress={() => copy(value)}>
        <Copy className="size-3.5" />
      </Button>
    </div>
  );
}

const clients = [
  { id: 'claude', label: 'Claude Code', icon: <ClaudeMark className="size-4" />, description: '使用本地网关，为 Claude Code 接入你的模型分组。', command: 'claude', docs: 'https://code.claude.com/docs/zh-CN/setup' },
  { id: 'codex', label: 'Codex', icon: <OpenAIMark className="size-4" />, description: '使用 OpenAI Responses 接口，将 Codex CLI 的请求交给 Portunus 路由。', command: 'codex', docs: 'https://developers.openai.com/codex/cli/' },
  ...additionalClientGuides.map((c) => ({ ...c, icon: <BookOpen className="size-4 shrink-0" /> })),
];

function DocLink({ href, children }) {
  return <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-accent hover:underline underline-offset-4" onClick={(e) => {
    if (window.api?.openExternal) {
      e.preventDefault();
      window.api.openExternal(href).catch(() => toast.danger('无法打开链接，请复制地址到浏览器'));
    }
  }}>{children}<ExternalLink className="size-3 shrink-0" /></a>;
}

function GuideStep({ number, title, children }) {
  return (
    <section id={`guide-step-${number}`} className="service-guide-step scroll-mt-5">
      <h2 className="mb-4 flex items-center gap-3 text-base font-semibold">
        <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent/10 text-sm text-accent">{number}</span>
        {title}
      </h2>
      <div className="space-y-4 pl-10 text-sm leading-7">{children}</div>
    </section>
  );
}

function CodeSample({ title, code, preview = code, disabled = false }) {
  return (
    <div className="overflow-hidden rounded-xl border border-separator bg-background/60">
      <div className="flex flex-wrap items-center justify-between gap-2 bg-surface-secondary/70 px-4 py-2">
        <span className="text-xs text-muted">{title}</span>
        <Button size="sm" variant="tertiary" aria-label={`复制${title}`} isDisabled={disabled} onPress={() => copy(code)}>
          <Copy className="size-3.5" />复制
        </Button>
      </div>
      <pre className="service-code m-0 overflow-auto p-4 text-xs leading-6 select-text"><code>{preview}</code></pre>
    </div>
  );
}

function Note({ children }) {
  return <div className="flex gap-2.5 rounded-lg border border-accent/15 bg-accent/5 p-3 text-sm leading-6"><Info className="mt-1 size-4 shrink-0 text-accent" /><div className="min-w-0">{children}</div></div>;
}

function Parameters({ rows }) {
  return (
    <div className="overflow-x-auto rounded-lg border border-separator">
      <table className="w-full text-left text-sm">
        <thead className="bg-surface-secondary/70 text-muted"><tr><th className="px-3 py-2 font-medium">字段</th><th className="px-3 py-2 font-medium">说明</th></tr></thead>
        <tbody>{rows.map(([key, description]) => <tr key={key} className="border-t border-separator"><td className="px-3 py-2 align-top"><code className="text-xs break-all">{key}</code></td><td className="px-3 py-2 text-muted">{description}</td></tr>)}</tbody>
      </table>
    </div>
  );
}

export default function Clients() {
  const [client, setClient] = useState('api');
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedGroup = searchParams.get('group');
  const [networkMode, setNetworkMode] = useState(null);
  const [savingMode, setSavingMode] = useState(false);
  const [regenerateOpen, setRegenerateOpen] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [stream, setStream] = useState(false);
  const [apiProtocol, setAPIProtocol] = useState('chat');
  const scrollRef = useRef(null);
  const [groups, setGroups] = useState([]);
  const [selectedGroup, setSelectedGroup] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [showKey, setShowKey] = useState(false);
  const [loading, setLoading] = useState(true);
  // 已有数据时的刷新不进入加载态：保留当前配置展示，只在按钮上给反馈
  const [refreshing, setRefreshing] = useState(false);
  const [loadError, setLoadError] = useState(false);
  // 桌面端取实际 sidecar 地址；浏览器开发环境直连开发后端，不把 Vite 端口交给客户端。
  const serverUrl = window.api?.getServerUrl?.() || 'http://127.0.0.1:3060';

  const load = async (isRefresh = false) => {
    // 首次加载或没有可展示的数据时才遮蔽内容；刷新失败保留旧配置
    const blocking = !isRefresh || (!groups.length && !apiKey);
    if (blocking) setLoading(true); else setRefreshing(true);
    const [groupResult, keyResult] = await Promise.allSettled([listGroups(), getAPIKey()]);
    const failed = groupResult.status === 'rejected' || keyResult.status === 'rejected';
    // 刷新失败保留旧配置，只有首次加载失败才进入错误态
    if (!failed || blocking) setLoadError(failed);
    if (groupResult.status === 'fulfilled') {
      const next = groupResult.value || [];
      setGroups(next);
      setSelectedGroup((old) => selectServiceGroup(next, requestedGroup, old));
    } else if (blocking) {
      setGroups([]);
      setSelectedGroup('');
    }
    if (keyResult.status === 'fulfilled') setApiKey(keyResult.value?.key || '');
    else if (blocking) setApiKey('');
    setLoading(false);
    setRefreshing(false);
  };

  useEffect(() => { load(); }, []);
  useEffect(() => {
    setSelectedGroup((old) => selectServiceGroup(groups, requestedGroup, old));
  }, [groups, requestedGroup]);
  useEffect(() => {
    window.api?.getNetworkMode?.().then((mode) => setNetworkMode(mode)).catch(() => toast.danger('访问范围加载失败'));
  }, []);

  const saveNetworkMode = async (mode) => {
    if (mode === networkMode || !window.api?.setNetworkMode) return;
    setSavingMode(true);
    try {
      const result = await window.api.setNetworkMode(mode);
      setNetworkMode(result?.networkMode ?? mode);
      toast.success(result?.restarted ? '访问范围已保存，服务已自动重启' : '访问范围已保存');
    } catch (error) { toast.danger(`保存失败：${error?.message || ''}`); }
    finally { setSavingMode(false); }
  };
  const generateKey = async () => {
    setRegenerating(true);
    try {
      const key = await regenerateAPIKey();
      setApiKey(key?.key || '');
      setRegenerateOpen(false);
      toast.success('令牌已生成，请更新所有调用方的配置');
    } catch { /* 请求拦截器显示错误 */ }
    finally { setRegenerating(false); }
  };
  const chooseGroup = (name) => {
    const next = new URLSearchParams(searchParams);
    next.set('group', name);
    setSearchParams(next, { replace: true });
  };

  const guide = buildConnectionGuide({ serverUrl, apiKey, model: selectedGroup });
  const preview = buildConnectionGuide({ serverUrl, apiKey: showKey ? apiKey : '', model: selectedGroup });
  const ready = !loading && !loadError && !!apiKey && !!selectedGroup;
  const selected = groups.find((g) => g.name === selectedGroup);

  const examples = buildServiceExamples({ serverUrl, apiKey, model: selectedGroup, stream });
  const examplePreview = buildServiceExamples({ serverUrl, apiKey: showKey ? apiKey : '', model: selectedGroup, stream });
  const missingRequestedGroup = requestedGroup !== null && !groups.some((g) => g.name === requestedGroup);
  const current = clients.find((c) => c.id === client) || clients[0];
  const isClaude = client === 'claude';
  const isCodex = client === 'codex';
  const fileName = isClaude ? 'settings.json' : 'config.toml';
  const directory = isClaude ? '.claude' : '.codex';
  const parameterRows = isClaude ? [
    ['ANTHROPIC_BASE_URL', 'Portunus 网关地址，不包含 /v1；客户端会自动拼接请求路径。'],
    ['ANTHROPIC_AUTH_TOKEN', 'Portunus 令牌，用于访问本地网关。'],
    ['ANTHROPIC_MODEL', '默认使用的分组名称，不是上游渠道的模型 ID。'],
    ['ANTHROPIC_DEFAULT_*_MODEL', '将 Sonnet、Opus、Haiku 档位映射到分组；也可分别填写不同分组。'],
  ] : isCodex ? [
    ['model_provider', '选择下方定义的 portunus provider。'],
    ['model', 'Portunus 中的分组名称，由网关选择实际调用的上游模型。'],
    ['base_url', 'OpenAI 兼容地址，需要包含 /v1。'],
    ['wire_api', '使用 responses，与 Portunus 的 Responses 接口对接。'],
    ['experimental_bearer_token', '该 provider 使用的 Portunus 令牌，无需修改 auth.json。'],
  ] : current.parameters;

  const chooseClient = (id) => {
    setClient(id);
    scrollRef.current?.scrollTo({ top: 0 });
  };
  const protocolIndex = Math.max(0, examples.findIndex((example) => example.id === apiProtocol));
  const protocolExample = examples[protocolIndex];
  const strategyLabels = { manual: '手动选择', round_robin: '轮询', failover: '故障转移' };
  const selectedDescription = selected ? `${strategyLabels[selected.strategy] || selected.strategy} · ${selected.items?.length || 0} 个成员` : '';
  const logsPath = selectedGroup ? `/logs?group=${encodeURIComponent(selectedGroup)}` : '/logs';

  return (
    <div className="service-page flex flex-col flex-1 min-h-0 min-w-0">
      <h1 className="sr-only">服务接入</h1>
      <div className="service-toolbar">
        <p className="service-toolbar-hint">选择模型分组，为你的应用提供一个稳定入口。</p>
        <Button size="md" variant="secondary" onPress={() => load(true)} isPending={loading || refreshing} aria-label="刷新连接信息"><RefreshCw className="size-4" />刷新连接</Button>
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
        <div className="service-access-grid">
          <aside className="service-connection-panel rounded-2xl bg-surface/70 p-5" aria-label="连接配置">
            <div className="mb-5 flex items-center justify-between gap-2">
              <h2 className="text-sm font-semibold">连接配置</h2>
              <span className="rounded-md bg-surface-secondary px-2 py-1 text-[11px] text-muted">本地网关</span>
            </div>
            <Select
              placeholder={loading ? '加载中…' : groups.length ? '请选择分组' : '暂无分组'}
              selectedKey={selectedGroup || null}
              onSelectionChange={(key) => { if (key !== null) chooseGroup(String(key)); }}
              isDisabled={loading || !groups.length}
              className="w-full min-w-0"
            >
              <Label className="mb-1 text-xs text-muted">模型分组 · 客户端 model</Label>
              <Select.Trigger aria-label={selected ? `模型分组：${selected.name}` : '选择模型分组'} aria-labelledby={null} className="service-group-trigger w-full min-w-0 rounded-xl">
                <Select.Value className="min-w-0 flex-1 text-left">
                  {() => selected ? <span className="block min-w-0"><span className="block truncate font-medium text-sm" title={selected.name}>{selected.name}</span><span className="mt-1 block truncate text-[11px] text-muted">{selectedDescription}</span></span> : <span className="text-sm text-muted">{loading ? '加载中…' : groups.length ? '请选择分组' : '暂无分组'}</span>}
                </Select.Value>
                <Select.Indicator className="size-4 shrink-0 text-muted" />
              </Select.Trigger>
              <Select.Popover className="service-group-popover">
                <ListBox aria-label="可接入的模型分组">
                  {groups.map((group) => <ListBox.Item key={group.name} id={group.name} textValue={group.name} className="min-w-0 py-2.5">
                    <div className="min-w-0 flex-1"><span className="block truncate text-sm font-medium" title={group.name}>{group.name}</span><span className="mt-0.5 block text-xs text-muted">{strategyLabels[group.strategy] || group.strategy} · {group.items?.length || 0} 个成员</span></div>
                    <ListBox.ItemIndicator />
                  </ListBox.Item>)}
                </ListBox>
              </Select.Popover>
            </Select>
            {!loading && missingRequestedGroup && <p role="alert" className="mt-3 text-xs leading-5 text-warning">链接中的分组「{requestedGroup || '空名称'}」不存在，请重新选择。未替换为其他分组。</p>}
            {!loading && !groups.length && <p className="mt-3 text-xs leading-5 text-muted">添加<Link to="/channels" className="text-accent underline">上游渠道</Link>后，创建<Link to="/groups" className="text-accent underline">模型分组</Link>再接入。</p>}
            {selected && !selected.items?.length && <p className="mt-3 text-xs leading-5 text-warning">分组尚无成员，请先添加上游模型。</p>}
            <div className="mt-5 divide-y divide-separator border-y border-separator">
              <CopyValue label="网关地址" value={guide.baseUrl} />
              <CopyValue label="OpenAI Base URL · 含 /v1" value={guide.openAIBaseUrl} />
              <CopyValue label="访问令牌" value={apiKey} display={loading ? '加载中…' : apiKey ? (showKey ? apiKey : '••••••••••••••••') : '尚未生成'} disabled={!apiKey || loading} />
            </div>
            {loadError && <p role="alert" className="mt-3 text-xs text-danger">连接信息加载失败，请刷新重试。</p>}
            <div className="mt-3 flex flex-wrap items-center gap-1">
              <Button size="sm" variant="tertiary" isDisabled={!apiKey} onPress={() => setShowKey(!showKey)}>{showKey ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}{showKey ? '隐藏令牌' : '显示令牌'}</Button>
              <Button size="sm" variant="tertiary" isDisabled={loading || loadError} isPending={regenerating} onPress={() => apiKey ? setRegenerateOpen(true) : generateKey()}>{apiKey ? '重置令牌' : '生成令牌'}</Button>
            </div>
            <div className="mt-3 border-t border-separator pt-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium">访问范围</span>
                {window.api?.setNetworkMode ? <div className="service-network-segment" role="group" aria-label="服务访问范围" aria-busy={savingMode}>
                  {[['local', '仅本机'], ['lan', '局域网']].map(([mode, label]) => <button type="button" key={mode} className="service-network-option" aria-pressed={networkMode === mode} disabled={savingMode || networkMode === null} onClick={() => saveNetworkMode(mode)}>{label}</button>)}
                </div> : <span className="text-xs text-muted">{networkMode === 'lan' ? '局域网' : networkMode === 'local' ? '仅本机' : window.api?.getNetworkMode ? '正在读取' : '由启动配置决定'}</span>}
              </div>
              <details className="service-disclosure mt-2 text-xs text-muted">
                <summary>访问与令牌说明</summary>
                <div className="space-y-2 pt-3 leading-6"><p>网关地址供本机调用。其他设备请使用本机局域网 IP 和端口 {new URL(serverUrl).port || '80'}，不能使用 127.0.0.1 或 0.0.0.0 作为目标地址。</p><p>切换范围会自动重启服务，进行中的请求可能中断。管理操作仅限本机，服务请求始终需要令牌。</p><p>重置令牌后旧令牌立即失效，所有调用方都需更新。上游 API Key 只在上游渠道中保存。</p><p>应用运行期间提供服务，退出后停止。分组名称就是客户端 model，调整组内上游无需修改客户端。</p></div>
              </details>
            </div>
          </aside>

          <section className="min-w-0 rounded-2xl bg-surface/70" aria-label="接入示例">
            <div className="border-b border-separator px-5 pt-5 pb-4">
              <div className="mb-4 flex items-center justify-between gap-2"><h2 className="text-sm font-semibold">接入示例</h2><span className="text-[11px] text-muted">手动配置 · 保留现有设置</span></div>
              <nav aria-label="选择接入工具" className="service-tool-nav flex gap-1 overflow-x-auto no-scrollbar">
                {[{ id: 'api', label: 'API', icon: <BookOpen className="size-3.5" /> }, ...clients].map((tool) => <Button key={tool.id} size="sm" variant={client === tool.id ? 'secondary' : 'tertiary'} aria-pressed={client === tool.id} onPress={() => chooseClient(tool.id)} className={`shrink-0 ${client === tool.id ? 'text-accent' : 'text-muted'}`}>{tool.icon}{tool.label}</Button>)}
              </nav>
            </div>
            <div ref={scrollRef} className="min-w-0 p-5">
              {client === 'api' ? <div className="space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="flex flex-wrap gap-1" role="group" aria-label="API 协议">
                    {examples.map((example) => <Button key={example.id} size="sm" variant={example.id === apiProtocol ? 'secondary' : 'tertiary'} aria-pressed={example.id === apiProtocol} onPress={() => setAPIProtocol(example.id)}>{example.label}</Button>)}
                  </div>
                  <label className="flex cursor-pointer items-center gap-2 text-xs text-muted"><input className="service-stream-check" type="checkbox" checked={stream} onChange={(event) => setStream(event.target.checked)} />流式响应</label>
                </div>
                <CodeSample title={`POST ${protocolExample.path}`} code={protocolExample.code} preview={examplePreview[protocolIndex].code} disabled={!ready} />
                <p className="text-xs leading-6 text-muted">输出预算 128 token。复制不会调用上游；手动执行可能产生费用。预览默认隐藏令牌，复制包含真实令牌。</p>
                <details className="service-disclosure border-t border-separator pt-4 text-sm">
                  <summary>验证接入与模型列表</summary>
                  <div className="mt-4 space-y-3 text-xs leading-6 text-muted"><CopyValue label="模型列表 · GET，需相同 Bearer 令牌" value={`${guide.openAIBaseUrl}/models`} /><p>执行示例后，到<Link to={logsPath} className="text-accent hover:underline">调用记录</Link>查看分组、命中上游与结果。模型列表只验证基础连接，一次生成成功只证明当前请求路径。</p><p>思考模型的预算可能包含思考 token。思考强度和输出预算在调用方设置，无需在分组重复配置。</p></div>
                </details>
              </div> : <div className="space-y-4">
                <div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="text-base font-medium">{current.label}</h3><p className="mt-1 text-xs leading-6 text-muted">{current.description}</p></div>{current.docs && <span className="text-xs"><DocLink href={current.docs}>官方文档</DocLink></span>}</div>
                <CodeSample title={isClaude || isCodex ? `${fileName} · 合并示例` : current.sampleTitle} code={isClaude || isCodex ? guide[client] : guide[current.sampleKey]} preview={isClaude || isCodex ? preview[client] : preview[current.sampleKey]} disabled={!ready} />
                <p className="text-xs leading-6 text-muted">手动合并到现有配置，保留 MCP、权限及其他服务商。复制包含真实令牌，Portunus 不读取或修改客户端文件。</p>
                <details key={client} className="service-disclosure border-t border-separator pt-4 text-sm">
                  <summary>详细配置步骤与常见问题</summary>
                  <div className="service-manual mt-6 space-y-7">
          <GuideStep number={1} title="开始之前">
            <p>请先完成以下准备：</p>
            <ul className="list-disc space-y-1 pl-5 text-muted">
              <li>保持 Portunus 运行，在<Link to="/channels" className="text-accent hover:underline">上游渠道</Link>中添加上游服务，并确认模型已同步。</li>
              <li>在<Link to="/groups" className="text-accent hover:underline">模型分组</Link>中创建分组并添加模型，在本页上方生成 Portunus 访问令牌。</li>
              {current.command ? <li>已安装 {current.label}。在终端执行 <code>{current.command} --version</code> 确认安装；尚未安装可参考 <DocLink href={current.docs}>{current.label} 官方安装说明</DocLink>。</li> : <li>{current.requirement}{current.docs && <> <DocLink href={current.docs}>{current.label} 官方配置文档</DocLink></>}</li>}
            </ul>
          </GuideStep>

          <GuideStep number={2} title="确认分组与连接参数">
            <p>当前分组为 <code>{selectedGroup || '尚未选择'}</code>。需要更换时，在上方“选择模型分组”中操作，以下配置随选择更新。</p>
          </GuideStep>

          <GuideStep number={3} title="手动配置客户端">
            {isClaude || isCodex ? <>
              <p>用文本编辑器打开下面的配置文件。目录或文件不存在时，请手动创建。</p>
              <div className="space-y-1 rounded-lg bg-surface-secondary/60 px-4 py-3 text-xs">
                <p><span className="mr-3 text-muted">macOS / Linux</span><code>~/{directory}/{fileName}</code></p>
                <p><span className="mr-3 text-muted">Windows</span><code>%USERPROFILE%\{directory}\{fileName}</code></p>
              </div>
              <p>{isClaude ? <>将示例中的字段合并到文件的 <code>env</code> 对象。文件为空时，可以直接粘贴完整示例。</> : <>将下面两个顶层字段放在所有 <code>[表头]</code> 之前，再添加 <code>[model_providers.portunus]</code> 表。已有同名字段或表时，请修改原项。</>}</p>
              <Note>已有配置时只合并相关字段，保留 MCP、权限和其他设置。{isClaude ? 'JSON 不支持注释或末尾多余的逗号；已有 env 对象时，请把字段合并进去，避免出现两个 env。' : 'TOML 中同名字段和同名表不能重复；本示例在 provider 内配置令牌，不需要修改 auth.json。'}</Note>
            </> : <>
              <ol className="list-decimal space-y-2 pl-5">{current.setup.map((step) => <li key={step}>{step}</li>)}</ol>
              {client === 'opencode' && <details className="rounded-lg border border-separator">
                <summary className="cursor-pointer px-4 py-2 font-medium">使用 OpenCode 2.x？</summary>
                <div className="space-y-3 p-3"><p>在项目的 opencode.jsonc 中合并以下结构。2.x 使用 providers / package / settings；不要和上面的 1.x 配置混用。</p>
                  <CodeSample title="opencode.jsonc · OpenCode 2.x 合并示例" code={guide.opencodeV2} preview={preview.opencodeV2} disabled={!ready} />
                  <DocLink href="https://opencode.ai/v2/docs/providers">OpenCode 2.x 官方配置说明</DocLink>
                </div>
              </details>}
              <Note>{current.note}</Note>

            </>}
            <details className="rounded-lg border border-separator">
              <summary className="cursor-pointer px-4 py-2 text-sm font-medium">这些字段分别代表什么？</summary>
              <div className="px-3 pb-3"><Parameters rows={parameterRows} /></div>
            </details>
          </GuideStep>

          <GuideStep number={4} title="启动客户端并验证接入">
            {current.command ? <>
              <p>保存配置，关闭已有的 {current.label} 会话，在项目目录重新打开终端并运行：</p>
              <CodeSample title="终端" code={current.command} />
            </> : <p>{current.launch}</p>}
            <p>发送一条简单消息，例如「你好，请简短介绍自己」，然后打开 Portunus 的<Link to={logsPath} className="text-accent hover:underline">调用记录</Link>页面。</p>
            <ul className="list-disc space-y-1 pl-5 text-muted">
              <li>出现新的请求记录，且分组为 <code>{selectedGroup || '所选分组'}</code>：请求已到达 Portunus。</li>
              <li>请求成功并收到客户端回复：说明本次网关、路由和上游调用已连通。</li>
              <li>没有新记录：优先检查客户端使用的地址、令牌，以及配置是否已生效。</li>
            </ul>
          </GuideStep>

          <section id="guide-faq" className="scroll-mt-5 border-t border-separator pt-6">
            <h2 className="mb-4 text-base font-semibold">常见问题</h2>
            <div className="divide-y divide-separator text-sm leading-7">
              <details className="py-3"><summary className="cursor-pointer font-medium">连接被拒绝，或提示无法连接到服务？</summary><p className="pt-2 text-muted">确认 Portunus 正在运行，且客户端和网关在同一台电脑上。当前网关地址为 <code>{guide.baseUrl}</code>。远程容器、SSH 会话或其他设备中的 127.0.0.1 指向它们自身，需要使用可访问的网关地址；局域网访问可在本页服务访问范围中开启。</p></details>
              <details className="py-3"><summary className="cursor-pointer font-medium">返回 401，或提示令牌无效？</summary><p className="pt-2 text-muted">客户端应填写 Portunus 令牌。若在服务接入中重新生成过令牌，请重新复制并手动更新客户端配置；渠道中填写的上游 API Key 不能代替网关令牌。</p></details>
              <details className="py-3"><summary className="cursor-pointer font-medium">提示模型不存在，或分组没有可用模型？</summary><p className="pt-2 text-muted">确认模型名与分组名称完全一致，分组中已添加模型，所选路由策略配置完整，并检查上游渠道是否可用。可在调用记录中查看具体错误。</p></details>
              <details className="py-3"><summary className="cursor-pointer font-medium">已经保存配置，为什么仍连接原来的服务商？</summary><p className="pt-2 text-muted">关闭旧会话后重新启动客户端。检查终端环境变量、项目级配置或启动参数是否覆盖了刚修改的设置；Codex 还需确认顶层 model_provider 已选为 portunus。</p></details>
            </div>
          </section>

                  </div>
                </details>
              </div>}
            </div>
          </section>
        </div>
      </div>
      <Modal.Backdrop isOpen={regenerateOpen} onOpenChange={setRegenerateOpen}>
        <Modal.Container size="sm"><Modal.Dialog><Modal.CloseTrigger /><Modal.Header><Modal.Heading>重新生成访问令牌</Modal.Heading></Modal.Header>
          <Modal.Body><Typography>旧令牌会立即失效，所有已接入的应用与客户端都需更新配置。是否继续？</Typography></Modal.Body>
          <Modal.Footer><Button slot="close" variant="secondary">取消</Button><Button variant="danger" isPending={regenerating} onPress={generateKey}>重新生成</Button></Modal.Footer>
        </Modal.Dialog></Modal.Container>
      </Modal.Backdrop>
    </div>
  );
}
