import { useMemo, useRef } from 'react';
import { Button } from '@heroui/react';
import { highlight, validate } from '../utils/configHighlight';

/**
 * 源码编辑器：textarea（透明文字）叠在 pre 高亮层上，左列行号。
 * 复刻 client-config-editor.html 的叠层技法。
 */
export default function CodeEditor({
  lang,
  fileName,
  text,
  onTextChange,
  dirty,
  savedAt,
  canRollback,
  onSave,
  onRollback,
  onReread,
  saving,
}) {
  const taRef = useRef(null);
  const gutterRef = useRef(null);
  const codeRef = useRef(null);

  const html = useMemo(() => highlight(text, lang), [text, lang]);
  const v = useMemo(() => validate(text, lang), [text, lang]);
  const lineCount = text.split('\n').length;

  const syncScroll = () => {
    const ta = taRef.current;
    if (!ta) return;
    if (codeRef.current) {
      codeRef.current.style.transform = `translate(${-ta.scrollLeft}px, ${-ta.scrollTop}px)`;
    }
    if (gutterRef.current) {
      gutterRef.current.style.transform = `translateY(${-ta.scrollTop}px)`;
    }
  };

  const onKeyDown = (e) => {
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
      onSave();
    }
  };

  return (
    <div>
      <div className={`cc-editor ${dirty ? 'dirty' : ''}`}>
        <div className="cc-editor-head">
          <span className="lang">{fileName}</span>
          <span className={`cc-editor-stat ${v.ok ? 'ok' : 'err'}`}>{v.msg}</span>
        </div>
        <div className="cc-editor-body">
          <div className="cc-gutter" ref={gutterRef}>
            {Array.from({ length: lineCount }, (_, i) => (
              <div key={i}>{i + 1}</div>
            ))}
          </div>
          <div className="cc-code-wrap" ref={codeRef}>
            <pre dangerouslySetInnerHTML={{ __html: html + '\n' }} />
          </div>
          <textarea
            ref={taRef}
            value={text}
            onChange={(e) => onTextChange(e.target.value)}
            onScroll={syncScroll}
            onKeyDown={onKeyDown}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            aria-label="配置编辑区"
          />
        </div>
        <div className="cc-editor-foot">
          <Button
            variant="tertiary"
            color="danger"
            size="sm"
            onPress={onRollback}
            isDisabled={!canRollback}
            title={canRollback ? '恢复为接入 Portunus 之前的配置' : '暂无备份'}
          >
            从备份回滚
          </Button>
          <span className={dirty ? 'unsaved' : 'savetime'}>
            {dirty ? (
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
          重新读取
        </Button>
        <span className="ml-auto text-xs text-muted">⌘ / Ctrl + S</span>
        <Button variant="primary" size="sm" onPress={onSave} isPending={saving}>
          保存
        </Button>
      </div>
    </div>
  );
}