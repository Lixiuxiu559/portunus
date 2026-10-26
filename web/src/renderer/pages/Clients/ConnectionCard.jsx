import { Button, Select, ListBox } from '@heroui/react';
import { Eye, EyeOff } from 'lucide-react';
import { CopyValue } from './shared';
import PageSkeleton from '../../components/PageSkeleton';

/** 连接配置卡：网关地址、令牌、访问范围等全局信息；模型分组已移到接入示例。 */
export default function ConnectionCard({
  guide,
  apiKey,
  showKey,
  onToggleKey,
  loading,
  refreshing,
  revealKey,
  loadError,
  apiKeys,
  selectedKey,
  onSelectKey,
  keyBusy,
  networkMode,
  savingMode,
  onSaveNetworkMode,
}) {
  const activeKeys = apiKeys.filter((key) => !key.revoked && !key.deleted_at);
  if (loading) return <aside className="service-connection-panel rounded-2xl bg-surface/70 p-5"><PageSkeleton variant="connection" /></aside>;

  return (
    <>
      <aside className="service-connection-panel rounded-2xl bg-surface/70 p-5" aria-label="连接配置" aria-busy={refreshing}>
        {refreshing && <span className="service-refresh-bar" aria-hidden="true" />}
        <div className="mb-4 flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
            <h2 className="text-sm font-semibold">连接配置</h2>
            <p className="service-config-hint">Claude Code 等 Anthropic 协议客户端用网关地址；OpenAI 兼容客户端用含 /v1 的地址。</p>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted">访问范围</span>
            {window.api?.setNetworkMode ? (
              <div className="service-network-segment" role="group" aria-label="服务访问范围" aria-busy={savingMode}>
                {[['local', '仅本机'], ['lan', '局域网']].map(([mode, label]) => (
                  <button
                    type="button"
                    key={mode}
                    className="service-network-option"
                    aria-pressed={networkMode === mode}
                    disabled={savingMode || networkMode === null}
                    onClick={() => onSaveNetworkMode(mode)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            ) : (
              <span className="text-xs text-muted">{networkMode === 'lan' ? '局域网' : networkMode === 'local' ? '仅本机' : window.api?.getNetworkMode ? '正在读取' : '由启动配置决定'}</span>
            )}
          </div>
        </div>

        <div className="service-guide-key-select">
          <span className="service-group-label">接入使用的 API Key</span>
          <Select
            aria-label="选择 API Key"
            placeholder="暂无有效 API Key"
            selectedKey={selectedKey ? String(selectedKey.id) : null}
            isDisabled={keyBusy || loadError || !activeKeys.length}
            onSelectionChange={(id) => { if (id !== null) onSelectKey(Number(id)); }}
            className="service-group-select min-w-0"
          >
            <Select.Trigger aria-label="选择 API Key" aria-labelledby={null} className="service-group-trigger w-full min-w-0 rounded-xl">
              <Select.Value className="min-w-0 flex-1 truncate text-left" />
              <Select.Indicator className="size-4 shrink-0 text-muted" />
            </Select.Trigger>
            <Select.Popover className="service-group-popover">
              <ListBox aria-label="可接入的 API Key">
                {activeKeys.map((key) => <ListBox.Item key={key.id} id={String(key.id)} textValue={key.name} className="min-w-0 py-2.5">
                  <span className="min-w-0 flex-1 truncate" title={key.name}>{key.name}</span>
                  <ListBox.ItemIndicator />
                </ListBox.Item>)}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
        <div className="service-config-values">
          <CopyValue label="网关地址" value={guide.baseUrl} revealKey={revealKey} />
          <CopyValue label="OpenAI Base URL · 含 /v1" value={guide.openAIBaseUrl} revealKey={revealKey} />
          <CopyValue
            label={selectedKey ? `访问令牌 · ${selectedKey.name}` : '访问令牌'}
            value={apiKey}
            display={loading ? '加载中…' : apiKey ? (showKey ? apiKey : '••••••••••••••••') : '尚未生成'}
            revealKey={revealKey}
            disabled={!apiKey || loading}
            wrap={showKey}
            actions={<Button isIconOnly size="sm" variant="tertiary" aria-label={showKey ? '隐藏访问令牌' : '显示访问令牌'} isDisabled={!apiKey} onPress={onToggleKey}>{showKey ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}</Button>}
          />
        </div>
        {loadError && <p role="alert" className="mt-3 text-xs text-danger">连接信息加载失败，请刷新重试。</p>}

        <div className="service-config-footer">
          <details className="service-disclosure text-xs text-muted">
            <summary>访问与令牌说明</summary>
            <div className="space-y-2 pt-3 leading-6"><p>网关地址供本机调用。其他设备请使用本机局域网 IP 和端口 {new URL(guide.baseUrl).port || '80'}，不能使用 127.0.0.1 或 0.0.0.0 作为目标地址。</p><p>切换范围会自动重启服务，进行中的请求可能中断。管理操作仅限本机，服务请求始终需要令牌。</p><p>为不同工具创建独立的命名 API Key，可在调用记录按 Key 筛选用量。重置、禁用或删除只影响使用该 Key 的工具；禁用后可恢复启用。上游 API Key 只在上游渠道中保存。</p><p>应用运行期间提供服务，退出后停止。分组名称就是客户端 model，调整组内上游无需修改客户端。</p></div>
          </details>
        </div>
      </aside>

    </>
  );
}
