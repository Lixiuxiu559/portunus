import { useState } from 'react';
import { Button } from '@heroui/react';
import { Eye, EyeOff } from 'lucide-react';
import ConfirmModal from '../../components/ConfirmModal';
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
  regenerating,
  onResetToken,
  networkMode,
  savingMode,
  onSaveNetworkMode,
}) {
  const [confirmOpen, setConfirmOpen] = useState(false);
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

        <div className="service-config-values">
          <CopyValue label="网关地址" value={guide.baseUrl} revealKey={revealKey} />
          <CopyValue label="OpenAI Base URL · 含 /v1" value={guide.openAIBaseUrl} revealKey={revealKey} />
          <CopyValue
            label="访问令牌"
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
            <div className="space-y-2 pt-3 leading-6"><p>网关地址供本机调用。其他设备请使用本机局域网 IP 和端口 {new URL(guide.baseUrl).port || '80'}，不能使用 127.0.0.1 或 0.0.0.0 作为目标地址。</p><p>切换范围会自动重启服务，进行中的请求可能中断。管理操作仅限本机，服务请求始终需要令牌。</p><p>重置令牌后旧令牌立即失效，所有调用方都需更新。上游 API Key 只在上游渠道中保存。</p><p>应用运行期间提供服务，退出后停止。分组名称就是客户端 model，调整组内上游无需修改客户端。</p></div>
          </details>
          <Button size="sm" variant="tertiary" isDisabled={loading || loadError} isPending={regenerating} onPress={() => (apiKey ? setConfirmOpen(true) : onResetToken())}>{apiKey ? '重置令牌' : '生成令牌'}</Button>
        </div>
      </aside>

      <ConfirmModal
        isOpen={confirmOpen}
        onOpenChange={setConfirmOpen}
        title="重新生成访问令牌"
        description="旧令牌会立即失效，所有已接入的应用与客户端都需更新配置。是否继续？"
        confirmText="重新生成"
        pendingText="正在生成…"
        onConfirm={onResetToken}
      />
    </>
  );
}
