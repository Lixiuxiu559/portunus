import test from 'node:test';
import assert from 'node:assert/strict';
import { copyWithFeedback, writeClipboardText } from './clipboard.js';

const notifications = () => {
  const events = [];
  return { events, notify: { success: (text) => events.push(['success', text]), danger: (text) => events.push(['danger', text]) } };
};

test('等待剪贴板实际写入后才反馈成功，内容保留完整换行', async () => {
  const { events, notify } = notifications();
  let finish;
  let copied;
  const result = copyWithFeedback('request-id\n错误摘要', { notify, document: null, clipboard: { writeText: (value) => { copied = value; return new Promise((resolve) => { finish = resolve; }); } } });
  assert.deepEqual(events, []);
  assert.equal(copied, 'request-id\n错误摘要');
  finish();
  assert.equal(await result, true);
  assert.deepEqual(events, [['success', '已复制']]);
});

test('剪贴板拒绝或不可用时只提示失败，不伪造已复制', async () => {
  for (const clipboard of [null, { writeText: async () => { throw new Error('denied'); } }]) {
    const { events, notify } = notifications();
    assert.equal(await copyWithFeedback('text', { notify, clipboard, document: null }), false);
    assert.deepEqual(events, [['danger', '复制失败，请手动选择并复制']]);
  }
});

test('旧式复制回退检查浏览器返回值并清理临时元素与恢复焦点', async () => {
  for (const accepted of [true, false]) {
    const steps = [];
    const input = { style: {}, setAttribute: () => {}, select: () => steps.push('select'), remove: () => steps.push('remove') };
    const document = {
      activeElement: { focus: () => steps.push('focus') }, createElement: () => input,
      body: { appendChild: () => steps.push('append') }, execCommand: (command) => { steps.push(command); return accepted; },
    };
    const result = writeClipboardText('fallback', { clipboard: null, document });
    if (accepted) await result; else await assert.rejects(result, /剪贴板写入失败/);
    assert.equal(input.value, 'fallback');
    assert.deepEqual(steps, ['append', 'select', 'copy', 'remove', 'focus']);
  }
});
