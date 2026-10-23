// 结构占位立即出现，动态反馈延后显示，快请求不会闪一次转圈。
export default function PageSkeleton({ variant = 'channels' }) {
  if (variant === 'connection') return (
    <div className="skeleton-connection" role="status" aria-label="正在加载连接配置" aria-busy="true">
      <span className="sr-only">正在加载连接配置</span>
      <div className="skeleton-heading" aria-hidden="true"><span className="skeleton-block skeleton-title" /></div>
      <div className="service-config-values" aria-hidden="true">{[0, 1, 2].map((i) => <div key={i}><span className="skeleton-block skeleton-title" /><span className="skeleton-block skeleton-line" /></div>)}</div>
    </div>
  );
  if (variant === 'content') return (
    <div className="skeleton-content" role="status" aria-label="正在加载接入内容" aria-busy="true">
      <span className="sr-only">正在加载接入内容</span>
      <div className="skeleton-content-fields" aria-hidden="true"><span className="skeleton-block skeleton-title" /><span className="skeleton-block skeleton-field" /></div>
      <div className="skeleton-content-fields" aria-hidden="true"><span className="skeleton-block skeleton-title" /><span className="skeleton-block skeleton-line" /><span className="skeleton-block skeleton-code" /></div>
      <div className="skeleton-content-fields" aria-hidden="true"><span className="skeleton-block skeleton-title" /><span className="skeleton-block skeleton-line" /><span className="skeleton-block skeleton-code" /></div>
    </div>
  );
  const table = variant === 'table';
  return (
    <div className={`page-skeleton page-skeleton--${variant}`} role="status" aria-label="正在加载内容" aria-busy="true">
      <span className="sr-only">正在加载内容</span>
      <div className={table ? 'skeleton-table' : `grid grid-cols-1 md:grid-cols-2 ${variant === 'groups' ? '' : 'xl:grid-cols-3'} gap-4`} aria-hidden="true">
        {Array.from({ length: table ? 8 : 6 }, (_, i) => (
          <div key={i} className={table ? 'skeleton-row' : 'skeleton-card'}>
            <div className="skeleton-heading"><span className="skeleton-block skeleton-icon" /><span className="skeleton-block skeleton-title" /></div>
            <span className="skeleton-block skeleton-line" />
            <span className="skeleton-block skeleton-line skeleton-line--short" />
            {variant === 'groups' && <><span className="skeleton-block skeleton-member" /><span className="skeleton-block skeleton-member" /><span className="skeleton-block skeleton-member" /></>}
          </div>
        ))}
      </div>
    </div>
  );
}
