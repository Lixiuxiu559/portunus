package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
)

const rateLimitRequest = `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`
const rateLimitSSE = "data: {\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit_exceeded\",\"message\":\"busy\"}}\n\n"
const rateLimitContent = "data: {\"id\":\"ok\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n"

// The shared fixture seeds one channel/model/group; additions keep explicit priorities.
func rateLimitSeed(t *testing.T) (channel.Channel, model.Model, group.Group) {
	t.Helper()
	var c channel.Channel
	var m model.Model
	var g group.Group
	for _, row := range []any{&c, &m, &g} {
		if err := shared.DB.First(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return c, m, g
}

func rateLimitAdd(t *testing.T, c channel.Channel, g group.Group, name string, priority int) model.Model {
	t.Helper()
	m := model.Model{ChannelID: c.ID, Name: name}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: priority}).Error; err != nil {
		t.Fatal(err)
	}
	return m
}

func rateLimitOtherChannel(t *testing.T, first channel.Channel) channel.Channel {
	t.Helper()
	c := channel.Channel{Name: "fallback", Type: first.Type, BaseURL: first.BaseURL, Key: "fallback-key"}
	if err := shared.DB.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func rateLimitModelName(t *testing.T, r *http.Request) string {
	t.Helper()
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
	}
	return req.Model
}

func TestRateLimitManualSkipsRetryAndUsesCooldown(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount, pc.CircuitFailureThreshold = 3, 1
	pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
	var calls atomic.Int32
	recorder := &attemptRecorder{}
	var breakers *BreakerStore
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		breakers = d.Breakers
		d.AttemptWrite = recorder.write
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	var item group.GroupItem
	if err := shared.DB.Where("group_id = ? AND model_id = ?", g.ID, m.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Model(&g).Updates(map[string]any{"strategy": group.StrategyManual, "active_item_id": item.ID}).Error; err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
		if w.Code != 429 {
			t.Fatalf("request %d status=%d body=%s", n, w.Code, w.Body.String())
		}
		if wait, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || wait < 1 || wait > 17 {
			t.Fatalf("request %d retry-after=%q", n, w.Header().Get("Retry-After"))
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("manual/cooldown sent %d requests, want 1", calls.Load())
	}
	rows := recorder.entries()
	if len(rows) != 1 || rows[0].Status != 429 || rows[0].Success {
		t.Fatalf("actual attempts=%+v", rows)
	}
	if allowed, _ := breakers.Allow(m.ID); !allowed {
		t.Fatal("429 must not open breaker")
	}
}

func TestRateLimitFailoverOrderCooldownAndExpiry(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	pc.CircuitFailureThreshold = 1
	pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	store := NewRateLimitStore(pc)
	store.now = func() time.Time { return time.Unix(0, clock.Load()) }
	recorder := &attemptRecorder{}
	var mu sync.Mutex
	var order []string
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.Cooldowns = store; d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := rateLimitModelName(t, r)
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
		if name == "upstream-model" {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	c, _, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	for n := 0; n < 3; n++ {
		if n == 2 {
			clock.Add(int64(2 * time.Second))
		}
		w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
		if w.Code != 200 {
			t.Fatalf("request %d: %d %s", n, w.Code, w.Body.String())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"upstream-model", "fallback-model", "fallback-model", "upstream-model", "fallback-model"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("fixed order=%v, want %v", order, want)
	}
	rows := recorder.entries()
	if len(rows) != 5 {
		t.Fatalf("cooldown skip generated attempts: %+v", rows)
	}
	for i, wantStatus := range []int{429, 200, 200, 429, 200} {
		if rows[i].Status != wantStatus {
			t.Fatalf("attempt %d=%+v", i, rows[i])
		}
	}
	if rows[2].Sequence != 1 {
		t.Fatalf("skipped target consumed request budget/sequence: %+v", rows[2])
	}
}

func TestRateLimitScopeKeepsOtherModelsOrSkipsChannel(t *testing.T) {
	for _, scope := range []string{"model", "channel"} {
		t.Run(scope, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 2
			pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
			var mu sync.Mutex
			var order []string
			r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := rateLimitModelName(t, r)
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				if name == "upstream-model" {
					w.WriteHeader(429)
					io.WriteString(w, `{"error":{"message":"channel quota account limit model limit"}}`)
					return
				}
				io.WriteString(w, budgetChatSuccess)
			}))
			c, _, g := rateLimitSeed(t)
			if err := shared.DB.Model(&c).Update("rate_limit_scope", scope).Error; err != nil {
				t.Fatal(err)
			}
			rateLimitAdd(t, c, g, "same-channel-model", 1)
			rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "other-channel-model", 2)
			for n := 0; n < 2; n++ {
				w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
				if w.Code != 200 {
					t.Fatalf("request %d: %d %s", n, w.Code, w.Body.String())
				}
			}
			target := "same-channel-model"
			if scope == "channel" {
				target = "other-channel-model"
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"upstream-model", target, target}; !reflect.DeepEqual(order, want) {
				t.Fatalf("scope=%s calls=%v want=%v", scope, order, want)
			}
		})
	}
}

func TestRateLimitIdentityChangesDoNotReuseStaleCooldown(t *testing.T) {
	for _, change := range []string{"key", "name", "base_url", "rate_limit_scope"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			r, key, _ := setupGatewayDeps(t, nil, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(429)
				io.WriteString(w, `{"error":{"message":"busy"}}`)
			}))
			c, _, _ := rateLimitSeed(t)
			w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
			if w.Code != 429 {
				t.Fatal(w.Code)
			}
			var value any
			switch change {
			case "key":
				value = "replacement-key"
			case "name":
				value = "renamed"
			case "base_url":
				value = c.BaseURL + "/"
			case "rate_limit_scope":
				value = "channel"
			}
			if err := shared.DB.Model(&c).Update(change, value).Error; err != nil {
				t.Fatal(err)
			}
			w = doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
			if w.Code != 429 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			want := int32(2)
			if change == "name" {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("change=%s calls=%d want=%d", change, calls.Load(), want)
			}
		})
	}
}

func TestRateLimitCompatibilityRetryStopsAt429(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
	var calls atomic.Int32
	recorder := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		}
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
	if w.Code != 429 || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
	rows := recorder.entries()
	if len(rows) != 2 || rows[0].Status != 400 || rows[1].Status != 429 || rows[1].Reason != "budget_compat" {
		t.Fatalf("attempts=%+v", rows)
	}
	w = doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
	if w.Code != 429 || calls.Load() != 2 || len(recorder.entries()) != 2 {
		t.Fatalf("cooldown repeated compat retry status=%d calls=%d", w.Code, calls.Load())
	}
}

func TestRateLimitSSEPreservesDeliverySafety(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(strconv.FormatBool(delivered), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 3
			pc.CircuitFailureThreshold = 1
			pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
			var mu sync.Mutex
			var order []string
			recorder := &attemptRecorder{}
			var breakers *BreakerStore
			r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := rateLimitModelName(t, r)
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if name == "upstream-model" {
					if delivered {
						io.WriteString(w, rateLimitContent)
						w.(http.Flusher).Flush()
					}
					io.WriteString(w, rateLimitSSE)
					return
				}
				io.WriteString(w, rateLimitContent+deliveryFinish+"data: [DONE]\n\n")
			}))
			c, m, g := rateLimitSeed(t)
			rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
			w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			mu.Lock()
			got := append([]string(nil), order...)
			mu.Unlock()
			want := []string{"upstream-model", "fallback-model"}
			if delivered {
				want = want[:1]
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("delivered=%t calls=%v want=%v", delivered, got, want)
			}
			if strings.Contains(w.Body.String(), "[DONE]") == delivered {
				t.Fatalf("wrong terminal delivered=%t body=%s", delivered, w.Body.String())
			}
			if allowed, _ := breakers.Allow(m.ID); !allowed {
				t.Fatal("SSE 429 must not trip breaker")
			}
			rows := recorder.entries()
			if len(rows) != len(want) || rows[0].Status != 200 || rows[0].Success {
				t.Fatalf("source HTTP evidence lost: %+v", rows)
			}
			// A source-200 error must cool the source exactly as an HTTP 429 does.
			w = doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("fallback after SSE cooldown failed: status=%d body=%s", w.Code, w.Body.String())
			}
			mu.Lock()
			got = append([]string(nil), order...)
			mu.Unlock()
			if got[len(got)-1] != "fallback-model" || len(got) != len(want)+1 {
				t.Fatalf("SSE 429 did not cool source: %v", got)
			}
		})
	}
}

func TestRateLimitOversized429StopsWholeRequest(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	pc.CircuitFailureThreshold = 1
	pc.ResourceLimits.MaxResponseBytes = 64
	var calls atomic.Int32
	var breakers *BreakerStore
	recorder := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		io.WriteString(w, strings.Repeat("x", 65))
	}))
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 502 || calls.Load() != 1 {
		t.Fatalf("resource safety bypassed status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
	rows := recorder.entries()
	if len(rows) != 1 || rows[0].Status != 429 || rows[0].ErrKind != shared.ErrKindResourceLimit {
		t.Fatalf("actual attempt=%+v", rows)
	}
	if allowed, _ := breakers.Allow(m.ID); !allowed {
		t.Fatal("resource error tripped breaker")
	}
}

func TestRateLimitAllCoolingAndMixedBreakerCreateNoAttempts(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(strconv.FormatBool(mixed), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount, pc.CircuitFailureThreshold = 2, 1
			pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
			var calls atomic.Int32
			var breakers *BreakerStore
			recorder := &attemptRecorder{}
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(pc)(d)
				breakers, d.AttemptWrite = d.Breakers, recorder.write
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "19")
				w.WriteHeader(429)
				io.WriteString(w, `{"error":{"message":"busy"}}`)
			}))
			c, _, g := rateLimitSeed(t)
			fallback := rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
			if mixed {
				breakers.Record(fallback.ID, true)
			}
			for n := 0; n < 2; n++ {
				w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
				if w.Code != 429 {
					t.Fatalf("request %d status=%d body=%s", n, w.Code, w.Body.String())
				}
				if n == 1 {
					wait, err := strconv.Atoi(w.Header().Get("Retry-After"))
					if err != nil || wait <= 0 || wait > 19 {
						t.Fatalf("local cooldown retry-after=%q", w.Header().Get("Retry-After"))
					}
				}
			}
			want := int32(2)
			if mixed {
				want = 1
			}
			if calls.Load() != want || len(recorder.entries()) != int(want) {
				t.Fatalf("mixed=%t calls=%d attempts=%+v", mixed, calls.Load(), recorder.entries())
			}
		})
	}
}

func TestRateLimitUnsafeRequestNeverFailsOver(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount, pc.CircuitFailureThreshold = 3, 1
	var calls atomic.Int32
	var breakers *BreakerStore
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		breakers = d.Breakers
	}, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"busy"}}`)
	}))
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"click button","tools":[{"type":"computer_use_preview"}]}`, key)
	if w.Code != 429 || calls.Load() != 1 {
		t.Fatalf("unsafe replay status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
	if allowed, _ := breakers.Allow(m.ID); !allowed {
		t.Fatal("unsafe 429 must not trip breaker")
	}
}

// Hold requestControl's lock after the pre-wait cooldown check. This barrier lets a
// separate request record a cooldown before waitForAttempt completes, without sleeps.
func TestRateLimitRechecksAfterRetryBackoffWithoutConsumingBudget(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1
	pc.RetryBackoffBaseMs, pc.RetryBackoffMaxMs = 1, 1
	store := NewRateLimitStore(pc)
	var calls atomic.Int32
	var control *requestControl
	var target router.Target
	entered, recorded := make(chan struct{}), make(chan struct{})
	var arm atomic.Bool
	now := time.Now()
	store.now = func() time.Time {
		if arm.CompareAndSwap(true, false) {
			// The first post-response Check is the pre-wait guard for the retry.
			control.mu.Lock()
			close(entered)
		}
		return now
	}
	recorder := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		d.Cooldowns, d.AttemptWrite = store, recorder.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			calls.Add(1)
			control = controlFrom(req.Context())
			target = req.Context().Value(rateLimitTargetContextKey{}).(router.Target)
			arm.Store(true)
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`))}, nil
		})}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server call") }))
	go func() {
		<-entered
		store.Record(target, "30")
		control.mu.Unlock()
		close(recorded)
	}()
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	select {
	case <-recorded:
	case <-time.After(time.Second):
		t.Fatal("cooldown barrier did not complete")
	}
	if w.Code != 429 || calls.Load() != 1 || len(recorder.entries()) != 1 {
		t.Fatalf("late cooldown sent retry status=%d calls=%d attempts=%+v", w.Code, calls.Load(), recorder.entries())
	}
	control.mu.Lock()
	used := control.used
	control.mu.Unlock()
	if used != 1 {
		t.Fatalf("blocked retry consumed budget: %d", used)
	}
}

func TestRateLimitClientCancellationWinsBeforeNextSend(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount, pc.CircuitFailureThreshold = 3, 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	recorder := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		d.AttemptWrite = recorder.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			calls.Add(1)
			cancel()
			return &http.Response{StatusCode: 429, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`))}, nil
		})}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server call") }))
	c, _, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(rateLimitRequest)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	rows := recorder.entries()
	if calls.Load() != 1 || len(rows) != 1 || rows[0].ErrKind != shared.ErrKindClientCancel {
		t.Fatalf("cancel was replayed/reclassified status=%d calls=%d attempts=%+v", w.Code, calls.Load(), rows)
	}
}

func TestRateLimitSlow429SharesCooldownAcrossGroupsAndPreservesBudget(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.MaxHTTPAttempts, pc.RetryCount = 1, 2
	store := NewRateLimitStore(pc)
	var arm atomic.Bool
	recorded, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	store.now = func() time.Time {
		if arm.CompareAndSwap(true, false) {
			close(recorded)
		}
		return time.Now()
	}
	var primary, fallback atomic.Int32
	recorder := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		d.Cooldowns, d.AttemptWrite = store, recorder.write
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitModelName(t, r) == "upstream-model" {
			primary.Add(1)
			arm.Store(true)
			w.WriteHeader(429)
			w.(http.Flusher).Flush()
			<-release
			io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		fallback.Add(1)
		io.WriteString(w, budgetChatSuccess)
	}))
	c, m, g := rateLimitSeed(t)
	backup := rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	other := group.Group{Name: "other-group", Strategy: group.StrategyFailover}
	if err := shared.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for i, id := range []int64{m.ID, backup.ID} {
		if err := shared.DB.Create(&group.GroupItem{GroupID: other.ID, ModelID: id, Priority: i}).Error; err != nil {
			t.Fatal(err)
		}
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	}()
	select {
	case <-recorded:
	case <-time.After(time.Second):
		t.Fatal("429 response headers did not record cooldown before body finished")
	}
	// First request still waits on its 429 body. The other group references the
	// same saved model, so its only allowed Do must go directly to the backup.
	w := doReq(t, r, "/v1/chat/completions", strings.Replace(rateLimitRequest, "my-model", "other-group", 1), key)
	if w.Code != 200 || primary.Load() != 1 || fallback.Load() != 1 {
		t.Errorf("cooldown missing/budget consumed: status=%d primary=%d fallback=%d body=%s", w.Code, primary.Load(), fallback.Load(), w.Body.String())
	}
	rows := recorder.entries()
	if len(rows) != 1 || rows[0].Sequence != 1 || rows[0].ModelID != backup.ID {
		t.Errorf("skip created attempt or charged budget: %+v", rows)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("slow upstream did not finish")
	}
	if primary.Load() != 1 || fallback.Load() != 1 {
		t.Fatalf("first request exceeded single-Do budget: primary=%d fallback=%d", primary.Load(), fallback.Load())
	}
}

// A late cooldown must not hide an exhausted request budget on the compatibility path.
func TestRateLimitBudgetStopWinsBeforeCompatibilityCooldown(t *testing.T) {
	cfg := shared.DefaultProxyConfig()
	cfg.MaxHTTPAttempts = 1
	target := cooldownTarget()
	store := NewRateLimitStore(cfg)
	control := newRelayRequestControl(context.Background(), cfg, false)
	defer control.close()
	ctx := context.WithValue(control.ctx, rateLimitTargetContextKey{}, target)
	calls := 0
	attempts := &attemptRecorder{}
	server := NewRelayServer(Deps{Cfg: cfg, Cooldowns: store, AttemptWrite: attempts.write, Client: &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		store.Record(target, "30")
		return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(budgetUnsupportedLegacy))}, nil
	})}})
	sequence := 0
	_, err := server.doRequestWithChatBudgetFallback(ctx, "http://example.invalid", make(http.Header), []byte(`{"model":"x","max_tokens":256,"messages":[]}`), true, &chatBudgetRetry{}, &relayAttemptTrace{sequence: &sequence})
	var budget *attemptBudgetError
	if !errors.As(err, &budget) || !failSpec(err).StopRequest || calls != 1 || control.used != 1 {
		t.Fatalf("hard stop lost: calls=%d used=%d err=%T %v", calls, control.used, err, err)
	}
	rows := attempts.entries()
	if len(rows) != 1 || rows[0].Status != 400 || sequence != 1 {
		t.Fatalf("compatibility evidence lost: sequence=%d rows=%+v", sequence, rows)
	}
}

type rateLimitClientFailureWriter struct {
	*httptest.ResponseRecorder
	failWrite       bool
	writes, flushes int
}

func (w *rateLimitClientFailureWriter) Write(b []byte) (int, error) {
	w.writes++
	if w.failWrite && w.writes == 2 {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(b)
}
func (w *rateLimitClientFailureWriter) FlushError() error {
	w.flushes++
	if !w.failWrite && w.flushes == 2 {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}
func TestRateLimitSourceObservationSurvivesClientDeliveryFailure(t *testing.T) {
	for _, failWrite := range []bool{true, false} {
		name := "flush"
		if failWrite {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			cfg := shared.DefaultProxyConfig()
			target := cooldownTarget()
			store := NewRateLimitStore(cfg)
			breakers := NewBreakerStore(cfg)
			attempts := &attemptRecorder{}
			var entry *shared.Log
			calls := 0
			server := NewRelayServer(Deps{Cfg: cfg, Cooldowns: store, Breakers: breakers, LogWrite: func(e *shared.Log) { entry = e }, AttemptWrite: attempts.write, Client: &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(rateLimitContent + rateLimitSSE))}, nil
			})}})
			w := &rateLimitClientFailureWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: failWrite}
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			sequence := 0
			_, status, err := server.relayToTarget(c, protocol.ProviderOpenAI, true, &group.Group{Name: "test"}, target, []byte(`{"model":"x","stream":true,"messages":[]}`), 0, "id", nil, &chatBudgetRetry{}, &relayAttemptTrace{sequence: &sequence})
			if err != nil || status != 200 || calls != 1 || w.writes != 2 {
				t.Fatalf("stream replay/result changed: err=%v status=%d calls=%d writes=%d", err, status, calls, w.writes)
			}
			if store.Check(target) == nil {
				t.Fatal("recognized source429 lost cooldown after client delivery failure")
			}
			rows := attempts.entries()
			if entry == nil || entry.ErrKind != shared.ErrKindClientWrite || entry.Success || len(rows) != 1 || rows[0].ErrKind != shared.ErrKindClientWrite || rows[0].Success {
				t.Fatalf("delivery attribution lost: log=%+v attempts=%+v", entry, rows)
			}
			assertNoLocalControlBreakerFailure(t, breakers, target.Model.ID)
			if strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatal("failed delivery manufactured success terminal")
			}
		})
	}
}
