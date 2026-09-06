package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// 本文件是失败处置的唯一权威（领域词条见 CONTEXT.md「失败处置」）：
// 一次上游尝试失败的错误值 → failDecision（重试 / 两档假死 / 熔断喂法 /
// 归因 errKind / 客户端状态码与错误类别）。此前同一 error 被 isRetryable、
// isStall、熔断记账 defer、状态码裁决、classifyErr、writeRelayError 六处
// switch 各判一遍，顺序敏感规则散布多文件靠人肉对齐；现收敛为 failSpec
// 一张表，任何语义调整只改这里。

// ── 错误类型 ─────────────────────────────────────────────────────────────

// upstreamStatusError 表示上游返回非 2xx，携带状态码与响应体供上层透出。
type upstreamStatusError struct {
	status     int
	body       []byte
	retryAfter string // 上游 Retry-After 头原值（429 / 503 常见），透传给客户端安排重试节奏
}

func (e *upstreamStatusError) Error() string {
	// err_msg 会落库：带上状态码与响应体片段，否则最高频的 upstream_error
	// 类失败全是同一句无信息量文案，无法区分限流与上游内部错误。
	return fmt.Sprintf("上游返回 %d: %s", e.status, truncateErr(string(e.body), 200))
}

// convertError 标记请求 / 响应协议转换阶段的失败（客户端请求体或渠道配置问题），
// 与上游 / 网络故障区分开，供日志归因与排障。
type convertError struct{ err error }

func (e *convertError) Error() string { return e.err.Error() }
func (e *convertError) Unwrap() error { return e.err }

// streamCommittedError 表示流已提交（已写 200 头 + SSE 头），之后的失败无法 failover。
type streamCommittedError struct{ err error }

func (e *streamCommittedError) Error() string { return e.err.Error() }
func (e *streamCommittedError) Unwrap() error { return e.err }

// streamStallError 上游流静默超时：等首个有效事件（首包）或两次数据之间的静默
// 超过看门狗时限。首包前是可重试失败（换家）；首包后由 streamCommittedError 包装，只能掐断。
type streamStallError struct {
	stage string        // 触发阶段："首包" / "静默"
	wait  time.Duration // 触发时的静默时限
}

func (e *streamStallError) Error() string {
	return fmt.Sprintf("流式响应%s超时: %ds（上游无新数据）", e.stage, int(e.wait.Seconds()))
}

// upstreamHeaderTimeoutError 上游在 FirstByteTimeoutSeconds 内未返回响应头。
// 只把 Transport 原生晦涩文案（net/http: timeout awaiting response headers）
// 换成可读错误。可重试 / 假死分类由 failSpec 按具体类型前置判定（在 net.Error
// 分支之前），不依赖 net.Error 接口——调整 failSpec 分支顺序时不得把该类型
// 落回通用网络分支，否则丢失快速熔断 + 504 语义。
type upstreamHeaderTimeoutError struct {
	seconds int
}

func (e *upstreamHeaderTimeoutError) Error() string {
	return fmt.Sprintf("上游 %ds 未返回响应头", e.seconds)
}

// ── 失败归因词表（shared.Log.ErrKind 取值），前端与排障按此过滤 ────────────

const (
	errKindClientCancel    = "client_cancel"      // 客户端主动断开（Esc / 关窗口）
	errKindUpstream        = "upstream_error"     // 上游返回非 2xx
	errKindWatchdog        = "watchdog_timeout"   // 上游流静默超时被看门狗掐断
	errKindStreamInterrupt = "stream_interrupted" // 流已提交后中途断掉，无法 failover
	errKindConvert         = "convert_error"      // 协议转换 / 请求改写失败
	errKindNetwork         = "network"            // 连接 / 超时等网络层失败
	errKindInternal        = "internal"           // 其余未归类失败
	errKindCircuitOpen     = "circuit_open"       // 分组全部目标熔断开路，未打上游直接 503
)

// ── 客户端错误类别（协议无关语义；协议形状渲染在 relay_error.go）────────────

// relayErrorKind 是客户端协议无关的错误类别。writeRelayError 按客户端协议把它
// 映射为具体错误类型字符串与响应体形状，让 claude code / codex 等客户端拿到
// 协议正确的错误——客户端 SDK 靠错误类型与状态码归类重试，笼统的
// {"error":"..."} 只能显示无意义的报错文案。
type relayErrorKind int

const (
	relayErrInvalidRequest relayErrorKind = iota // 400 请求体不合法 / 分组解析失败
	relayErrNotFound                             // 404 分组不存在
	relayErrAuth                                 // 401 鉴权失败
	relayErrRateLimit                            // 429 上游限流
	relayErrOverloaded                           // 503 分组全部目标熔断开路
	relayErrAPI                                  // 其余 5xx 上游 / 网关故障
)

// relayErrorKindByStatus 把非 2xx 的上游状态码映射为错误类别：429 限流、
// 401/403 渠道鉴权问题、404 目标不存在、5xx 上游 / 网关故障、其余 4xx 按请求
// 问题归类。渠道 key 过期（401）被呈现为 authentication_error 而非
// invalid_request_error——后者语义是「请求格式错、勿重试」，会把用户与运维
// 的排障方向引向客户端 payload 而非换 key。
func relayErrorKindByStatus(status int) relayErrorKind {
	switch {
	case status == http.StatusTooManyRequests:
		return relayErrRateLimit
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return relayErrAuth
	case status == http.StatusNotFound:
		return relayErrNotFound
	case status >= 500:
		return relayErrAPI
	default:
		return relayErrInvalidRequest
	}
}

// ── 查表 ─────────────────────────────────────────────────────────────────

// failDecision 是一次失败的全部处置决定，六个消费方各取所需：
// 重试循环读 Retryable / StallNoHeader；熔断 defer 读 BreakerFail / StallNoHeader；
// 归因落库读 ErrKind；状态码裁决与错误体渲染读 Status / RelayKind / RetryAfter。
type failDecision struct {
	Retryable      bool           // 值得对同一 / 下一上游重试（内层循环继续）
	StallNoHeader  bool           // 等头假死：跳过同目标重试直接换下一家；熔断按快速阈值（2 次）开路
	StallMidStream bool           // 流静默假死：保留同目标重试；熔断按普通阈值计数
	BreakerFail    bool           // 计入熔断健康度（可重试失败 / 已提交的流中断）；false 时按成功清零语义之外的「不记」处理
	ErrKind        string         // 归因类别（shared.Log.ErrKind）
	Status         int            // 客户端状态码裁决
	RelayKind      relayErrorKind // 客户端错误类别
	RetryAfter     string         // 上游 Retry-After 原值透传；空 = 无
}

// failSpec 把一个失败错误值解析为处置决定。判定顺序有讲究，改动前先读
// fail_test.go 的表——每一行都是一份行为契约：
//  1. committed 必须最先 errors.As：它会穿透 Unwrap 命中下层 net.Error /
//     upstreamStatusError，后判会导致「半途断连」被误判为可重试网络错误或上游错误；
//  2. Canceled 其次（含 committed 包装的取消）：用户行为不是渠道故障——
//     不重试、不计熔断，归因 client_cancel（errors.Is 穿透 Unwrap 命中）；
//  3. 等头假死（upstreamHeaderTimeoutError）在 net.Error 之前：它实现了
//     net.Error，后判会丢掉「快速开路 + 504」的特殊处置。
func failSpec(err error) failDecision {
	if err == nil {
		return failDecision{}
	}
	// 流已提交：无法 failover，一律不可重试。
	var committed *streamCommittedError
	if errors.As(err, &committed) {
		d := failDecision{ErrKind: errKindStreamInterrupt, Status: http.StatusBadGateway, RelayKind: relayErrAPI}
		if errors.Is(err, context.Canceled) {
			d.ErrKind = errKindClientCancel // 包装的用户取消仍归用户行为
		} else {
			// 半途失败仍是模型不健康（假流 / 静默超时 / 断连），普通阈值计入熔断，
			// 否则持续假死的模型永远开不了路。
			d.BreakerFail = true
		}
		return d
	}
	// 客户端主动取消（context.Canceled）不是上游故障，不重试也不计入熔断。
	// 否则 doRequest 返回的 "Post ...: context canceled"（*url.Error 包装）会命中
	// 下方 net.Error 分支被误判为可重试，一次用户取消就污染熔断器健康度。
	if errors.Is(err, context.Canceled) {
		return failDecision{ErrKind: errKindClientCancel, Status: http.StatusBadGateway, RelayKind: relayErrAPI}
	}
	// 等头假死：连响应头都不吐的目标在一个超时窗口内自愈概率极低，
	// 同目标重试只会再等满一轮超时——换下一家 + 熔断快速开路 + 504。
	// 注意与看门狗流静默（streamStallError）区分：后者响应头已到，保留同目标重试。
	var hte *upstreamHeaderTimeoutError
	if errors.As(err, &hte) {
		return failDecision{
			Retryable: true, StallNoHeader: true, BreakerFail: true,
			ErrKind:   errKindNetwork, // Transport 层网络失败
			Status:    http.StatusGatewayTimeout,
			RelayKind: relayErrAPI,
		}
	}
	// 流静默假死：看门狗掐断，归因 watchdog_timeout，504；假死趋势按普通阈值累积。
	var stall *streamStallError
	if errors.As(err, &stall) {
		return failDecision{
			Retryable: true, StallMidStream: true, BreakerFail: true,
			ErrKind:   errKindWatchdog,
			Status:    http.StatusGatewayTimeout,
			RelayKind: relayErrAPI,
		}
	}
	// 上游有回话：状态码即裁决，5xx / 429 可重试并计入熔断，其余 4xx 是请求问题。
	var se *upstreamStatusError
	if errors.As(err, &se) {
		retryable := se.status >= 500 || se.status == 429
		return failDecision{
			Retryable: retryable, BreakerFail: retryable,
			ErrKind:    errKindUpstream,
			Status:     se.status,
			RelayKind:  relayErrorKindByStatus(se.status),
			RetryAfter: se.retryAfter,
		}
	}
	// 协议转换 / 请求改写失败：客户端请求体或渠道配置问题，重试无意义。
	var convErr *convertError
	if errors.As(err, &convErr) {
		return failDecision{ErrKind: errKindConvert, Status: http.StatusBadGateway, RelayKind: relayErrAPI}
	}
	// 网络层失败（dial 失败 / 连接重置 / 非流式总超时的 *url.Error 包装）。
	var ne net.Error
	if errors.As(err, &ne) {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			// 非流式总超时以 *url.Error 包装 DeadlineExceeded 出现且 url.Error 实现
			// net.Error——按网络归因，但状态码提为 504 与上游明确拒绝区分。
			status = http.StatusGatewayTimeout
		}
		return failDecision{Retryable: true, BreakerFail: true, ErrKind: errKindNetwork, Status: status, RelayKind: relayErrAPI}
	}
	// 裸 DeadlineExceeded 不会走到独立分支：context.DeadlineExceeded 自带
	// Timeout/Temporary 方法，本就满足 net.Error，由上方网络分支覆盖。
	return failDecision{ErrKind: errKindInternal, Status: http.StatusBadGateway, RelayKind: relayErrAPI}
}

// ── 旧判定名薄委托：语义唯一实现在 failSpec ─────────────────────────────────
// 仅存为既有调用点与测试的可读入口（集成测试直接引用这些名字）；任何分支
// 调整只改 failSpec，这里不允许出现第二个 switch。

// isRetryable 判断某次失败是否值得对同一 / 下一上游重试。
func isRetryable(err error) bool { return failSpec(err).Retryable }

// isStall 判断是否等头假死（Transport 层等响应头超时）。
func isStall(err error) bool { return failSpec(err).StallNoHeader }

// classifyErr 把失败原因归为固定归因类别。
func classifyErr(err error) string { return failSpec(err).ErrKind }
