import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ChevronRight } from 'lucide-react';
import { TREE_SECTIONS } from './tools';
import { selectionHref } from './sectionState';

/**
 * 左树：两个可折叠分组 + 叶子链接。
 * 不用 role="tree"：这里是导航链接列表，原生 button/ul/Link 已具备键盘与读屏语义。
 */
export default function TreeNav({ searchParams, selection, onSelect }) {
  const [expanded, setExpanded] = useState({ examples: true, config: true });

  // 深链落在折叠分组时自动展开，避免选中项被隐藏
  useEffect(() => {
    setExpanded((old) => (old[selection.section] ? old : { ...old, [selection.section]: true }));
  }, [selection.section]);

  return (
    <nav className="service-tree" aria-label="接入内容">
      <ul className="service-tree-list">
        {TREE_SECTIONS.map((section) => {
          const isOpen = expanded[section.id];
          return (
            <li key={section.id}>
              <button
                type="button"
                className="service-tree-section"
                aria-expanded={isOpen}
                aria-controls={`service-tree-${section.id}`}
                onClick={() => setExpanded((old) => ({ ...old, [section.id]: !old[section.id] }))}
              >
                <ChevronRight className={`service-tree-chevron${isOpen ? ' is-open' : ''}`} aria-hidden="true" />
                <span>{section.label}</span>
              </button>
              {isOpen && (
                <ul id={`service-tree-${section.id}`} className="service-tree-children">
                  {section.children.map((item) => {
                    const active = selection.section === section.id && selection.tool === item.id;
                    return (
                      <li key={item.id}>
                        <Link
                          to={selectionHref(searchParams, { section: section.id, tool: item.id })}
                          className="service-tree-item"
                          aria-current={active ? 'page' : undefined}
                          onClick={(event) => {
                            // 交给页面决定是否被未保存守卫拦截；链接本身仍可复制/回车
                            event.preventDefault();
                            onSelect({ section: section.id, tool: item.id });
                          }}
                        >
                          <span className="service-tree-icon" aria-hidden="true">{item.icon}</span>
                          <span className="truncate">{item.label}</span>
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              )}
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
