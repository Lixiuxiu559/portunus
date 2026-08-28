/**
 * 通用数据表格组件（基于 HeroUI Table）
 * - dataSource: 数据数组
 * - columns: 列配置 [{ title, dataIndex, key?, render?, width?, isRowHeader?, copy? }]
 * - loading?: 加载状态（首次加载显示 Spinner，有数据时仅叠加半透明遮罩）
 * - emptyText?: 空态文案
 *
 * copy 字段：设置为 true 时，单元格内容旁会显示复制图标，hover 时可见，点击可复制文本到剪贴板。
 */
import { useState, useCallback, memo, type ReactNode } from 'react';
import { Table, Spinner } from '@heroui/react';
import { Copy, Check } from 'lucide-react';

/* ──────────── 类型定义 ──────────── */

export interface ColumnType<T = Record<string, unknown>> {
  /** 列标题 */
  title: string;
  /** 数据字段名 */
  dataIndex: string;
  /** 列唯一 key，默认取 dataIndex */
  key?: string;
  /** 自定义渲染，参数依次为 (单元格值, 行数据, 行索引) */
  render?: (value: unknown, record: T, rowIndex: number) => ReactNode;
  /** 列宽 */
  width?: string;
  /** 是否为行标题列 */
  isRowHeader?: boolean;
  /** 是否显示复制按钮（hover 可见，点击复制单元格文本） */
  copy?: boolean;
}

export interface DataTableProps<T = Record<string, unknown>> {
  /** 数据源 */
  dataSource?: T[];
  /** 列配置 */
  columns?: ColumnType<T>[];
  /** 加载状态（首次加载显示 Spinner） */
  loading?: boolean;
  /** 刷新状态（仅在表格右上角显示旋转图标） */
  refreshing?: boolean;
  /** 空态文案 */
  emptyText?: string;
  /** 表格 aria-label */
  ariaLabel?: string;
  /** 额外 className */
  className?: string;
}

/* ──────────── 复制按钮子组件 ──────────── */

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 降级方案
      const input = document.createElement('input');
      input.value = text;
      document.body.appendChild(input);
      input.select();
      document.execCommand('copy');
      document.body.removeChild(input);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  }, [text]);

  return (
    <span className="inline-flex items-center gap-1">
      <button
        type="button"
        onClick={handleCopy}
        className="inline-flex size-5 items-center justify-center rounded text-muted/0 transition-all hover:text-muted cursor-pointer group-hover/cell:text-muted"
        aria-label="复制"
      >
        {copied ? (
          <Check className="size-3.5 text-success" />
        ) : (
          <Copy className="size-3.5" />
        )}
      </button>
    </span>
  );
}

/* ──────────── DataTable 主组件 ──────────── */

export default memo(function DataTable<T extends Record<string, unknown>>({
  dataSource = [],
  columns = [],
  loading = false,
  refreshing = false,
  emptyText = '暂无数据',
  ariaLabel = '数据表格',
  className = '',
}: DataTableProps<T>) {
  const hasData = dataSource.length > 0;

  // 首次加载（无数据）：居中显示 Spinner
  if (loading && !hasData) {
    return (
      <div className="flex items-center justify-center flex-1">
        <Spinner />
      </div>
    );
  }

  // 空态
  if (!hasData) {
    return (
      <div className="flex items-center justify-center flex-1 text-muted">
        {emptyText}
      </div>
    );
  }

  return (
    <div className={`relative flex min-h-0 flex-col overflow-hidden rounded-2xl border border-separator bg-surface/40 backdrop-blur-xl shadow-[0_8px_30px_rgba(0,0,0,0.12)] dark:shadow-[0_8px_30px_rgba(0,0,0,0.35)] ${className}`}>
      {/* 刷新指示器：表格正中间显示旋转图标 */}
      {refreshing && (
        <div className="absolute inset-0 z-10 flex items-center justify-center bg-surface/20 backdrop-blur-[1px] rounded-xl">
          <Spinner size="sm" className="text-primary" />
        </div>
      )}

      <Table variant="secondary" className="h-full grid-rows-[minmax(0,1fr)] text-foreground">
        <Table.ScrollContainer className="h-full min-h-0 overflow-auto">
          <Table.Content
            aria-label={ariaLabel}
            className="min-w-[600px] px-0"
            aria-hidden={false}
          >
            <Table.Header className="sticky top-0 z-10 shadow-[0_1px_0_var(--border)]">
              {columns.map((col) => (
                <Table.Column
                  key={col.key || col.dataIndex}
                  isRowHeader={col.isRowHeader}
                  style={col.width ? { width: col.width } : undefined}
                >
                  {col.title}
                </Table.Column>
              ))}
            </Table.Header>
            <Table.Body>
              {dataSource.map((row, rowIndex) => (
                <Table.Row
                  key={(row as Record<string, unknown>).id as string ?? rowIndex}
                  className="text-foreground"
                >
                  {columns.map((col) => {
                    const cellValue = row[col.dataIndex];
                    const display: ReactNode = col.render
                      ? col.render(cellValue, row, rowIndex)
                      : (cellValue as ReactNode);

                    return (
                      <Table.Cell key={col.key || col.dataIndex}>
                        {col.copy ? (
                          <span className="group/cell inline-flex items-center gap-1">
                            <span>{display}</span>
                            <CopyButton text={String(cellValue ?? '')} />
                          </span>
                        ) : (
                          display
                        )}
                      </Table.Cell>
                    );
                  })}
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Content>
        </Table.ScrollContainer>
      </Table>
    </div>
  );
});
