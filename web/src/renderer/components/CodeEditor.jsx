import { useMemo, useRef, useState, useEffect } from 'react';
import { RotateCcw, RefreshCw, Save, AlignLeft } from 'lucide-react';
import { Button, toast } from '@heroui/react';
import { highlight, validate, format } from '../utils/configHighlight';

/**
 * 源码编辑器：textarea（透明文字）叠在 pre 高亮层上，左列行号。
 * 高度随内容自适应（无内部滚动条），横向超宽时 textarea 自身滚动并把
 * scrollLeft 同步给高亮层；支持「当前 / 备份」查看与格式化。
 */
export default function CodeEditor({
  lang,
  fileName,
  text,
  backupText,
  onTextChange,
  dirty,
  savedAt,
  canRollback,
  active = true,
  onSave,
  onRollback,
  onReread,
  saving,
}) {
  const [view, setView] = useState('current');
  const taRef = useRef(null);
  const codeWrapRef = useRef(null);

  const hasBackup = backupText != null && backupText !== '';
  const isBackup = view === 'backup';
  const displayText = isBackup ? backupText || '' : text;

  const html = useMemo(() => highlight(displayText, lang), [displayText, lang]);
  const v = useMemo(() => validate(displayText, lang), [displayText, lang]);
  const lineCount = displayText.split('\n').length;

  // textarea 高度自适应内容（无内部滚动）；active 用于隐藏面板恢复显示时重算
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = 'auto';
    ta.style.height = `${ta.scrollHeight}px`;
  }, [displayText, active]);

  // 横向滚动时把 scrollLeft 同步给高亮层，保持文字与光标对齐
  const handleScroll = () => {
    const ta = taRef.current;
    if (ta && codeWrapRef.current) codeWrapRef.current.scrollLeft = ta.scrollLeft;
  };

  const onChange = (e) => {
    if (isBackup) return;
    onTextChange(e.target.value);
  };

  const onKeyDown = (e) => {
    if (isBackup) return;
    if (e.key === 'Tab') {
      e.preventDefault();
      const ta = taRef.current;
      const s = ta.selectionStart;
      const en = ta.selectionEnd;
      onTextChange(text.slice(0, s) + '  ' + text.slice(en));
      requestAnimationFrame(() => {
        ta.selectionStart = ta.selectionEnd = s + 2;
      });
      return;
    }
    if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
      e.preventDefault();
      // 与保存按钮同一守卫：无改动或保存中不重复触发
      if (!dirty || saving) return;
      onSave();
    }
  };

  const handleFormat = () => {
    try {
      const next = format(text, lang);
      onTextChange(next);
      toast.success('已格式化');
    } catch (e) {
      toast.danger((e && e.message) || '格式化失败：内容语法错误');
    }
  };

  return (
    <div>
      <div className={`editor ${dirty ? 'dirty' : ''}`}>
        <div className="editor-head">
          <span className="lang">{fileName}</span>
          <div className="view-tabs" role="tablist" aria-label="查看模式">
            <button
              type="button"
              role="tab"
              aria-selected={!isBackup}
              className={`view-tab ${!isBackup ? 'active' : ''}`}
              onClick={() => setView('current')}
            >
              当前
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={isBackup}
              className={`view-tab ${isBackup ? 'active' : ''}`}
              onClick={() => setView('backup')}
              disabled={!hasBackup}
              title={hasBackup ? '查看接入 Portunus 之前的备份' : '暂无备份'}
            >
              备份
            </button>
          </div>
          <span className={`stat ${v.ok ? 'ok' : 'err'}`}>
            {isBackup ? '备份 · ' : ''}
            {v.msg}
          </span>
        </div>
        <div className="editor-body">
          <div className="gutter">
            {Array.from({ length: lineCount }, (_, i) => (
              <div key={i}>{i + 1}</div>
            ))}
          </div>
          <div className="code-wrap" ref={codeWrapRef}>
            <pre dangerouslySetInnerHTML={{ __html: html + '\n' }} />
          </div>
          <textarea
            ref={taRef}
            value={displayText}
            onChange={onChange}
            onKeyDown={onKeyDown}
            onScroll={handleScroll}
            readOnly={isBackup}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            aria-label={isBackup ? '备份配置预览' : '配置编辑区'}
          />
        </div>
        <div className="editor-foot">
          <Button
            variant="danger-soft"
            size="sm"
            onPress={onRollback}
            isDisabled={!canRollback}
            title={canRollback ? '恢复为接入 Portunus 之前的配置' : '暂无备份'}
          >
            <RotateCcw className="size-4" />
            从备份回滚
          </Button>
          <span className={dirty ? 'unsaved' : 'savetime'}>
            {isBackup ? (
              <>
                {/* 备份只读视图也保留 dirty 指示，防止查看备份时遗忘仍挂起的改动 */}
                <span className="dot" />
                {`只读预览${dirty ? ' · 仍有未保存改动' : ''}`}
              </>
            ) : dirty ? (
              <>
                <span className="dot" />
                未保存改动
              </>
            ) : (
              `已保存 ${savedAt}`
            )}
          </span>
        </div>
      </div>

      <div className="flex items-center gap-2 mt-3">
        <Button variant="tertiary" size="sm" onPress={onReread}>
          <RefreshCw className="size-4" />
          重新读取
        </Button>
        <Button variant="tertiary" size="sm" onPress={handleFormat} isDisabled={isBackup}>
          <AlignLeft className="size-4" />
          格式化
        </Button>
        <span className="text-xs text-muted ml-auto">⌘ / Ctrl + S</span>
        <Button variant="primary" size="sm" onPress={onSave} isPending={saving} isDisabled={isBackup || !dirty}>
          <Save className="size-4" />
          保存
        </Button>
      </div>
    </div>
  );
}