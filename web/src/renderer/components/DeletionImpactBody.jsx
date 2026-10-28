import { Button, Spinner, Typography } from '@heroui/react';
import { AlertTriangle } from 'lucide-react';

/**
 * 删除弹窗共用的影响展示区。
 * 加载中禁止确认由调用方根据 state.status 控制；这里只负责把状态如实呈现，
 * 影响未知（error）时给出重试入口，而不是显示成“没有引用”。
 */
export default function DeletionImpactBody({ state, view, onRetry }) {
  if (state.status === 'loading') {
    return (
      <div role="status" className="flex items-center gap-2 text-sm text-muted">
        <Spinner size="sm" />正在检查引用影响…
      </div>
    );
  }

  if (state.status === 'error') {
    return (
      <div role="alert" className="flex flex-col items-start gap-2 rounded-xl border border-danger/30 bg-danger/5 p-3 text-sm">
        <span className="flex items-center gap-1.5 font-medium text-danger">
          <AlertTriangle className="size-4 shrink-0" />无法确认删除影响，已阻止删除
        </span>
        {state.error && <span className="break-words text-muted">{state.error}</span>}
        <Button size="sm" variant="secondary" onPress={onRetry}>重试预览</Button>
      </div>
    );
  }

  if (!view) return null;

  return (
    <div className="flex flex-col gap-3">
      {view.channel && (
        <div className="flex flex-col gap-1 rounded-xl border border-separator bg-surface-secondary/40 p-3 text-sm">
          <span>{view.channel.modelCountText}</span>
          <span className={view.channel.hasQuotaSource ? 'text-warning' : 'text-muted'}>
            {view.channel.quotaText}
          </span>
        </div>
      )}
      <Typography className="text-sm font-medium">{view.headline}</Typography>
      {view.noReferences ? (
        <Typography color="muted" className="text-sm">没有分组引用，删除后不会影响任何分组成员。</Typography>
      ) : (
        <ul className="flex flex-col gap-2">
          {view.groups.map((group) => (
            <li key={group.key} className="rounded-xl border border-separator bg-surface-secondary/40 p-3">
              <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                <span className="font-medium">{group.name}</span>
                <span className="text-xs text-muted">{group.strategyLabel}</span>
              </div>
              <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
                <span>移除 {group.removedCount} 个成员</span>
                {group.clearsActive && <span className="text-warning">手动选定项将被清除</span>}
                {group.becomesEmpty && <span className="text-danger">该分组将变为空分组</span>}
              </div>
            </li>
          ))}
        </ul>
      )}
      <Typography color="muted" className="text-xs">{view.note}</Typography>
      {state.deleteError && (
        <p role="alert" className="text-sm text-danger">删除失败：{state.deleteError}</p>
      )}
    </div>
  );
}
