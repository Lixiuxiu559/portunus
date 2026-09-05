package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// streamWatchdog 挂在上游流式请求上的静默看门狗：首包前按首包时限、提交后按静默时限，
// 每收到一段上游数据就重排续命；超时即取消请求 ctx 掐断 body 读阻塞。
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
func relayStream(c *gin.Context, clientProto protocol.Provider, upstreamResp *http.Response, conv protocol.StreamConverter, wd *streamWatchdog) (first time.Time, err error) {
	scanner := bufio.NewScanner(upstreamResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 放大缓冲，避免长行截断

	wd.Arm() // 响应头已回，开始首包静默计时
	committed := false
	for scanner.Scan() {
		wd.Reset() // 有数据，续命
		payload, ok := parseSSEDataLine(scanner.Text())
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		chunks, err := conv.Convert([]byte(payload))
		if err != nil {
			return first, failCommittedStream(c, clientProto, committed, err, "api_error")
		}
		if !committed {
			first = time.Now()
			writeStreamHeaders(c)
			committed = true
			wd.UseIdle() // 已提交，切换静默时限
		}
		for _, chunk := range chunks {
			writeSSE(c.Writer, clientProto, chunk)
		}
		c.Writer.Flush()
	}
	if err := scanner.Err(); err != nil {
		// 看门狗触发（上游静默超时）时底层是 ctx 取消错误，还原为可读的静默超时，
		// 避免与客户端主动断连（同为 context.Canceled）混淆。
		if stall := wd.Fired(); stall != nil {
			err = stall
		}
		return first, failCommittedStream(c, clientProto, committed, err, "overloaded_error")
	}

	finals, err := conv.Finish()
	if err != nil {
		return first, failCommittedStream(c, clientProto, committed, err, "api_error")
	}
	if !committed {
		// 上游 200 但未产出有效事件（如空流）：提交头后再收尾，交给客户端结束。
		// 未产出任何 token，first 保持零值（无首包语义），与「未产出事件时为零值」的注释约定一致。
		writeStreamHeaders(c)
		committed = true
	}
	for _, chunk := range finals {
		writeSSE(c.Writer, clientProto, chunk)
	}
	// OpenAI / Responses 客户端补 [DONE]；Anthropic 客户端已在 message_stop 事件里结束
	if clientProto != protocol.ProviderAnthropic {
		c.Writer.WriteString("data: [DONE]\n\n")
	}
	c.Writer.Flush()
	return first, nil
}

// failCommittedStream 提交后失败：向 Anthropic 客户端发协议内 error 事件再包装为
// streamCommittedError；未提交时原样返回错误（走 failover，客户端什么都还没收到）。
//
// Anthropic 流式协议的 error 是终止事件，形如 {"type":"error","error":{"type":...,
// "message":...}}——嵌套错误对象必须在 "error" 键下（官方 SDK 只读 body.error.type
// 判类型，写别的键名客户端识别不了，只能退化为非流式回退）。Claude Code v2.1.260
// 实测（/tmp 实验室复现，2026-09）：收到流内 error 后——已有文本内容时静默把残缺
// 内容定稿为完整回答（无警告；工具调用等无法定稿时才显示「Server error
// mid-response」）；无内容时显示重试横幅（错误原文可见）重试 2 次，耗尽后自动降级
// 为非流式请求重发——用户视角即「卡住 → 重试 → 报检查网关/网络」，流已被掐断这件
// 事客户端不会明说。分类：静默掐断 / 上游断连 →
// overloaded_error（过载重试语义）；转换失败 → api_error（由调用方传入）。
// OpenAI / Responses 客户端无对应的流内错误标准形式，保持截断，由客户端按断流处理。
// 客户端主动断连（context.Canceled）不发事件——连接已亡，写了无意义。
func failCommittedStream(c *gin.Context, clientProto protocol.Provider, committed bool, err error, anthropicType string) error {
	if !committed {
		return err
	}
	if clientProto == protocol.ProviderAnthropic && !errors.Is(err, context.Canceled) {
		if payload, merr := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]string{"type": anthropicType, "message": err.Error()},
		}); merr == nil {
			writeSSE(c.Writer, clientProto, payload)
			c.Writer.Flush()
		}
	}
	return &streamCommittedError{err: err}
}

// parseSSEDataLine 解析一行 SSE：是 `data:` 行则返回去除前缀与空白的载荷（trim 后非空），
// 否则返回 ok=false。`[DONE]` 原样作为载荷返回。
func parseSSEDataLine(line string) (payload string, ok bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return "", false
	}
	return payload, true
}

// writeStreamHeaders 提交 SSE 响应头与 200 状态码。仅调用一次（首个有效事件后）。
func writeStreamHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)
}

// writeSSE 按客户端协议补 SSE 帧。
func writeSSE(w io.Writer, clientProto protocol.Provider, payload []byte) {
	if clientProto == protocol.ProviderAnthropic {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &ev) == nil && ev.Type != "" {
			io.WriteString(w, "event: "+ev.Type+"\n")
		}
	}
	io.WriteString(w, "data: "+string(payload)+"\n\n")
}
