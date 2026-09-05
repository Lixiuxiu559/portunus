package gateway

import (
	"fmt"
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

// circuitBreaker 是单个模型的进程内熔断状态，按连续失败 / 成功计数。
// 粒度是模型而非渠道：中转上游常是个别模型路由故障（假流 / 超时），
// 渠道级熔断会把同渠道的无关模型一起冤枉掉，单渠道分组更是直接全灭 503。
// 字段由 mu 保护：sync.Map 只管键的增删，同一模型被多分组并发请求时
// state / 计数 / openedAt 的读写必须互斥，否则计数漂移、多字撕裂读。
type circuitBreaker struct {
	mu                 sync.Mutex
	state              circuitState
	consecutiveFailure int
	consecutiveStall   int // 连续「不吐响应头」假死失败数，仅成功清零；达 stallOpenThreshold 直接开路
	consecutiveSuccess int
	openedAt           time.Time
}

// stallOpenThreshold 假死快速开路阈值：连响应头都不吐的目标在一个超时窗口内
// 自愈概率极低，按普通阈值（CircuitFailureThreshold，默认 4 次）计数要连续拖垮
// 两轮完整请求才开路；假死连续 2 次即开路，让 failover 尽快绕开死路由。
const stallOpenThreshold = 2

// breakers 记录各模型的熔断器（内存态，重启清零）。
// 同一模型被多个分组引用时共享健康度——上游同一个端点，在哪都是同一个状态。
var breakers = &sync.Map{} // int64(modelID) -> *circuitBreaker

// breakerAllow 判断某模型当前是否放行。开路未冷却则拒绝；
// 开路冷却期满转半开放行一个探测请求。
// 放行时描述为空串；拒绝时返回可读原因（剩余冷却秒数），供 relay 拒绝分支记日志。
func breakerAllow(modelID int64) (bool, string) {
	v, _ := breakers.LoadOrStore(modelID, &circuitBreaker{state: stateClosed})
	b := v.(*circuitBreaker)
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case stateClosed, stateHalfOpen:
		return true, ""
	case stateOpen:
		reset := time.Duration(proxyCfg.CircuitResetSeconds) * time.Second
		remain := reset - time.Since(b.openedAt)
		if remain <= 0 {
			b.state = stateHalfOpen
			b.consecutiveSuccess = 0
			return true, ""
		}
		return false, fmt.Sprintf("熔断冷却中，约 %d 秒后可探测", int(remain.Seconds())+1)
	}
	return true, ""
}

// breakerRecord 记录一次尝试的结果：retryableFailure 为 true 表示一次可重试类失败，
// 否则视为成功。仅可重试失败计入熔断（不可重试的客户端错误不污染健康度）。
// 假死失败走 breakerRecordStall；这里的普通失败（5xx / 429 等上游有回话的失败）
// 不清零 consecutiveStall——上游能回错误说明路由活着，但假死趋势仍应累积。
func breakerRecord(modelID int64, retryableFailure bool) {
	v, _ := breakers.LoadOrStore(modelID, &circuitBreaker{state: stateClosed})
	b := v.(*circuitBreaker)
	b.mu.Lock()
	defer b.mu.Unlock()

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
	b.consecutiveStall = 0
	b.consecutiveSuccess++
	if b.state == stateHalfOpen && b.consecutiveSuccess >= proxyCfg.CircuitSuccessThreshold {
		b.state = stateClosed
		b.consecutiveSuccess = 0
	}
}

// breakerRecordStall 记录一次「不吐响应头」假死失败（等头超时）：
// 在普通连续失败计数之外另按 stallOpenThreshold 快速开路。否则假死模型要按普通
// 阈值失败 4 次（拖垮两轮完整请求）才熔断，每轮请求都得陪它等满超时。
func breakerRecordStall(modelID int64) {
	v, _ := breakers.LoadOrStore(modelID, &circuitBreaker{state: stateClosed})
	b := v.(*circuitBreaker)
	b.mu.Lock()
	defer b.mu.Unlock()

	b.consecutiveSuccess = 0
	b.consecutiveFailure++
	b.consecutiveStall++
	if b.consecutiveStall >= stallOpenThreshold || b.consecutiveFailure >= proxyCfg.CircuitFailureThreshold {
		b.state = stateOpen
		b.openedAt = time.Now()
	}
}
