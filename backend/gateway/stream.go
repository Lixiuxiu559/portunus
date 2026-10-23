package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// streamWatchdog 挂在上游流式请求上的静默看门狗：首包前按首包时限、提交后按静默时限，
// 首内容期限不因元数据续期，交付后收到数据才续静默期限；超时取消上游 body 读。
// 看门狗 ctx 同时传给 doRequest，因此触发时也能掐断头等待之外的整个读取链路；
// 客户端断连（父 ctx 取消）不算看门狗触发，由 Fired 区分，避免误伤取消语义。
type streamWatchdog struct {
	mu     sync.Mutex
	timer  *time.Timer
	cancel context.CancelFunc
	first  time.Duration     // 首包时限（等响应头之后到首个有效事件）
	idle   time.Duration     // 静默时限（首个有效事件之后任意两次数据之间）
	stage  string            // 当前阶段："首包" / "静默"
	d      time.Duration     // 当前阶段时限
	stall  *streamStallError // 触发信息；未触发为 nil
}

// newStreamWatchdog 从 parent（请求 ctx）派生看门狗 ctx。时限为 0 表示该阶段禁用。
func newStreamWatchdog(parent context.Context, first, idle time.Duration) (context.Context, *streamWatchdog) {
	ctx, cancel := context.WithCancel(parent)
	return ctx, &streamWatchdog{cancel: cancel, first: first, idle: idle, stage: "首包", d: first}
}

// Arm 开始首包阶段计时，在拿到上游响应头之后调用。等响应头本身由 Transport 的
// ResponseHeaderTimeout 兜底，这里只管「头已回但迟迟无数据」的静默。
func (w *streamWatchdog) Arm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.armLocked(w.d)
}

// Reset 在每次成功读到上游数据后调用，按当前阶段时限重排续命。
func (w *streamWatchdog) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stall != nil {
		return // 已触发：ctx 已取消，无需再布防
	}
	w.armLocked(w.d)
}

// UseIdle 在流提交（首个有效事件写回）后调用，切换到静默时限并立即重排已布防的
// timer——提交后上游可能再无数据，等不到下一次 Reset，必须在切换当场重排。
func (w *streamWatchdog) UseIdle() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stage = "静默"
	w.d = w.idle
	if w.timer != nil && w.stall == nil {
		if w.idle > 0 {
			w.timer.Reset(w.idle)
		} else {
			w.timer.Stop() // 静默阶段禁用
		}
	}
}

// UseTerminalGrace 终态后的有界尾帧窗口不因心跳而延长。
func (w *streamWatchdog) UseTerminalGrace(wait time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stall != nil {
		return
	}
	w.stage, w.d = "收尾", wait
	w.armLocked(wait)
}

// Fired 返回触发信息；未触发返回 nil。看门狗触发时 body 读会以 ctx 取消报错，
// 调用方据此替换读错误，与客户端主动断连（同样报 context.Canceled）区分开。
func (w *streamWatchdog) Fired() *streamStallError {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stall
}

// Stop 撤防并释放 ctx 资源。
func (w *streamWatchdog) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.cancel()
}

// armLocked 按时限布防；已布防则重排。须持有 mu。
func (w *streamWatchdog) armLocked(d time.Duration) {
	if d <= 0 {
		return // 该阶段禁用
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(d, w.fire)
		return
	}
	w.timer.Reset(d)
}

// fire 看门狗到点：记录触发信息并取消请求 ctx。幂等（Reset 与到期竞态时可能被
// 重排后再触发一次）。
func (w *streamWatchdog) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stall != nil {
		return
	}
	w.stall = &streamStallError{stage: w.stage, wait: w.d}
	w.cancel()
}

// relayStream 把上游流式响应逐 chunk 转成客户端协议并 SSE 写回。
// 提交 200 头被推迟到成功转换出首个有效事件之后，因此「首包前」的失败仍可返回错误触发重试 / failover；
// 「首包后」的失败（静默断连 / 中途转换错误）返回 streamCommittedError，仅中断流、不再换家。
// wd 为上游静默看门狗：首包阶段在拿到响应头后开始计时，提交后切换静默时限，
// 超时掐断并以上游 ctx 取消报错，由 wd.Fired() 还原为 streamStallError。
// 返回 first 为首个有效事件写出时刻（首包前失败或未产出事件时为零值），供上层计算客户端 TTFT。
// SSE 帧的解帧 / 装帧 / [DONE] / 流内 error 形状由 protocol.StreamFramer 承担，
// 本函数只管编排：看门狗、committed 状态、首包计时、错误时机与类别判定。
func relayStream(c *gin.Context, clientProto protocol.Provider, upstreamResp *http.Response, conv protocol.StreamConverter, wd *streamWatchdog, options ...streamRelayOptions) (first time.Time, err error) {
	opt := streamRelayOptions{Source: protocol.ProviderOpenAI}
	if len(options) > 0 {
		opt = options[0]
	}
	if opt.Source == "" {
		opt.Source = protocol.ProviderOpenAI
	}
	if opt.TerminalGrace <= 0 {
		opt.TerminalGrace = defaultTerminalGrace
	}
	gate := newDeliveryGate(c, opt)
	session := protocol.NewStreamSession(opt.Source, clientProto, conv)
	fr := protocol.NewStreamFramer(clientProto, conv)
	unsafe, tailing := opt.ReplayUnsafe, false
	reader := protocol.NewSSEReader(&streamReadProgress{r: upstreamResp.Body, wd: wd, report: gate.report, tailing: &tailing}, 1<<20)
	defer func() {
		gate.report.ResponseCompatibility = session.Compatibility()
		gate.report.Outcome = session.Outcome()
		if err != nil && gate.report.Outcome.Status == "pending" {
			gate.report.Outcome.Status, gate.report.Outcome.Reason = "truncated", "transport_interrupted"
		}
		first = gate.first
	}()
	fail := func(cause error, kind string) error { return finishStreamError(c, fr, gate, unsafe, cause, kind) }
	wd.Arm()
	for {
		event, readErr := reader.ReadEvent()
		if readErr != nil {
			if stopErr := requestControlError(c.Request.Context()); stopErr != nil {
				return first, fail(stopErr, "api_error")
			}
			if streamSucceeded(session.Outcome()) && !errors.Is(readErr, protocol.ErrSSEEventTooLarge) {
				gate.report.TailLimited = readErr != io.EOF
				break
			}
			if readErr == io.EOF {
				break
			}
			if stall := wd.Fired(); stall != nil {
				readErr = stall
			}
			return first, fail(readErr, "overloaded_error")
		}
		batch, processErr := session.Process(event)
		unsafe = unsafe || batch.ReplayUnsafe
		if processErr != nil {
			// 已交付的同协议错误帧仍保留原生形状；未提交时整批丢弃。
			if gate.report.Started && len(batch.Frames) > 0 {
				if writeErr := gate.push(batch); writeErr != nil {
					return first, fail(writeErr, "api_error")
				}
			}
			return first, fail(processErr, "api_error")
		}
		started := gate.report.Started
		if err := gate.push(batch, len(event.Raw)); err != nil {
			return first, fail(err, "api_error")
		}
		if !started && gate.report.Started {
			wd.UseIdle()
		}
		if batch.Done {
			break
		}
		if !tailing && streamSucceeded(session.Outcome()) {
			tailing = true
			wd.UseTerminalGrace(opt.TerminalGrace)
		}
	}
	if stopErr := requestControlError(c.Request.Context()); stopErr != nil {
		return first, fail(stopErr, "api_error")
	}
	final, finishErr := session.Finish()
	if finishErr != nil {
		return first, fail(finishErr, "api_error")
	}
	if err := gate.push(final); err != nil {
		return first, fail(err, "api_error")
	}
	return first, nil
}

// writeStreamHeaders 提交 SSE 响应头与 200 状态码。仅调用一次（首个有效事件后）。
func writeStreamHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)
}
