package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// withCfg 返回把转发配置注入 deps 的 mutate 函数：配置、熔断阈值、HTTP 客户端
// 同源重建（替代旧的包级全局换血 helper）。
func withCfg(pc shared.ProxyConfig) func(*Deps) {
	return func(d *Deps) {
		d.Cfg = pc
		d.Client = NewHTTPClient(pc)
		d.Breakers = NewBreakerStore(pc)
	}
}

// withHeaderTimeout 在 withCfg 之上覆盖等响应头超时（秒级配置表达不了的毫秒级用例）。
func withHeaderTimeout(pc shared.ProxyConfig, d time.Duration) func(*Deps) {
	return func(deps *Deps) {
		withCfg(pc)(deps)
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.ResponseHeaderTimeout = d
		deps.Client = &http.Client{Transport: tr}
	}
}

func TestRelayRetryThenSucceed(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2 // 最多 3 次尝试

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})

	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
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

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":{"message":"upstream down"}}`))
	})

	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
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

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	})

	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("4xx 不应重试，上游应被调用 1 次，实际 %d", got)
	}
}

// TestRelayHeaderTimeoutSkipsRetry 锁定假死快速通道：上游迟迟不返回响应头（不吐
// 首字节）时，同目标重试只会再等满一轮超时——应放弃该目标直接失败，而不是把
// RetryCount 次超时全烧完（旧行为 3 次 × 60s = 3 分钟才换家）。
func TestRelayHeaderTimeoutSkipsRetry(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2 // 旧行为会把 3 次尝试全烧在假死目标上

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(200 * time.Millisecond) // 不吐响应头，触发等头超时
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1"}`))
	})

	r, key, _ := setupGatewayDeps(t, withHeaderTimeout(pc, 50*time.Millisecond), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("假死目标应直接放弃并返回 504 上游超时语义，实际 %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("不吐响应头的目标不应同目标重试，上游应被调用 1 次，实际 %d", got)
	}
}

// TestRelayStallFailoverFastToNextTarget 锁定假死 failover 快速通道的换家路径：
// 首选目标不吐响应头，只尝试 1 次就换下一家接住流量；且上游请求必须带 X-Request-Id
// （跨网关对账凭据）。
func TestRelayStallFailoverFastToNextTarget(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2

	var stallCalls, okCalls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &req)
		if req.Model == "bad-model" {
			atomic.AddInt32(&stallCalls, 1)
			if r.Header.Get("X-Request-Id") == "" {
				t.Error("上游请求应携带 X-Request-Id")
			}
			time.Sleep(200 * time.Millisecond) // 不吐响应头，触发等头超时
			return
		}
		atomic.AddInt32(&okCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"1","object":"chat.completion","model":"good-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})

	r, key, _ := setupGatewayDeps(t, withHeaderTimeout(pc, 50*time.Millisecond), protocol.ProviderOpenAI, upstream)
	// 同渠道再种 bad-model，并把它插为分组首选（priority 0），原模型降为 priority 1
	var ch channel.Channel
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatalf("查询渠道失败: %v", err)
	}
	bad := model.Model{ChannelID: ch.ID, Name: "bad-model"}
	if err := shared.DB.Create(&bad).Error; err != nil {
		t.Fatalf("建 bad-model 失败: %v", err)
	}
	var g group.Group
	if err := shared.DB.Where("name = ?", "my-model").First(&g).Error; err != nil {
		t.Fatalf("查询分组失败: %v", err)
	}
	var firstItem group.GroupItem
	if err := shared.DB.Where("group_id = ?", g.ID).First(&firstItem).Error; err != nil {
		t.Fatalf("查询分组项失败: %v", err)
	}
	if err := shared.DB.Model(&firstItem).Update("priority", 1).Error; err != nil {
		t.Fatalf("降级原分组项失败: %v", err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: bad.ID, Priority: 0}).Error; err != nil {
		t.Fatalf("建 bad-model 分组项失败: %v", err)
	}

	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != 200 {
		t.Fatalf("假死目标应被快速跳过并 failover 成功，实际 %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&stallCalls); got != 1 {
		t.Errorf("假死目标只应被尝试 1 次（不重试），实际 %d", got)
	}
	if got := atomic.LoadInt32(&okCalls); got != 1 {
		t.Errorf("下一家应接住流量，实际调用 %d 次", got)
	}
}

func TestRelayStreamRetryBeforeFirstByte(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1

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

	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
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
	bs := NewBreakerStore(pc)

	id := int64(999)
	if allow, _ := bs.Allow(id); !allow {
		t.Fatal("初始应放行")
	}
	for i := 0; i < 3; i++ {
		bs.Record(id, true)
	}
	allow, reason := bs.Allow(id)
	if allow {
		t.Fatal("连续失败达阈值后应开路")
	}
	if reason == "" {
		t.Fatal("开路拒绝时应返回可读原因（剩余冷却时间），供 503 日志定位")
	}

	// 把开路时间回拨到冷却期之前 → 半开探测
	v, _ := bs.m.Load(id)
	v.(*circuitBreaker).openedAt = time.Now().Add(-time.Minute)
	if allow, _ := bs.Allow(id); !allow {
		t.Fatal("冷却期后应半开放行")
	}
	bs.Record(id, false)
	bs.Record(id, false)
	b2, _ := bs.m.Load(id)
	if b2.(*circuitBreaker).state != stateClosed {
		t.Errorf("半开连续成功应转关闭，实际 state=%d", b2.(*circuitBreaker).state)
	}
}

// TestCircuitBreakerStallFastOpen 锁定假死快速熔断：连响应头都不吐的目标
// 连续 2 次即开路（普通阈值 4 次要拖垮两轮完整请求）；普通失败不清假死趋势，
// 成功才清零。
func TestCircuitBreakerStallFastOpen(t *testing.T) {
	bs := NewBreakerStore(shared.DefaultProxyConfig()) // 普通阈值 CircuitFailureThreshold=4

	// 连续两次假死 → 快速开路
	id := int64(888)
	bs.RecordStall(id)
	if allow, _ := bs.Allow(id); !allow {
		t.Fatal("单次假死不应开路")
	}
	bs.RecordStall(id)
	if allow, _ := bs.Allow(id); allow {
		t.Fatal("连续两次假死应按 stallOpenThreshold 快速开路")
	}

	// 假死与普通失败混发：普通失败（5xx，上游有回话）不清零假死计数
	id2 := int64(889)
	bs.RecordStall(id2)
	bs.Record(id2, true)
	if allow, _ := bs.Allow(id2); !allow {
		t.Fatal("一次假死 + 一次普通失败不应达到快速开路条件")
	}
	bs.RecordStall(id2)
	if allow, _ := bs.Allow(id2); allow {
		t.Fatal("第二次假死应开路（假死计数不被普通失败清零）")
	}

	// 成功清零：假死 → 成功 → 孤立假死，仍是单次计数不开路
	id3 := int64(890)
	bs.RecordStall(id3)
	bs.Record(id3, false)
	bs.RecordStall(id3)
	if allow, _ := bs.Allow(id3); !allow {
		t.Fatal("成功应清零假死计数，第二次孤立假死不应开路")
	}
}

// TestRelayCircuitSkipsOpenModel 锁定 503「所有渠道暂不可用」的产生条件：
// 分组全部目标模型的熔断器都开路时，请求不打上游直接 503。
func TestRelayCircuitSkipsOpenModel(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	pc.CircuitFailureThreshold = 1

	var calls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	var bs *BreakerStore
	r, key, logs := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		bs = d.Breakers // 捕获注入的熔断 store，供预置开路
	}, protocol.ProviderOpenAI, upstream)

	// 拿到 setupGateway 建的模型 ID，直接制造开路态
	var m model.Model
	if err := shared.DB.First(&m).Error; err != nil {
		t.Fatalf("查询模型失败: %v", err)
	}
	bs.Record(m.ID, true) // 阈值 1，一次即开路
	if allow, _ := bs.Allow(m.ID); allow {
		t.Fatal("开路的模型在冷却期内应被 Allow 拒绝")
	}

	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("全部模型开路应返回 503，实际 %d", w.Code)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("开路模型不应再调用上游，实际调用 %d 次", got)
	}

	// 503 必须落库（err_kind=circuit_open）：此前只写 stdout，管理端日志页零痕迹，
	// 用户报障只能登服务器翻容器日志。写入走注入的 LogWrite，断言查内存记录。
	entry := logs.last()
	if entry == nil {
		t.Fatalf("503 应落库")
	}
	if entry.Success {
		t.Fatalf("503 落库应为失败记录")
	}
	if entry.Status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", entry.Status)
	}
	if entry.ErrKind != "circuit_open" {
		t.Errorf("err_kind = %q, want circuit_open", entry.ErrKind)
	}
	if entry.GroupName != "my-model" {
		t.Errorf("group_name = %q, want my-model", entry.GroupName)
	}
	if !strings.Contains(entry.ErrMsg, "熔断") {
		t.Errorf("err_msg 应包含被跳过目标的熔断详情: %q", entry.ErrMsg)
	}
}

// TestRelayCircuitIsolatedPerModel 锁定模型级熔断粒度：同一渠道上一个模型连续失败
// 熔断后，failover 只跳过该模型，同渠道其他模型照常参与。若按渠道熔断（旧行为），
// 单渠道分组会在一个模型开路后整体被跳过，请求 503「所有渠道暂不可用」。
func TestRelayCircuitIsolatedPerModel(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	pc.CircuitFailureThreshold = 1

	var badCalls, goodCalls int32
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if req.Model == "bad-model" {
			atomic.AddInt32(&badCalls, 1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		atomic.AddInt32(&goodCalls, 1)
		w.Write([]byte(`{"id":"1","object":"chat.completion","model":"good-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})

	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	// 同渠道再种 bad-model，并把它插为分组首选（priority 0），原模型降为 priority 1
	var ch channel.Channel
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatalf("查询渠道失败: %v", err)
	}
	bad := model.Model{ChannelID: ch.ID, Name: "bad-model"}
	if err := shared.DB.Create(&bad).Error; err != nil {
		t.Fatalf("建 bad-model 失败: %v", err)
	}
	var g group.Group
	if err := shared.DB.Where("name = ?", "my-model").First(&g).Error; err != nil {
		t.Fatalf("查询分组失败: %v", err)
	}
	var firstItem group.GroupItem
	if err := shared.DB.Where("group_id = ?", g.ID).First(&firstItem).Error; err != nil {
		t.Fatalf("查询分组项失败: %v", err)
	}
	if err := shared.DB.Model(&firstItem).Update("priority", 1).Error; err != nil {
		t.Fatalf("降级原分组项失败: %v", err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: bad.ID, Priority: 0}).Error; err != nil {
		t.Fatalf("建 bad-model 分组项失败: %v", err)
	}

	// 第一次请求：bad-model 500（阈值 1 → 开路），failover 到 good-model 成功
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)
	if w.Code != 200 {
		t.Fatalf("首次请求应 failover 成功，实际 %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&badCalls); got != 1 {
		t.Errorf("bad-model 应被调用 1 次，实际 %d", got)
	}

	// 第二次请求：bad-model 已开路，应被跳过直接走 good-model；
	// 渠道级熔断会在这里连 good-model 一起跳过 → 503
	w2 := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)
	if w2.Code != 200 {
		t.Fatalf("bad-model 熔断后 good-model 应照常服务，实际 %d, body=%s", w2.Code, w2.Body.String())
	}
	if got := atomic.LoadInt32(&badCalls); got != 1 {
		t.Errorf("开路后 bad-model 不应再被调用，实际累计 %d 次", got)
	}
	if got := atomic.LoadInt32(&goodCalls); got != 2 {
		t.Errorf("good-model 应两次都接住流量，实际 %d", got)
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
