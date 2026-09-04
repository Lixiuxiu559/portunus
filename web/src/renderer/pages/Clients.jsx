import { useEffect, useState } from 'react';
import { Typography, Button, Card, TextField, Input, Select, ListBox, toast } from '@heroui/react';
import { RefreshCw, Save, FolderInput, Info } from 'lucide-react';
import { listAPIKeys } from '../api/apikey';

// 是否在 Electron 桌面端（有 preload 注入 clientConfig 桥）；纯浏览器开发时不存在。
const hasElectron = typeof window !== 'undefined' && !!window.clientConfig;

function maskToken(v) {
  if (!v) return '';
  if (typeof v !== 'string') return String(v);
  if (v.length <= 8) return v;
  return `${v.slice(0, 4)}...${v.slice(-4)}`;
}

/**
 * 单个客户端配置区块：展示目标文件与现状，选择 base_url + 令牌，写入 / 读回。
 * 两种客户端仅字段描述不同，结构一致，故抽成复用组件。
 */
function ClientSection({
  title,
  description,
  filePath,
  live,
  baseUrl,
  baseUrlNote,
  onBaseUrlChange,
  keyId,
  onKeyIdChange,
  apiKeys,
  onWrite,
  onRead,
  writing,
}) {
  const liveBaseUrl = live?.env?.ANTHROPIC_BASE_URL ?? live?.model_providers?.portunus?.base_url ?? '';
  const liveToken = live?.env?.ANTHROPIC_AUTH_TOKEN ?? live?.model_providers?.portunus?.experimental_bearer_token ?? '';

  return (
    <Card className="gap-4 p-5">
      <Card.Header className="p-0">
        <div className="flex items-center justify-between w-full">
          <Typography className="font-medium">{title}</Typography>
          <Button variant="tertiary" size="sm" onPress={onRead}>
            <RefreshCw className="size-4" />
            读取现状
          </Button>
        </div>
      </Card.Header>
      <Card.Content className="p-0">
        <div className="flex flex-col gap-4">
          <Typography type="body-sm" className="text-muted">{description}</Typography>

          {/* 目标文件 */}
          <div className="flex items-center gap-2">
            <FolderInput className="size-4 text-muted shrink-0" />
            <Typography type="body-xs" className="text-muted font-mono break-all">{filePath}</Typography>
          </div>

          {/* 现状回显 */}
          {(liveBaseUrl || liveToken) && (
            <div className="flex flex-col gap-1 rounded-lg bg-default-100/60 p-3">
              <Typography type="body-xs" className="text-muted">当前已配置</Typography>
              <Typography type="body-xs" className="font-mono break-all">base_url: {liveBaseUrl || '—'}</Typography>
              <Typography type="body-xs" className="font-mono break-all">token: {maskToken(liveToken) || '—'}</Typography>
            </div>
          )}

          {/* base_url */}
          <TextField size="sm" label="base_url" value={baseUrl} onChange={onBaseUrlChange} placeholder="https://your-portunus-host">
            <Input />
          </TextField>
          <Typography type="body-xs" className="text-muted -mt-3">{baseUrlNote}</Typography>

          {/* 令牌选择 */}
          <Select
            size="sm"
            label="令牌"
            placeholder={apiKeys.length ? '请选择令牌' : '暂无令牌，请先到「设置」创建'}
            selectedKey={keyId}
            onSelectionChange={onKeyIdChange}
          >
            <Select.Trigger>
              <Select.Value />
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox>
                {apiKeys.map((k) => (
                  <ListBox.Item key={String(k.id)} id={String(k.id)} textValue={k.name}>
                    <div className="flex items-center justify-between gap-2 w-full">
                      <span>{k.name}</span>
                      <span className="text-muted font-mono text-xs">{maskToken(k.key)}</span>
                    </div>
                    <ListBox.ItemIndicator />
                  </ListBox.Item>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>

          <Button variant="primary" size="sm" onPress={onWrite} isPending={writing}>
            <Save className="size-4" />
            写入配置
          </Button>
        </div>
      </Card.Content>
    </Card>
  );
}

export default function Clients() {
  const serverUrl = window.api?.getServerUrl?.() || 'http://localhost:3061';

  const [paths, setPaths] = useState({ claude: '', codex: '' });
  const [apiKeys, setApiKeys] = useState([]);

  const [claudeBaseUrl, setClaudeBaseUrl] = useState(serverUrl);
  const [codexBaseUrl, setCodexBaseUrl] = useState(`${serverUrl.replace(/\/+$/, '')}/v1`);
  const [claudeKeyId, setClaudeKeyId] = useState(null);
  const [codexKeyId, setCodexKeyId] = useState(null);

  const [claudeLive, setClaudeLive] = useState(null);
  const [codexLive, setCodexLive] = useState(null);
  const [writing, setWriting] = useState({ claude: false, codex: false });

  const loadLive = async () => {
    if (!hasElectron) return;
    const [c, x] = await Promise.all([
      window.clientConfig.readClaude(),
      window.clientConfig.readCodex(),
    ]);
    if (c.ok) setClaudeLive(c.data);
    if (x.ok) setCodexLive(x.data);
  };

  useEffect(() => {
    if (!hasElectron) return;
    window.clientConfig.getPaths().then((r) => {
      if (r.ok) setPaths(r.data);
    });
    listAPIKeys()
      .then((ks) => setApiKeys(Array.isArray(ks) ? ks : []))
      .catch(() => {});
    loadLive();
  }, []);

  const handleWrite = async (which) => {
    const keyId = which === 'claude' ? claudeKeyId : codexKeyId;
    const baseUrl = which === 'claude' ? claudeBaseUrl : codexBaseUrl;
    const key = apiKeys.find((k) => String(k.id) === String(keyId));
    if (!key) {
      toast.error('请先选择令牌');
      return;
    }
    if (!baseUrl?.trim()) {
      toast.error('base_url 不能为空');
      return;
    }
    setWriting((s) => ({ ...s, [which]: true }));
    try {
      const fn = which === 'claude' ? window.clientConfig.writeClaude : window.clientConfig.writeCodex;
      const r = await fn({ baseUrl: baseUrl.trim(), apiKey: key.key });
      if (r.ok) {
        toast.success('配置已写入');
        await loadLive();
      } else {
        toast.danger(r.error || '写入失败');
      }
    } catch (e) {
      toast.danger(e?.message || '写入失败');
    } finally {
      setWriting((s) => ({ ...s, [which]: false }));
    }
  };

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
    <div className="space-y-6">
      <div>
        <Typography type="h2">客户端配置</Typography>
        <Typography type="body-sm" className="text-muted mt-1">
          把 Portunus 配成 Claude Code / Codex 的上游；合并写入、不覆盖你现有配置，首次写入前自动备份为 <span className="font-mono">.portunus.bak</span>。
        </Typography>
      </div>

      <ClientSection
        title="Claude Code CLI"
        description="写入 ~/.claude/settings.json 的 env（ANTHROPIC_BASE_URL + ANTHROPIC_AUTH_TOKEN），对应网关 /v1/messages。"
        filePath={paths.claude}
        live={claudeLive}
        baseUrl={claudeBaseUrl}
        baseUrlNote="指向 Portunus 根地址（不带 /v1），Claude Code 会自动拼 /v1/messages。"
        onBaseUrlChange={setClaudeBaseUrl}
        keyId={claudeKeyId}
        onKeyIdChange={setClaudeKeyId}
        apiKeys={apiKeys}
        onWrite={() => handleWrite('claude')}
        onRead={loadLive}
        writing={writing.claude}
      />

      <ClientSection
        title="Codex"
        description="写入 ~/.codex/config.toml 的 [model_providers.portunus] 段并激活，对应网关 /v1/responses；不碰 auth.json。"
        filePath={paths.codex}
        live={codexLive}
        baseUrl={codexBaseUrl}
        baseUrlNote="指向 Portunus 的 /v1（带 /v1），Codex Responses API 会自动拼 /responses。"
        onBaseUrlChange={setCodexBaseUrl}
        keyId={codexKeyId}
        onKeyIdChange={setCodexKeyId}
        apiKeys={apiKeys}
        onWrite={() => handleWrite('codex')}
        onRead={loadLive}
        writing={writing.codex}
      />
    </div>
  );
}