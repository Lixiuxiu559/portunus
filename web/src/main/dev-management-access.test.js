const test = require('node:test');
const assert = require('node:assert/strict');

test('Vite 管理代理拒绝非本机连接，不信任伪造转发头', async () => {
  const { localManagementProxyGuard } = await import('../../dev-management-access.mjs');
  for (const [peer, allow] of [
    ['127.0.0.1', true], ['127.0.1.2', true], ['::1', true], ['::ffff:127.0.0.1', true],
    ['192.168.1.2', false], ['::ffff:192.168.1.2', false], ['fd00::1', false], ['8.8.8.8', false], [undefined, false],
  ]) {
    let next = false;
    let body;
    const res = { setHeader() {}, end(value) { body = value; } };
    localManagementProxyGuard({
      url: '/api/apikey', socket: { remoteAddress: peer },
      headers: { 'x-forwarded-for': '127.0.0.1', forwarded: 'for=127.0.0.1' },
    }, res, () => { next = true; });
    assert.equal(next, allow, `peer=${peer}`);
    if (!allow) {
      assert.equal(res.statusCode, 403);
      assert.match(body, /管理接口仅允许本机访问/);
    }
  }
});

test('Vite 只保护管理代理，不改变服务路径', async () => {
  const { localManagementProxyGuard } = await import('../../dev-management-access.mjs');
  for (const url of ['/v1/models', '/v1/chat/completions', '/src/main.js']) {
    let next = false;
    localManagementProxyGuard({ url, socket: { remoteAddress: '192.168.1.2' } }, {}, () => { next = true; });
    assert.equal(next, true);
  }
});
