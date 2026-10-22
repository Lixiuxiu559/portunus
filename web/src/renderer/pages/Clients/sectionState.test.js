import test from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeSearch,
  parseSelection,
  selectionHref,
  selectionSearch,
} from './sectionState.js';

const params = (query) => new URLSearchParams(query);

test('默认选中接入示例的 API', () => {
  assert.deepEqual(parseSelection(params('')), { section: 'examples', tool: 'api' });
  assert.deepEqual(parseSelection(params('group=coding')), { section: 'examples', tool: 'api' });
});

test('section=config 默认进入 Claude Code', () => {
  assert.deepEqual(parseSelection(params('section=config')), { section: 'config', tool: 'claude' });
  assert.deepEqual(parseSelection(params('section=config&tool=claude')), { section: 'config', tool: 'claude' });
});

test('section 下的非法 tool 回退到该分组默认项', () => {
  assert.deepEqual(parseSelection(params('section=config&tool=api')), { section: 'config', tool: 'claude' });
  assert.deepEqual(parseSelection(params('section=examples&tool=bad')), { section: 'examples', tool: 'api' });
  assert.deepEqual(parseSelection(params('section=examples&tool=codex')), { section: 'examples', tool: 'codex' });
});

test('未知 section 回退默认，孤立 tool 不生效', () => {
  assert.deepEqual(parseSelection(params('section=other&tool=claude')), { section: 'examples', tool: 'api' });
  assert.deepEqual(parseSelection(params('tool=claude')), { section: 'examples', tool: 'api' });
});

test('默认值从 URL 省略，非默认值保留 section', () => {
  assert.equal(selectionSearch(params(''), { section: 'examples', tool: 'api' }).toString(), '');
  assert.equal(
    selectionSearch(params(''), { section: 'examples', tool: 'claude' }).toString(),
    'section=examples&tool=claude',
  );
  assert.equal(selectionSearch(params(''), { section: 'config', tool: 'claude' }).toString(), 'section=config');
});

test('写回时保留 group 与未知参数', () => {
  const next = selectionSearch(params('group=coding&foo=1'), { section: 'examples', tool: 'codex' });
  assert.equal(next.get('group'), 'coding');
  assert.equal(next.get('foo'), '1');
  assert.equal(next.get('section'), 'examples');
  assert.equal(next.get('tool'), 'codex');
});

test('规范化幂等，且能修正非法 URL', () => {
  const once = normalizeSearch(params('section=config&tool=api&group=coding'));
  assert.equal(once.toString(), 'section=config&group=coding');
  const twice = normalizeSearch(once);
  assert.equal(twice.toString(), once.toString());
});

test('selectionHref 生成可分享的深链', () => {
  assert.equal(selectionHref(params(''), { section: 'examples', tool: 'api' }), '/clients');
  assert.equal(
    selectionHref(params('group=coding'), { section: 'config', tool: 'claude' }),
    '/clients?group=coding&section=config',
  );
});
