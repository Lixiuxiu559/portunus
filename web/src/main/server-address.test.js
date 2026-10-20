const { test } = require('node:test');
const assert = require('node:assert/strict');
const { addressOf, hostFor, DEV_PORT, PROD_PORT } = require('./server-address.js');

test('端口按模式固定：Dev 13061 / 正式 13060', () => {
  assert.equal(addressOf({ isPackaged: false }).port, DEV_PORT);
  assert.equal(addressOf({ isPackaged: true }).port, PROD_PORT);
});

test('监听 host：local 回环，lan 全网卡', () => {
  assert.equal(hostFor('local'), '127.0.0.1');
  assert.equal(hostFor('lan'), '0.0.0.0');
});

test('未知 networkMode 回退 local', () => {
  assert.equal(hostFor('weird'), '127.0.0.1');
});

test('内部 url 恒回环 127.0.0.1，与监听 host 解耦', () => {
  assert.equal(addressOf({ isPackaged: true, networkMode: 'lan' }).url, `http://127.0.0.1:${PROD_PORT}`);
  assert.equal(addressOf({ isPackaged: false, networkMode: 'local' }).url, `http://127.0.0.1:${DEV_PORT}`);
});

test('baseURL 拼 /api 前缀', () => {
  assert.equal(addressOf({ isPackaged: true }).baseURL, `http://127.0.0.1:${PROD_PORT}/api`);
});

test("打包 Dev 仍用 13061，正式身份仍用 13060", () => {
  assert.equal(addressOf({ profile: "development", isPackaged: true }).port, 13061);
  assert.equal(addressOf({ profile: "production", isPackaged: false }).port, 13060);
});
