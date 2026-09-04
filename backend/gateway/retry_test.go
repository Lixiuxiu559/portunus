package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// setProxyConfigForTest 注入转发配置并重置熔断器，避免测试间 channelID 残留污染。
func setProxyConfigForTest(t *testing.T, pc shared.ProxyConfig) {
	t.Helper()
	old := proxyCfg
	proxyCfg = pc
	httpClient = newHTTPClient(pc)
	oldBreakers := breakers
	breakers = &sync.Map{}
	t.Cleanup(func() {
		proxyCfg = old
		httpClient = newHTTPClient(old)
		breakers = oldBreakers
	})
}

// setResponseHeaderTimeout 覆盖当前 httpClient 的等响应头超时，便于测试快速触发。
func setResponseHeaderTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := httpClient
	tr := httpClient.Transport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = d
	httpClient = &http.Client{Transport: tr}
	t.Cleanup(func() { httpClient = old })
}

func TestRelayRetryThenSucceed(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2 // 最多 3 次尝试
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("上游应被调用 3 次，实际 %d", got)
	}
}

func TestRelayRetryExhausted(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1 // 最多 2 次尝试
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":{"message":"upstream down"}}`))
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("上游应被调用 2 次，实际 %d", got)
	}
	if !strings.Contains(w.Body.String(), "upstream down") {
		t.Errorf("应透传上游 JSON 错误体: %s", w.Body.String())
	}
}

func TestRelay4xxNoRetry(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("4xx 不应重试，上游应被调用 1 次，实际 %d", got)
	}
}

func TestRelayTimeoutTriggersRetry(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1
	setProxyConfigForTest(t, pc)
	setResponseHeaderTimeout(t, 50*time.Millisecond)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1"}`))
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("超时耗尽应返回 502，实际 %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("超时应触发重试，上游应被调用 2 次，实际 %d", got)
	}
}

func TestRelayStreamRetryBeforeFirstByte(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		fl.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("流式首包前失败应重试，上游应被调用 2 次，实际 %d", got)
	}
	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Errorf("流式响应未包含内容: %s", w.Body.String())
	}
}

func TestCircuitBreakerOpenAndRecover(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 3
	pc.CircuitSuccessThreshold = 2
	pc.CircuitResetSeconds = 60
	setProxyConfigForTest(t, pc)

	id := int64(999)
	if !breakerAllow(id) {
		t.Fatal("初始应放行")
	}
	for i := 0; i < 3; i++ {
		breakerRecord(id, true)
	}
	if breakerAllow(id) {
		t.Fatal("连续失败达阈值后应开路")
	}

	// 把开路时间回拨到冷却期之前 → 半开探测
	v, _ := breakers.Load(id)
	v.(*circuitBreaker).openedAt = time.Now().Add(-time.Minute)
	if !breakerAllow(id) {
		t.Fatal("冷却期后应半开放行")
	}
	breakerRecord(id, false)
	breakerRecord(id, false)
	b2, _ := breakers.Load(id)
	if b2.(*circuitBreaker).state != stateClosed {
		t.Errorf("半开连续成功应转关闭，实际 state=%d", b2.(*circuitBreaker).state)
	}
}

func TestRelayCircuitSkipsOpenChannel(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	pc.CircuitFailureThreshold = 1
	setProxyConfigForTest(t, pc)

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)

	// 拿到 setupGateway 建的渠道 ID，直接制造开路态
	var ch channel.Channel
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatalf("查询渠道失败: %v", err)
	}
	breakerRecord(ch.ID, true) // 阈值 1，一次即开路
	if breakerAllow(ch.ID) {
		t.Fatal("开路的渠道在冷却期内应被 breakerAllow 拒绝")
	}

	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("全部渠道开路应返回 503，实际 %d", w.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("开路渠道不应再调用上游，实际调用 %d 次", got)
	}
}

// TestIsRetryableClientCancel 断言客户端主动取消（context.Canceled）不被判为可重试失败。
// 根因：claude-cli 等客户端断连/取消时，doRequest 返回 "Post ...: context canceled"
// （*url.Error 包装 context.Canceled）。url.Error 实现了 net.Error，被 isRetryable 的
// net.Error 分支误判为可重试 → 既触发无意义重试，又被 breakerRecord 记入熔断，
// 连续几次用户取消就把整个渠道熔断开路，导致后续所有请求 503「所有渠道暂不可用」。
func TestIsRetryableClientCancel(t *testing.T) {
	if isRetryable(context.Canceled) {
		t.Error("context.Canceled（客户端主动取消）不应被判定为可重试")
	}
	wrapped := &url.Error{Op: "Post", URL: "https://upstream/v1/chat/completions", Err: context.Canceled}
	if isRetryable(wrapped) {
		t.Error("被 url.Error 包装的 context.Canceled 不应被判定为可重试")
	}
	// 上游超时（context.DeadlineExceeded）仍是可重试失败，不能被上面误伤
	if !isRetryable(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded 应被判定为可重试")
	}
}
