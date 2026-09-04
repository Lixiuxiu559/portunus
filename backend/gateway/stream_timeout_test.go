package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// ─── 接缝 1：isRetryable 错误分类 ───

// 流已提交后的失败一律不可重试：客户端已收到部分流，重试会二次写流。
// 尤其要挡住「穿透 streamCommittedError.Unwrap 命中下层 net.Error」的误判——
// 上游半途 connection reset 是 net.Error，不挡住就会触发重试导致重复写流。
func TestIsRetryableStreamCommitted(t *testing.T) {
	opErr := &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
	if isRetryable(&streamCommittedError{err: opErr}) {
		t.Error("streamCommittedError（流已提交）不应被判定为可重试，即使底层是 net.Error")
	}
	if isRetryable(&streamCommittedError{err: context.Canceled}) {
		t.Error("streamCommittedError{context.Canceled}（客户端断连）不应被判定为可重试")
	}
	if isRetryable(&streamCommittedError{err: &streamStallError{stage: "静默", wait: time.Second}}) {
		t.Error("streamCommittedError{streamStallError}（提交后静默超时）不应被判定为可重试")
	}
	if !isRetryable(&streamStallError{stage: "首包", wait: time.Second}) {
		t.Error("首包前静默超时（streamStallError）应可重试/换家")
	}
}

// 层-1 错误（等响应头超时）实现 net.Error，保持可重试分类且文案可读。
func TestUpstreamHeaderTimeoutErrorIsNetError(t *testing.T) {
	var ne net.Error
	e := &upstreamHeaderTimeoutError{seconds: 60}
	if !errors.As(e, &ne) {
		t.Fatal("upstreamHeaderTimeoutError 应实现 net.Error")
	}
	if !ne.Timeout() {
		t.Error("Timeout() 应为 true（保持可重试分类）")
	}
	if !isRetryable(e) {
		t.Error("等响应头超时应可重试")
	}
}

// ─── 接缝 2：streamWatchdog ───

func TestStreamWatchdogFiresOnFirstStage(t *testing.T) {
	ctx, wd := newStreamWatchdog(context.Background(), 30*time.Millisecond, time.Minute)
	defer wd.Stop()
	wd.Arm()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("首包阶段超静默后 ctx 应被取消")
	}
	stall := wd.Fired()
	if stall == nil {
		t.Fatal("Fired() 应返回触发信息")
	}
	if stall.stage != "首包" {
		t.Errorf("stage = %q, want 首包", stall.stage)
	}
}

func TestStreamWatchdogResetExtends(t *testing.T) {
	ctx, wd := newStreamWatchdog(context.Background(), 50*time.Millisecond, time.Minute)
	defer wd.Stop()
	wd.Arm()

	// 每 20ms 喂一次（< 50ms 时限），持续 200ms 不应触发
	for i := 0; i < 10; i++ {
		select {
		case <-ctx.Done():
			t.Fatal("持续有数据（每次 Reset）不应触发看门狗")
		case <-time.After(20 * time.Millisecond):
			wd.Reset()
		}
	}
	if wd.Fired() != nil {
		t.Fatal("看门狗不应已触发")
	}
}

func TestStreamWatchdogUseIdleSwitchesDuration(t *testing.T) {
	ctx, wd := newStreamWatchdog(context.Background(), time.Hour, 30*time.Millisecond)
	defer wd.Stop()
	wd.Arm() // 首包时限 1h，不可能触发
	wd.UseIdle()
	wd.Reset() // 提交后 Reset 以静默时限重排

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("UseIdle 后应以静默时限计时")
	}
	if stall := wd.Fired(); stall == nil || stall.stage != "静默" {
		t.Fatalf("Fired() = %v, want stage=静默", stall)
	}
}

func TestStreamWatchdogUseIdleImmediateRearm(t *testing.T) {
	// commit（UseIdle）后若上游再无数据，不会再有 Reset——UseIdle 必须立即以
	// 静默时限重排已布防的 timer，否则生效的是首包时限（错一拍）。
	ctx, wd := newStreamWatchdog(context.Background(), time.Hour, 30*time.Millisecond)
	defer wd.Stop()
	wd.Arm()
	wd.UseIdle() // 不调用 Reset

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("UseIdle 应立即重排 timer")
	}
}

func TestStreamWatchdogParentCancelNotFired(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx, wd := newStreamWatchdog(parent, time.Hour, time.Hour)
	defer wd.Stop()
	wd.Arm()

	cancelParent() // 客户端断连
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("父 ctx 取消应传播")
	}
	if wd.Fired() != nil {
		t.Fatal("父 ctx（客户端）取消不算看门狗触发")
	}
}

func TestStreamWatchdogZeroDisables(t *testing.T) {
	ctx, wd := newStreamWatchdog(context.Background(), 0, 0)
	defer wd.Stop()
	wd.Arm()
	wd.UseIdle()
	wd.Reset() // 全程 0 = 禁用，Reset 不应建 timer

	select {
	case <-ctx.Done():
		t.Fatal("0 时限应禁用看门狗")
	case <-time.After(60 * time.Millisecond):
	}
	if wd.Fired() != nil {
		t.Fatal("禁用态不应有触发记录")
	}
}

// ─── 接缝 3：relayToTarget 集成 ───

const testChunkLine = "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"

// writeStalledSSE 像真实「假流」上游：立刻回响应头，可选先吐一个事件，然后静默挂住
// 直到请求被取消（看门狗掐断 / 测试结束）。
func writeStalledSSE(t *testing.T, w http.ResponseWriter, r *http.Request, emitFirst bool) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fl := w.(http.Flusher)
	fl.Flush()
	if emitFirst {
		w.Write([]byte(testChunkLine))
		fl.Flush()
	}
	<-r.Context().Done() // 挂住，等看门狗 / 客户端掐断
}

// 场景：上游回完响应头后始终不发数据（层-2 prime 阶段静默）。
// 首包超时应可重试：RetryCount=1 时上游被调 2 次，耗尽后客户端拿到 502。
func TestRelayStreamPrimeTimeoutRetries(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1
	pc.FirstByteTimeoutSeconds = 1
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeStalledSSE(t, w, r, false)
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("首包超时应触发重试，上游应被调用 2 次，实际 %d", got)
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("耗尽后应返回 502，实际 %d, body=%s", w.Code, w.Body.String())
	}
}

// 场景复现线上故障：上游先吐一个事件（流已提交）再永久静默（ saver 假流，300s 才被上游掐）。
// 期望：静默时限到即掐断，不再重试（流已提交），渠道计入熔断，后续请求 503。
func TestRelayStreamIdleTimeoutBreaksStreamAndTripsBreaker(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	pc.CircuitFailureThreshold = 1
	pc.StreamIdleTimeoutSeconds = 1
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) > 1 {
			// 熔断开路后不应再进来；真进来了立刻成功，避免拖慢测试
			w.Write([]byte(testChunkLine + "data: [DONE]\n\n"))
			return
		}
		writeStalledSSE(t, w, r, true)
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("流已提交后静默超时不应重试，上游应只被调用 1 次，实际 %d", got)
	}
	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Fatalf("客户端应已收到首个事件，实际 body=%q", w.Body.String())
	}

	// 渠道已计入熔断（阈值 1 → 开路）：第二次请求应 503 且不再打上游
	w2 := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("熔断开路后应返回 503，实际 %d, body=%s", w2.Code, w2.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("熔断开路后上游不应被再调用，实际 %d 次", got)
	}
}

// 静默时限内滴滴答答有数据不应误杀：逐个吐事件后正常收尾。
func TestRelayStreamIdleTimeoutNoFalsePositive(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	pc.StreamIdleTimeoutSeconds = 1
	setProxyConfigForTest(t, pc)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 4; i++ {
			w.Write([]byte(testChunkLine))
			fl.Flush()
			time.Sleep(200 * time.Millisecond) // 间隔 < 1s 静默时限
		}
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if w.Code != 200 {
		t.Fatalf("持续有数据的流不应被掐断，状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := strings.Count(w.Body.String(), `"content":"hi"`); got != 4 {
		t.Errorf("应完整收到 4 个事件，实际 %d", got)
	}
}

// ─── 提交后失败的流内 error 事件（Anthropic 客户端） ───

// fakeConv 可控的转换器：每个 payload 都产出一条 anthropic 事件，第 failAt 个报错。
type fakeConv struct {
	calls  int
	failAt int // 从 1 计；0 = 不报错
	fail   error
}

func (f *fakeConv) Convert([]byte) ([][]byte, error) {
	f.calls++
	if f.failAt > 0 && f.calls >= f.failAt {
		return nil, f.fail
	}
	return [][]byte{[]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)}, nil
}
func (f *fakeConv) Finish() ([][]byte, error) { return nil, nil }
func (f *fakeConv) Usage() *protocol.Usage    { return nil }

// seqReader 依次返回 parts，耗尽后返回 err（nil 则 EOF）。
type seqReader struct {
	parts [][]byte
	i     int
	err   error
}

func (r *seqReader) Read(p []byte) (int, error) {
	if r.i < len(r.parts) {
		n := copy(p, r.parts[r.i])
		r.i++
		return n, nil
	}
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.EOF
}

// streamRelayTestCtx 构造 relayStream 单测环境：看门狗禁用（0 时限），不干扰错误分类断言。
func streamRelayTestCtx(t *testing.T, body io.Reader, conv protocol.StreamConverter) (*httptest.ResponseRecorder, *gin.Context, *http.Response, *streamWatchdog) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(body),
	}
	_, wd := newStreamWatchdog(context.Background(), 0, 0)
	return w, c, resp, wd
}

// 提交后转换失败 → Anthropic 客户端应收到流内 error 事件（api_error），
// 而不是对着截断的流报连接错误。
func TestRelayStreamConvertErrorEmitsAPIError(t *testing.T) {
	body := &seqReader{parts: [][]byte{
		[]byte("data: {\"id\":\"1\"}\n\n"),
		[]byte("data: {\"id\":\"2\"}\n\n"),
	}}
	conv := &fakeConv{failAt: 2, fail: errors.New("字段映射失败")}
	w, c, resp, wd := streamRelayTestCtx(t, body, conv)
	defer wd.Stop()

	_, err := relayStream(c, protocol.ProviderAnthropic, resp, conv, wd)
	var sce *streamCommittedError
	if !errors.As(err, &sce) {
		t.Fatalf("提交后错误应包装为 streamCommittedError，实际 %v", err)
	}
	if out := w.Body.String(); !strings.Contains(out, "event: error") || !strings.Contains(out, `"type":"api_error"`) {
		t.Fatalf("应发流内 api_error 事件，实际 body=%q", out)
	}
}

// 流内 error 事件的嵌套错误对象必须在 "error" 键下：官方 SDK（Claude Code 同款）
// 只读 body.error.type 判类型，写成 "message" 会让类型识别落空、退化为非流式回退。
// event 行须为 error（SDK 按 sse.event === 'error' 分支）。锁定事件结构。
func TestRelayStreamErrorEventShape(t *testing.T) {
	body := &seqReader{parts: [][]byte{
		[]byte("data: {\"id\":\"1\"}\n\n"),
		[]byte("data: {\"id\":\"2\"}\n\n"),
	}}
	conv := &fakeConv{failAt: 2, fail: errors.New("字段映射失败")}
	w, c, resp, wd := streamRelayTestCtx(t, body, conv)
	defer wd.Stop()

	_, err := relayStream(c, protocol.ProviderAnthropic, resp, conv, wd)
	var sce *streamCommittedError
	if !errors.As(err, &sce) {
		t.Fatalf("提交后错误应包装为 streamCommittedError，实际 %v", err)
	}

	var payload string
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if p, ok := parseSSEDataLine(line); ok && strings.Contains(p, `"type":"error"`) {
			payload = p
		}
	}
	if payload == "" {
		t.Fatalf("应发出流内 error 事件，实际 body=%q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "event: error\n") {
		t.Fatalf("error 事件须带 event: error 行，实际 body=%q", w.Body.String())
	}
	var ev struct {
		Type  string `json:"type"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Message any `json:"message"`
	}
	if jerr := json.Unmarshal([]byte(payload), &ev); jerr != nil {
		t.Fatalf("error 事件载荷不是合法 JSON: %v, payload=%s", jerr, payload)
	}
	if ev.Error == nil {
		t.Fatalf(`嵌套错误对象应在 "error" 键下，实际 payload=%s`, payload)
	}
	if ev.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error", ev.Error.Type)
	}
	if ev.Error.Message == "" {
		t.Error("error.message 不应为空")
	}
	if ev.Message != nil {
		t.Errorf(`顶层不应有 "message" 键（错误对象错位），实际 payload=%s`, payload)
	}
}

// 提交后客户端主动断连（context.Canceled）：不发 error 事件（连接已亡），
// 但仍包装为 streamCommittedError 供上层区分熔断语义。
func TestRelayStreamNoErrorEventOnClientCancel(t *testing.T) {
	body := &seqReader{
		parts: [][]byte{[]byte("data: {\"id\":\"1\"}\n\n")},
		err:   context.Canceled,
	}
	conv := &fakeConv{}
	w, c, resp, wd := streamRelayTestCtx(t, body, conv)
	defer wd.Stop()

	_, err := relayStream(c, protocol.ProviderAnthropic, resp, conv, wd)
	var sce *streamCommittedError
	if !errors.As(err, &sce) || !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 streamCommittedError{context.Canceled}，实际 %v", err)
	}
	if out := w.Body.String(); strings.Contains(out, "event: error") {
		t.Fatalf("客户端断连不应发 error 事件，实际 body=%q", out)
	}
}

// 线上场景完整链路（Anthropic 客户端 ← OpenAI 上游假流）：先收到内容，静默超时
// 掐断后应收到流内 overloaded_error，让 Claude Code 按标准过载退避重试。
func TestRelayStreamAnthropicInStreamErrorOnIdleTimeout(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	pc.StreamIdleTimeoutSeconds = 1
	setProxyConfigForTest(t, pc)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeStalledSSE(t, w, r, true)
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	out := w.Body.String()
	if !strings.Contains(out, "event: error") || !strings.Contains(out, `"type":"overloaded_error"`) {
		t.Fatalf("静默掐断后应发流内 overloaded_error，实际 body=%q", out)
	}
}
