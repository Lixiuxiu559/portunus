import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';

// 系统"减弱动态效果"时跳过 FLIP，直接呈现/卸载（可读性与 reduced-motion 同思路）
const REDUCED =
  typeof window !== 'undefined' &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// 收起动画时长（与 --dur-collapse 对齐），到点通知父组件卸载，不依赖 transitionend 保证正确性
const EXIT_MS = 260;

/**
 * 卡片展开编辑器：编辑界面从触发卡片原位"生长"出来，而非弹窗。
 * 由父组件在「有编辑对象」时条件渲染；挂载即 FLIP，卸载即收起完成。
 *
 * - 挂载后用 FLIP 让面板从 triggerRect 的 translate+scale 过渡到终态
 * - 只动 transform / border-radius / box-shadow（GPU 合成，无布局抖动）
 * - 头部复用卡片同一套 icon+标题+状态，转场中视觉连续；body 内容延迟淡入
 * - 点击 scrim / Esc / 头部 X / children 内的 requestClose 均反向收起后触发 onExited
 *
 * children 为函数，接收 { requestClose }：关闭必须走 requestClose 才能有反向动画，
 * 父组件在 onExited 里清掉编辑对象以卸载本组件。
 */
export default function ExpandEditor({
  triggerRect,
  onExited,
  title,
  status,
  children,
  width = 720,
  ariaLabel = '编辑',
}) {
  const [opened, setOpened] = useState(false);
  const [closing, setClosing] = useState(false);
  const panelRef = useRef(null);
  const startRef = useRef(triggerRect);
  const closingRef = useRef(false);
  const exitTimer = useRef(null);

  // FLIP 进入：面板已按终态布局渲染，量出终态 rect → 倒回卡片 rect → 过渡到终态
  useLayoutEffect(() => {
    const panel = panelRef.current;
    const start = startRef.current;

    if (REDUCED || !start) {
      setOpened(true);
      return;
    }

    // 先复位到终态布局再测量：StrictMode 下 effect 会双跑，上一轮可能残留
    // 卡片位 transform，直接量会把终态错量成卡片尺寸，FLIP 退化成恒等变换。
    panel.style.transition = 'none';
    panel.style.transform = 'none';
    panel.style.borderRadius = '';
    panel.style.boxShadow = '';

    const end = panel.getBoundingClientRect();
    const dx = start.left - end.left;
    const dy = start.top - end.top;
    const sx = start.width / end.width;
    const sy = start.height / end.height;

    panel.style.transformOrigin = 'top left';
    panel.style.transform = `translate(${dx}px, ${dy}px) scale(${sx}, ${sy})`;
    panel.style.borderRadius = start.borderRadius || 'var(--expand-radius-end)';
    panel.style.boxShadow = 'var(--expand-shadow-start)';

    void panel.offsetWidth; // 强制提交起始态，紧接着切换到终态触发过渡

    panel.style.transition =
      'transform var(--dur-expand) var(--ease-expand), border-radius var(--dur-expand) var(--ease-expand), box-shadow var(--dur-expand) var(--ease-expand)';
    panel.style.transform = 'none';
    panel.style.borderRadius = '';
    panel.style.boxShadow = '';
    setOpened(true); // scrim 淡入 + body 延迟淡入（body 自带 transition-delay）
  }, []);

  const requestClose = () => {
    if (closingRef.current) return;
    closingRef.current = true;
    setClosing(true);
    setOpened(false);

    const panel = panelRef.current;
    const start = startRef.current;
    if (REDUCED || !panel || !start) {
      onExited();
      return;
    }

    const end = panel.getBoundingClientRect();
    const dx = start.left - end.left;
    const dy = start.top - end.top;
    const sx = start.width / end.width;
    const sy = start.height / end.height;

    panel.style.transition =
      'transform var(--dur-collapse) var(--ease-collapse), border-radius var(--dur-collapse) var(--ease-collapse), box-shadow var(--dur-collapse) var(--ease-collapse)';
    panel.style.transform = `translate(${dx}px, ${dy}px) scale(${sx}, ${sy})`;
    panel.style.borderRadius = start.borderRadius || 'var(--expand-radius-end)';
    panel.style.boxShadow = 'var(--expand-shadow-start)';

    exitTimer.current = setTimeout(onExited, EXIT_MS);
  };

  // Esc 关闭
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') requestClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 锁背景滚动，卸载时还原并清理未决的收起计时器
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prev;
      clearTimeout(exitTimer.current);
    };
  }, []);

  return createPortal(
    <div className={`expand-editor${opened ? ' is-open' : ''}${closing ? ' is-closing' : ''}`}>
      <div className="expand-editor__scrim" onClick={requestClose} aria-hidden="true" />
      <div
        ref={panelRef}
        className="expand-editor__panel"
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel}
        tabIndex={-1}
        style={{ '--panel-w': `${width}px` }}
      >
        <div className="expand-editor__head">
          <div className="expand-editor__identity">{title}</div>
          {status && <div className="expand-editor__status">{status}</div>}
          <button type="button" className="expand-editor__close" onClick={requestClose} aria-label="关闭">
            <X className="size-4" />
          </button>
        </div>
        <div className="expand-editor__body">{children({ requestClose })}</div>
      </div>
    </div>,
    document.body,
  );
}
