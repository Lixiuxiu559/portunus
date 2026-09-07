/**
 * 网络层失败重试的纯决策逻辑（不依赖 axios / HeroUI，便于 node:test 表驱动单测）。
 *
 * 背景：打包版启动时后端 sidecar 由主进程异步拉起（已由 main.js 门控缓解，这里是
 * 兜底），网络模式切换 restartServer 也有 ~300ms + 子进程启动的无服务窗口。
 * 浏览器 XHR 连接失败时 axios 抛 AxiosError(message='Network Error', code='ERR_NETWORK')，
 * 无 error.response。这类失败做有限次静默重试，超限才走 request.js 的 toast 报错。
 */

// 重试节奏：固定 500ms 间隔、最多 5 次失败判定（即最多静默重试 4 次），
// 静默窗口 ≈ 2s + 请求耗时，秒级封顶，避免后端真挂时 UI 无限转圈。
export const RETRY_INTERVAL_MS = 500;
export const RETRY_MAX_ATTEMPTS = 5;

// 不应重试的错误码：请求超时（已等满 30s，重试只会成倍拉长等待）/ 主动取消
const NON_RETRYABLE_CODES = new Set(['ECONNABORTED', 'ETIMEDOUT', 'ERR_CANCELED']);

/**
 * 判断第 attempt 次失败（attempt 从 1 计，含本次）是否应静默重试。
 * 只重试「请求未到达后端」的连接类失败；拿到 HTTP 响应（4xx/5xx）重试无意义。
 * @param {Error} err axios 错误对象
 * @param {number} attempt 已失败次数（含本次）
 */
export function shouldRetryNetworkError(err, attempt) {
  if (!err || attempt >= RETRY_MAX_ATTEMPTS) return false;
  if (err.response) return false; // 后端已应答：业务错误
  if (NON_RETRYABLE_CODES.has(err.code)) return false; // 超时 / 取消
  // 浏览器 XHR 网络错误：axios ≥1.x 为 code='ERR_NETWORK'；兼容无 code 的旧形态
  return err.code === 'ERR_NETWORK' || err.message === 'Network Error';
}
