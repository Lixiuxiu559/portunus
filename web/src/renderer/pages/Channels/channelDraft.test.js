import test from 'node:test';
import assert from 'node:assert/strict';
import { applyProfile, connectionIdentity, connectionPayload, createDraft, isCurrentRequest, normalizeModels, savePayload, validateChannelCapacity } from './channelDraft.js';

const preset = { id: 'generic', profiles: [] };
const profile = { id: 'chat', type: 'openai', adapter_id: 'generic', base_url: 'https://example.test/v1', model_discovery: 'api' };

test('图标保存回显，清除可提交空值，换预设不覆盖手动选择且不影响连接身份', () => {
  const draft = createDraft({ icon: 'deepseek', type: 'openai' });
  assert.equal(draft.icon, 'deepseek');
  assert.equal(savePayload(draft, []).icon, 'deepseek');
  assert.equal(savePayload({ ...draft, icon: '' }, []).icon, '');
  assert.equal(createDraft().icon, '');
  assert.equal(applyProfile(draft, preset, profile).icon, 'deepseek');
  assert.equal(connectionIdentity(draft), connectionIdentity({ ...draft, icon: 'openai' }));
  assert.equal('icon' in connectionPayload(draft), false);
});

test('通用目录的四协议原样进入渠道草稿，不由页面猜测协议', () => {
  for (const type of ['openai', 'openai_responses', 'anthropic', 'gemini']) {
    const selected = applyProfile(createDraft(), preset, { ...profile, id: type, type });
    assert.equal(selected.type, type);
    assert.equal(selected.profile_id, type);
  }
});

test('首次选择填默认地址；切预设保留自定义地址，显式应用才替换', () => {
  const initial = applyProfile(createDraft(), preset, profile);
  assert.equal(initial.base_url, 'https://example.test/v1');
  const next = { ...profile, id: 'other', base_url: 'https://other.test/api' };
  assert.equal(applyProfile(initial, preset, next).base_url, next.base_url);
  const custom = { ...initial, base_url: 'http://localhost:1234/custom/v1' };
  assert.equal(applyProfile(custom, preset, next).base_url, custom.base_url);
  assert.equal(applyProfile(custom, preset, next, true).base_url, next.base_url);
});

test('编辑不把脱敏密钥放入草稿，空 Key 复用旧凭据，新 Key 明确提交', () => {
  const draft = createDraft({ id: 8, key: 'sk-****', type: 'openai', base_url: 'https://old.test' });
  assert.equal(draft.key, '');
  assert.equal('key' in connectionPayload(draft, 8), false);
  assert.equal(connectionPayload(draft, 8).channel_id, 8);
  assert.equal(connectionPayload({ ...draft, key: ' new-secret ' }, 8).key, 'new-secret');
  assert.equal(applyProfile(draft, preset, profile).base_url, 'https://old.test');
});

test('保存仅提交明确勾选且已规范化的模型，不携带删除模型意图', () => {
  const payload = savePayload({ ...createDraft(), name: ' draft ' }, [' one ', 'two', 'one', '']);
  assert.deepEqual(payload.models, ['one', 'two']);
  assert.equal(payload.name, 'draft');
  assert.deepEqual(savePayload(createDraft(), []).models, []);
  assert.equal('deleted_models' in payload, false);
  assert.deepEqual(normalizeModels([' ', 'a', 'a']), ['a']);
});

test('手动发现模式强制关闭同步，即使草稿保留了原 true', () => {
  const draft = { ...createDraft(), auto_sync: true };
  const manual = applyProfile(draft, preset, { ...profile, model_discovery: 'manual' });
  assert.equal(manual.auto_sync, false);
  assert.equal(savePayload({ ...manual, auto_sync: true }, []).auto_sync, false);
});

test('名称和同步选择不使连接结果失效，地址、Key、协议变化均失效', () => {
  const draft = applyProfile(createDraft(), preset, profile);
  assert.equal(connectionIdentity(draft), connectionIdentity({ ...draft, name: 'new', auto_sync: true }));
  for (const fields of [{ key: 'replacement' }, { base_url: 'https://new.test/v1' }, { type: 'gemini' }, { thinking_compat: false }]) {
    assert.notEqual(connectionIdentity(draft), connectionIdentity({ ...draft, ...fields }));
  }
});

test('过期连接、被新请求替代或取消的结果不允许写入当前草稿', () => {
  const request = { id: 9, generation: 3 };
  assert.equal(isCurrentRequest(request, 3, 9), true);
  assert.equal(isCurrentRequest(request, 4, 9), false);
  assert.equal(isCurrentRequest(request, 3, 10), false);
  assert.equal(isCurrentRequest(request, 3, undefined), false);
});

test('新渠道沿用既有 thinking 默认，编辑保留显式 false', () => {
  assert.equal(createDraft().thinking_compat, true);
  assert.equal(createDraft({ thinking_compat: false }).thinking_compat, false);
});


test('历史渠道空连接设置只改名后保留原自动同步选择', () => {
  for (const options of [undefined, null, {}, { schema_version: 0, model_discovery: '' }]) {
    for (const autoSync of [true, false]) {
      const draft = createDraft({ name: 'legacy', type: 'openai', base_url: 'https://legacy.test/v1',
        auto_sync: autoSync, connection_options: options });
      assert.deepEqual(draft.connection_options, { schema_version: 1, model_discovery: 'api' });
      const payload = savePayload({ ...draft, name: 'renamed' }, []);
      assert.equal(payload.auto_sync, autoSync);
      assert.equal(payload.name, 'renamed');
      assert.equal('key' in payload, false);
    }
  }
});

test('429 冷却范围默认按模型，整渠道设置编辑保存往返且不改变连接身份', () => {
  for (const channel of [undefined, {}, { rate_limit_scope: '' }]) {
    const draft = createDraft(channel);
    assert.equal(draft.rate_limit_scope, 'model');
    assert.equal(savePayload(draft, []).rate_limit_scope, 'model');
  }
  const draft = createDraft({ rate_limit_scope: 'channel' });
  assert.equal(savePayload(draft, []).rate_limit_scope, 'channel');
  assert.equal(applyProfile(draft, preset, profile).rate_limit_scope, 'channel');
  assert.equal(connectionIdentity(draft), connectionIdentity({ ...draft, rate_limit_scope: 'model' }));
  assert.equal('rate_limit_scope' in connectionPayload(draft), false);
});

test('渠道并发默认不限，编辑保留上限并能显式清零，不改变连接和发现结果', () => {
  for (const channel of [undefined, {}, { max_concurrent_requests: 0 }]) {
    assert.equal(createDraft(channel).max_concurrent_requests, '0');
    assert.equal(savePayload(createDraft(channel), []).max_concurrent_requests, 0);
  }
  const draft = createDraft({ max_concurrent_requests: 12 });
  assert.equal(draft.max_concurrent_requests, '12');
  assert.equal(savePayload(draft, []).max_concurrent_requests, 12);
  assert.equal(savePayload({ ...draft, max_concurrent_requests: '0' }, []).max_concurrent_requests, 0);
  assert.equal(applyProfile(draft, preset, profile).max_concurrent_requests, '12');
  assert.equal(connectionIdentity(draft), connectionIdentity({ ...draft, max_concurrent_requests: '3' }));
  assert.equal('max_concurrent_requests' in connectionPayload(draft), false);
});

test('渠道并发拒绝空白、负数、小数、指数、非法文本和无法精确表达的整数', () => {
  for (const value of ['', ' ', '-1', '1.5', '1e2', '+1', 'abc', 'Infinity', 'NaN', '9007199254740992']) {
    assert.match(validateChannelCapacity(value), /非负整数/);
    assert.throws(() => savePayload({ ...createDraft(), max_concurrent_requests: value }, []), /非负整数/);
  }
  for (const value of ['0', '1', '256', '9007199254740991']) {
    assert.equal(validateChannelCapacity(value), null);
    assert.equal(savePayload({ ...createDraft(), max_concurrent_requests: value }, []).max_concurrent_requests, Number(value));
  }
});
