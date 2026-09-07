import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  shouldRetryNetworkError,
  RETRY_INTERVAL_MS,
  RETRY_MAX_ATTEMPTS,
} from './retryPolicy.js';

// 网络层失败重试决策的表驱动回归测试（node --test，运行：cd web && pnpm test）。
// 场景背景：启动竞态 / 网络模式切换窗口期，请求未到达后端（连接拒绝）应静默重试；
// 超时（已等满 30s）、主动取消、后端已应答（4xx/5xx）都不应重试。

const networkError = (extra = {}) => ({ message: 'Network Error', ...extra });

const cases = [
  {
    name: 'axios ≥1.x 连接拒绝（code=ERR_NETWORK）→ 重试',
    err: networkError({ code: 'ERR_NETWORK' }),
    attempt: 1,
    want: true,
  },
  {
    name: '旧 axios 形态：无 code、message=Network Error → 重试',
    err: networkError(),
    attempt: 1,
    want: true,
  },
  {
    name: `次数封顶：attempt=${RETRY_MAX_ATTEMPTS} → 不重试`,
    err: networkError({ code: 'ERR_NETWORK' }),
    attempt: RETRY_MAX_ATTEMPTS,
    want: false,
  },
  {
    name: `封顶边界：attempt=${RETRY_MAX_ATTEMPTS - 1} → 重试`,
    err: networkError({ code: 'ERR_NETWORK' }),
    attempt: RETRY_MAX_ATTEMPTS - 1,
    want: true,
  },
  {
    name: '30s 超时（ECONNABORTED）→ 不重试（重试只会成倍拉长等待）',
    err: { message: 'timeout of 30000ms exceeded', code: 'ECONNABORTED' },
    attempt: 1,
    want: false,
  },
  {
    name: '超时变体（ETIMEDOUT）→ 不重试',
    err: { message: 'timeout of 30000ms exceeded', code: 'ETIMEDOUT' },
    attempt: 1,
    want: false,
  },
  {
    name: '主动取消（ERR_CANCELED）→ 不重试',
    err: { message: 'canceled', code: 'ERR_CANCELED' },
    attempt: 1,
    want: false,
  },
  {
    name: '后端已应答 5xx → 不重试',
    err: { response: { status: 500 }, message: '内部错误' },
    attempt: 1,
    want: false,
  },
  {
    name: '后端已应答 4xx → 不重试',
    err: { response: { status: 404 }, message: 'Request failed with status code 404' },
    attempt: 1,
    want: false,
  },
  {
    name: 'err 为 null → 不重试（健壮性）',
    err: null,
    attempt: 1,
    want: false,
  },
  {
    name: 'err 为 undefined → 不重试（健壮性）',
    err: undefined,
    attempt: 1,
    want: false,
  },
];

for (const c of cases) {
  test(c.name, () => {
    assert.equal(shouldRetryNetworkError(c.err, c.attempt), c.want);
  });
}

// 设计约束固化：静默重试窗口必须秒级封顶，避免后端真挂时 UI 无限转圈
test('重试总静默时长 ≤ 2.5s', () => {
  assert.ok((RETRY_MAX_ATTEMPTS - 1) * RETRY_INTERVAL_MS <= 2500);
});
