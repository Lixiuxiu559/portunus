/**
 * 统一图标按钮：覆盖卡片/表格内的同步、编辑、删除、复制等次级操作。
 * 统一击区尺寸、圆角与 hover 语义色，避免各页各自手写造成不一致。
 *
 * - 默认使用主色 accent 的 hover 强调
 * - danger 用于删除、移除等破坏性操作，改用 danger 语义色
 * - size: md=28px（常规），sm=24px（紧凑行内，如分组项移除）
 */
export default function IconButton({
  label,
  onClick,
  danger = false,
  disabled = false,
  size = 'md',
  className = '',
  children,
  ...rest
}) {
  const base =
    'flex items-center justify-center rounded-lg text-muted transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed';
  const color = danger
    ? 'hover:bg-danger/10 hover:text-danger'
    : 'hover:bg-accent/10 hover:text-accent';
  const dim = size === 'sm' ? 'size-6' : 'size-7';

  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      disabled={disabled}
      className={`${base} ${color} ${dim} ${className}`}
      {...rest}
    >
      {children}
    </button>
  );
}