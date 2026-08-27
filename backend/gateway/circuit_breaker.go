package gateway

import (
	"sync"
	"time"
)

// circuitState 是熔断器的三态。
type circuitState int

const (
	stateClosed   circuitState = iota // 正常放行
	stateOpen                         // 开路：拒绝请求
	stateHalfOpen                     // 半开：放行探测请求
)

// circuitBreaker 是单个渠道的进程内熔断状态，按连续失败 / 成功计数。
type circuitBreaker struct {
	state              circuitState
	consecutiveFailure int
	consecutiveSuccess int
	openedAt           time.Time
}

// breakers 记录各渠道的熔断器（内存态，重启清零）。
var breakers = &sync.Map{} // int64(channelID) -> *circuitBreaker

// breakerAllow 判断某渠道当前是否放行。开路未冷却则拒绝；
// 开路冷却期满转半开放行一个探测请求。
func breakerAllow(channelID int64) bool {
	v, _ := breakers.LoadOrStore(channelID, &circuitBreaker{state: stateClosed})
	b := v.(*circuitBreaker)
	switch b.state {
	case stateClosed:
		return true
	case stateHalfOpen:
		return true
	case stateOpen:
		reset := time.Duration(proxyCfg.CircuitResetSeconds) * time.Second
		if time.Since(b.openedAt) >= reset {
			b.state = stateHalfOpen
			b.consecutiveSuccess = 0
			return true
		}
		return false
	}
	return true
}

// breakerRecord 记录一次尝试的结果：retryableFailure 为 true 表示一次可重试类失败，
// 否则视为成功。仅可重试失败计入熔断（不可重试的客户端错误不污染健康度）。
func breakerRecord(channelID int64, retryableFailure bool) {
	v, _ := breakers.LoadOrStore(channelID, &circuitBreaker{state: stateClosed})
	b := v.(*circuitBreaker)

	if retryableFailure {
		b.consecutiveSuccess = 0
		b.consecutiveFailure++
		if b.consecutiveFailure >= proxyCfg.CircuitFailureThreshold {
			b.state = stateOpen
			b.openedAt = time.Now()
		}
		return
	}

	b.consecutiveFailure = 0
	b.consecutiveSuccess++
	if b.state == stateHalfOpen && b.consecutiveSuccess >= proxyCfg.CircuitSuccessThreshold {
		b.state = stateClosed
		b.consecutiveSuccess = 0
	}
}
