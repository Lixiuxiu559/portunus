import test from 'node:test';
import assert from 'node:assert/strict';
import {
  assertDeletionImpactComplete,
  buildDeletionImpactView,
  createDeletionImpactController,
  normalizeDeletionImpact,
} from './deletionImpact.js';

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};

const impact = {
  groups: [
    { group_id: 1, name: 'coding', strategy: 'manual', removed_item_ids: [1, 2], clears_active: true, becomes_empty: false },
    { group_id: 2, name: 'backup', strategy: 'failover', removed_item_ids: [7], clears_active: false, becomes_empty: true },
  ],
  model_count: 2,
  has_quota_source: true,
};

const target = (kind, id, name = `目标 ${id}`) => ({ kind, id, name });

function setup({ load, remove = async () => {} } = {}) {
  const loads = [];
  const removes = [];
  const deleted = [];
  const states = [];
  const controller = createDeletionImpactController({
    load: (...args) => { loads.push(args); return load(...args); },
    remove: (value) => { removes.push(value); return remove(value); },
    onChange: (state) => states.push(state),
    onDeleted: (value) => deleted.push(value),
  });
  return { controller, loads, removes, deleted, states, state: () => states.at(-1) };
}

test('F1 打开删除目标只调用预览接口，成功后才允许确认并展示影响', async () => {
  const request = deferred();
  const app = setup({ load: () => request.promise });
  const opening = app.controller.open(target('model', 1, 'gpt-4o'));

  const loading = app.state();
  assert.equal(loading.status, 'loading');
  assert.equal(loading.target.id, 1);
  assert.equal(app.loads.length, 1);
  assert.equal(app.loads[0][0].kind, 'model');
  assert.ok(app.loads[0][1].signal, '预览请求应携带取消信号');
  assert.equal(app.controller.canConfirm(), false, '加载中不得确认删除');
  assert.equal(app.removes.length, 0, '预览阶段不得调用删除');

  request.resolve(impact);
  await opening;

  assert.equal(app.state().status, 'ready');
  assert.equal(app.controller.canConfirm(), true);
  assert.equal(app.removes.length, 0);
  const view = buildDeletionImpactView('model', app.state().impact);
  assert.deepEqual(view.groups.map((group) => group.name), ['coding', 'backup']);
  assert.deepEqual(view.groups.map((group) => group.removedCount), [2, 1]);
  assert.equal(view.groups[0].clearsActive, true);
  assert.equal(view.groups[1].becomesEmpty, true);
});

test('F2 预览失败进入可重试错误态并阻止确认，重试成功后才放行', async () => {
  const requests = [deferred(), deferred()];
  let loadCalls = 0;
  const app = setup({ load: () => requests[loadCalls++].promise });
  const first = app.controller.open(target('channel', 9));

  assert.equal(app.state().status, 'loading');
  assert.equal(app.controller.canConfirm(), false);
  assert.equal(await app.controller.confirm(), false);
  assert.equal(app.removes.length, 0, '影响未知时不得删除');

  requests[0].reject(new Error('预览失败'));
  await first;
  assert.equal(app.state().status, 'error');
  assert.match(app.state().error, /预览失败/);
  assert.equal(app.controller.canConfirm(), false);
  assert.equal(await app.controller.confirm(), false);
  assert.equal(app.removes.length, 0);

  const retried = app.controller.retry();
  assert.equal(loadCalls, 2, '重试应重新请求预览');
  assert.equal(app.state().status, 'loading');
  assert.equal(app.controller.canConfirm(), false);
  requests[1].resolve(impact);
  await retried;
  assert.equal(app.state().status, 'ready');
  assert.equal(app.controller.canConfirm(), true);

  // 渠道预览缺少模型数/余额来源时仍属未知影响，必须阻止确认。
  const incomplete = deferred();
  const incompleteApp = setup({ load: () => incomplete.promise });
  const incompleteOpening = incompleteApp.controller.open(target('channel', 10));
  incomplete.resolve({ groups: [] });
  await incompleteOpening;
  assert.equal(incompleteApp.state().status, 'error');
  assert.match(incompleteApp.state().error, /不完整/);
  assert.equal(incompleteApp.controller.canConfirm(), false);
});

test('F3 关闭或切换目标后旧预览响应被丢弃，不覆盖新状态', async () => {
  const first = deferred();
  const second = deferred();
  const queue = [first.promise, second.promise];
  const app = setup({ load: () => queue.shift() });

  const oldRequest = app.controller.open(target('model', 1));
  const newRequest = app.controller.open(target('model', 2));
  assert.equal(app.loads.length, 2);
  assert.equal(app.loads[0][1].signal.aborted, true, '切换目标应取消旧请求');
  assert.equal(app.state().target.id, 2);

  first.resolve({ ...impact, groups: [] });
  await oldRequest;
  assert.equal(app.state().target.id, 2, '旧响应不得覆盖新目标');
  assert.equal(app.state().status, 'loading');
  assert.equal(app.controller.canConfirm(), false, '新目标影响未加载前不得确认');

  second.resolve(impact);
  await newRequest;
  assert.equal(app.state().target.id, 2);
  assert.equal(app.state().status, 'ready');
  assert.equal(buildDeletionImpactView('model', app.state().impact).groups.length, 2);

  const closed = deferred();
  const closeApp = setup({ load: () => closed.promise });
  const closingRequest = closeApp.controller.open(target('model', 3));
  closeApp.controller.cancel();
  assert.equal(closeApp.state().status, 'idle');
  assert.equal(closeApp.loads[0][1].signal.aborted, true);
  assert.equal(await closeApp.controller.confirm(), false);
  assert.equal(closeApp.removes.length, 0);
  closed.resolve(impact);
  await closingRequest;
  assert.equal(closeApp.state().status, 'idle', '关闭后迟到响应不得恢复影响');
  assert.equal(closeApp.states.some((state) => state.status === 'ready'), false);
});

test('F4 确认中防重复提交，删除失败保留影响且不回调，成功才回调', async () => {
  const preview = deferred();
  const removals = [deferred(), deferred()];
  let removeCalls = 0;
  const app = setup({
    load: () => preview.promise,
    remove: () => removals[removeCalls++].promise,
  });
  const opening = app.controller.open(target('channel', 5));
  preview.resolve(impact);
  await opening;

  const firstConfirm = app.controller.confirm();
  assert.equal(app.state().status, 'deleting');
  assert.equal(app.controller.canConfirm(), false);
  assert.equal(await app.controller.confirm(), false);
  assert.equal(removeCalls, 1, '确认中重复触发只能发出一次删除请求');

  removals[0].reject(new Error('删除失败'));
  assert.equal(await firstConfirm, false);
  assert.equal(app.state().status, 'ready', '删除失败保留弹窗内容');
  assert.match(app.state().deleteError, /删除失败/);
  assert.equal(app.state().impact.groups.length, 2, '删除失败后影响数据仍保留');
  assert.equal(app.deleted.length, 0, '删除失败不得调用成功回调');
  assert.equal(app.controller.canConfirm(), true, '失败后允许重试删除');

  const retryConfirm = app.controller.confirm();
  assert.equal(removeCalls, 2);
  removals[1].resolve({ message: 'ok' });
  assert.equal(await retryConfirm, true);
  assert.equal(app.deleted.length, 1);
  assert.equal(app.deleted[0].kind, 'channel');
  assert.equal(app.deleted[0].id, 5);
  assert.equal(app.state().status, 'done');
  assert.equal(app.controller.canConfirm(), false);
});

test('F5 取消不触发任何删除请求', async () => {
  const preview = deferred();
  const app = setup({ load: () => preview.promise });
  const opening = app.controller.open(target('model', 7));
  preview.resolve(impact);
  await opening;

  app.controller.cancel();
  assert.equal(app.state().status, 'idle');
  assert.equal(app.controller.canConfirm(), false);
  assert.equal(await app.controller.confirm(), false);
  assert.equal(app.removes.length, 0);
  assert.equal(app.deleted.length, 0);
});

test('F6 展示模型正确表达分组、0 值、空组风险与渠道提示', () => {
  const normalized = normalizeDeletionImpact(impact);
  const channelView = buildDeletionImpactView('channel', normalized);
  assert.equal(channelView.headline, '将影响 2 个分组');
  assert.equal(channelView.noReferences, false);
  assert.deepEqual(channelView.groups.map((group) => group.strategyLabel), ['手动选择', '故障转移']);
  assert.equal(channelView.groups[0].removedCount, 2);
  assert.equal(channelView.groups[0].clearsActive, true);
  assert.equal(channelView.groups[1].becomesEmpty, true);
  assert.equal(channelView.channel.modelCountText, '2 个模型');
  assert.equal(channelView.channel.hasQuotaSource, true);
  assert.equal(channelView.channel.quotaText, '该渠道的余额来源将一并移除');

  const empty = buildDeletionImpactView('model', normalizeDeletionImpact({ groups: [] }));
  assert.equal(empty.noReferences, true);
  assert.equal(empty.headline, '没有分组引用');
  assert.equal(empty.channel, null);

  const zero = buildDeletionImpactView('channel', normalizeDeletionImpact({
    groups: [{ group_id: 3, name: 'empty', strategy: 'manual', removed_item_ids: [], clears_active: false, becomes_empty: false }],
    model_count: 0,
    has_quota_source: false,
  }));
  assert.equal(zero.groups[0].removedCount, 0, '0 个成员必须显示为 0，而不是未知');
  assert.equal(zero.groups[0].clearsActive, false);
  assert.equal(zero.groups[0].becomesEmpty, false, '不得由成员数自行推断空组');
  assert.equal(zero.channel.modelCountText, '0 个模型', '0 个模型不得显示成未知');
  assert.equal(zero.channel.quotaText, '该渠道没有余额来源');

  const unknown = buildDeletionImpactView('channel', normalizeDeletionImpact({ groups: [] }));
  assert.equal(unknown.channel.modelCountText, '模型数量未知');
  assert.equal(unknown.channel.quotaText, '无法确认是否存在余额来源');

  assert.throws(() => normalizeDeletionImpact({}), /不完整/, '响应结构不完整不得当作无影响');
  assert.throws(() => normalizeDeletionImpact({ groups: [{}] }), /不完整/, '分组字段缺失不得当作无影响');
  assert.throws(
    () => assertDeletionImpactComplete('channel', normalizeDeletionImpact({ groups: [] })),
    /不完整/,
    '渠道缺少模型数/余额来源时不得当作无影响',
  );
  assert.doesNotThrow(() => assertDeletionImpactComplete('model', normalizeDeletionImpact({ groups: [] })));
});
