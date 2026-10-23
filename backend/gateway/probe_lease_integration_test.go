package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// Expire only the test model's existing open circuit. No real cooldown sleeps.
func expireProbeCircuit(t *testing.T, bs *BreakerStore, id int64) {
	t.Helper()
	v, ok := bs.m.Load(id)
	if !ok {
		t.Fatal("missing breaker")
	}
	b := v.(*circuitBreaker)
	b.mu.Lock()
	b.openedAt = time.Now().Add(-time.Duration(bs.cfg.CircuitResetSeconds+1) * time.Second)
	b.mu.Unlock()
}

func seedExpiredProbeCircuit(t *testing.T, bs *BreakerStore, id int64) {
	t.Helper()
	for n := 0; n < bs.cfg.CircuitFailureThreshold; n++ {
		bs.Record(id, true)
	}
	expireProbeCircuit(t, bs, id)
}

func waitProbeSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("probe barrier timed out")
	}
}

func TestProbeLeaseConcurrentManualAndFailover(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "failover"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.CircuitFailureThreshold = 1
			pc.CircuitSuccessThreshold = 2
			pc.RetryCount = 2
			pc.RetryBackoffBaseMs = 1
			pc.RetryBackoffMaxMs = 1
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var primary, fallback atomic.Int32
			var breakers *BreakerStore
			attempts := &attemptRecorder{}
			r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if rateLimitModelName(t, r) == "upstream-model" {
					if primary.Add(1) == 1 {
						close(entered)
						<-release
					}
				} else {
					fallback.Add(1)
				}
				io.WriteString(w, budgetChatSuccess)
			}))
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			c, m, g := rateLimitSeed(t)
			backup := rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
			if manual {
				var item group.GroupItem
				if err := shared.DB.Where("group_id = ? AND model_id = ?", g.ID, m.ID).First(&item).Error; err != nil {
					t.Fatal(err)
				}
				if err := shared.DB.Model(&g).Updates(map[string]any{"strategy": group.StrategyManual, "active_item_id": item.ID}).Error; err != nil {
					t.Fatal(err)
				}
			}
			// A second group references the identical model, hence the identical lease.
			other := group.Group{Name: "other-group", Strategy: g.Strategy}
			if manual {
				other.Strategy = group.StrategyManual
			}
			if err := shared.DB.Create(&other).Error; err != nil {
				t.Fatal(err)
			}
			first := group.GroupItem{GroupID: other.ID, ModelID: m.ID, Priority: 0}
			if err := shared.DB.Create(&first).Error; err != nil {
				t.Fatal(err)
			}
			if err := shared.DB.Create(&group.GroupItem{GroupID: other.ID, ModelID: backup.ID, Priority: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if manual {
				if err := shared.DB.Model(&other).Update("active_item_id", first.ID).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedExpiredProbeCircuit(t, breakers, m.ID)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- doReq(t, r, "/v1/chat/completions", rateLimitRequest, key) }()
			waitProbeSignal(t, entered)
			const n = 12
			var wg sync.WaitGroup
			statuses := make(chan int, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					body := rateLimitRequest
					if i%2 == 1 {
						body = strings.Replace(body, "my-model", "other-group", 1)
					}
					statuses <- doReq(t, r, "/v1/chat/completions", body, key).Code
				}(i)
			}
			wg.Wait()
			close(statuses)
			want := 200
			if manual {
				want = 503
			}
			for status := range statuses {
				if status != want {
					t.Errorf("busy primary status=%d want=%d", status, want)
				}
			}
			if primary.Load() != 1 {
				t.Errorf("concurrent primary calls=%d want=1", primary.Load())
			}
			rows := attempts.entries()
			if manual {
				if fallback.Load() != 0 || len(rows) != 0 {
					t.Errorf("manual switched or invented attempts: fallback=%d rows=%+v", fallback.Load(), rows)
				}
			} else {
				if fallback.Load() != n || len(rows) != n {
					t.Errorf("fallback=%d attempts=%d want=%d", fallback.Load(), len(rows), n)
				}
				for _, row := range rows {
					if row.Sequence != 1 {
						t.Errorf("skip consumed attempt sequence: %+v", row)
					}
				}
			}
			releaseOnce.Do(func() { close(release) })
			if w := <-done; w.Code != 200 {
				t.Fatalf("probe status=%d body=%s", w.Code, w.Body.String())
			}
			v, _ := breakers.m.Load(m.ID)
			b := v.(*circuitBreaker)
			b.mu.Lock()
			state, successes := b.state, b.consecutiveSuccess
			b.mu.Unlock()
			if state != stateHalfOpen || successes != 1 {
				t.Fatalf("one success prematurely recovered state=%v successes=%d", state, successes)
			}
			for i := 0; i < 2; i++ {
				if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 {
					t.Fatalf("recovery request=%d", w.Code)
				}
			}
			b.mu.Lock()
			state = b.state
			b.mu.Unlock()
			if state != stateClosed || primary.Load() != 3 {
				t.Fatalf("recovery/fixed priority state=%v primary=%d", state, primary.Load())
			}
		})
	}
}

func TestProbeLeaseFailureImmediatelyReopensWithoutSameTargetRetry(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 4
	pc.RetryCount = 3
	pc.RetryBackoffBaseMs = 1
	pc.RetryBackoffMaxMs = 1
	var primary, fallback atomic.Int32
	var breakers *BreakerStore
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitModelName(t, r) == "upstream-model" {
			primary.Add(1)
			w.WriteHeader(500)
			io.WriteString(w, `{"error":{"message":"failed"}}`)
			return
		}
		fallback.Add(1)
		io.WriteString(w, budgetChatSuccess)
	}))
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	for i := 0; i < 2; i++ {
		if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if primary.Load() != 1 || fallback.Load() != 2 {
		t.Fatalf("primary=%d fallback=%d", primary.Load(), fallback.Load())
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	state := b.state
	b.mu.Unlock()
	if state != stateOpen {
		t.Fatalf("failed probe state=%v", state)
	}
}

func TestProbeLeaseResourceFailureReleasesWithoutRecovery(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	pc.ResourceLimits.MaxResponseBytes = 512
	var calls atomic.Int32
	var breakers *BreakerStore
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			io.WriteString(w, strings.Repeat("x", 513))
			return
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	_, m, _ := rateLimitSeed(t)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 502 {
		t.Fatalf("resource status=%d body=%s", w.Code, w.Body.String())
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	state, successes := b.state, b.consecutiveSuccess
	b.mu.Unlock()
	if state != stateHalfOpen || successes != 0 {
		t.Fatalf("resource counted as recovery state=%v successes=%d", state, successes)
	}
	w = doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("neutral lease not released status=%d calls=%d", w.Code, calls.Load())
	}
	b.mu.Lock()
	state, successes = b.state, b.consecutiveSuccess
	b.mu.Unlock()
	if state != stateHalfOpen || successes != 1 {
		t.Fatalf("wrong recovery accounting state=%v successes=%d", state, successes)
	}
}

func TestProbeLeaseHoldsAcrossCompatibilityHTTPAttempts(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	pc.RetryBackoffBaseMs = 1
	pc.RetryBackoffMaxMs = 1
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var primary, fallback atomic.Int32
	var breakers *BreakerStore
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitModelName(t, r) == "upstream-model" {
			n := primary.Add(1)
			if n == 1 {
				w.WriteHeader(400)
				io.WriteString(w, budgetUnsupportedLegacy)
				return
			}
			if n == 2 {
				close(entered)
				<-release
			}
		} else {
			fallback.Add(1)
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
	}()
	waitProbeSignal(t, entered)
	if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 {
		t.Fatalf("fallback status=%d", w.Code)
	}
	if primary.Load() != 2 || fallback.Load() != 1 {
		t.Fatalf("compat lease released early primary=%d fallback=%d", primary.Load(), fallback.Load())
	}
	once.Do(func() { close(release) })
	if w := <-done; w.Code != 200 {
		t.Fatalf("compat result=%d body=%s", w.Code, w.Body.String())
	}
	rows := attempts.entries()
	if len(rows) != 3 {
		t.Fatalf("attempts=%+v", rows)
	}
	var compat int
	for _, row := range rows {
		if row.Reason == "budget_compat" {
			compat++
		}
	}
	if compat != 1 {
		t.Fatalf("compat attempt evidence=%+v", rows)
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	successes := b.consecutiveSuccess
	b.mu.Unlock()
	if successes != 1 {
		t.Fatalf("two HTTP attempts counted as %d recoveries", successes)
	}
}

func TestProbeLeaseStreamRemainsExclusiveAfterDelivery(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var primary, fallback atomic.Int32
	var breakers *BreakerStore
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitModelName(t, r) == "upstream-model" {
			if primary.Add(1) != 1 {
				io.WriteString(w, budgetChatSuccess)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, rateLimitContent)
			w.(http.Flusher).Flush()
			close(entered)
			<-release
			io.WriteString(w, deliveryFinish+"data: [DONE]\n\n")
			return
		}
		fallback.Add(1)
		io.WriteString(w, budgetChatSuccess)
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
	}()
	waitProbeSignal(t, entered)
	for i := 0; i < 3; i++ {
		if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 {
			t.Fatalf("fallback status=%d", w.Code)
		}
	}
	if primary.Load() != 1 || fallback.Load() != 3 {
		t.Fatalf("active stream lost lease primary=%d fallback=%d", primary.Load(), fallback.Load())
	}
	once.Do(func() { close(release) })
	w := <-done
	if w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("stream result=%d body=%s", w.Code, w.Body.String())
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	successes := b.consecutiveSuccess
	b.mu.Unlock()
	if successes != 1 {
		t.Fatalf("stream successes=%d", successes)
	}
}

func TestProbeLeaseCancellationReleasesWithoutRecovery(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	var breakers *BreakerStore
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		breakers = d.Breakers
		d.AttemptWrite = attempts.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				cancel()
				return nil, req.Context().Err()
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(budgetChatSuccess))}, nil
		})}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected actual transport") }))
	_, m, _ := rateLimitSeed(t)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(rateLimitRequest)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	rows := attempts.entries()
	if calls.Load() != 1 || len(rows) != 1 || rows[0].ErrKind != shared.ErrKindClientCancel {
		t.Fatalf("cancellation replayed calls=%d attempts=%+v", calls.Load(), rows)
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	state, successes := b.state, b.consecutiveSuccess
	b.mu.Unlock()
	if state != stateHalfOpen || successes != 0 {
		t.Fatalf("cancelled probe counted as recovery state=%v successes=%d", state, successes)
	}
	w = doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("cancelled lease not released status=%d calls=%d", w.Code, calls.Load())
	}
	b.mu.Lock()
	successes = b.consecutiveSuccess
	b.mu.Unlock()
	if successes != 1 {
		t.Fatalf("cancel changed success accounting: %d", successes)
	}
}

func TestProbeLeaseRateLimitIsNeutralAndCooldownDoesNotConsumeAttempts(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	cooldowns := NewRateLimitStore(pc)
	cooldowns.now = func() time.Time { return time.Unix(0, now.Load()) }
	var calls atomic.Int32
	var breakers *BreakerStore
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		breakers = d.Breakers
		d.Cooldowns = cooldowns
		d.AttemptWrite = attempts.write
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"busy"}}`)
			return
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	_, m, _ := rateLimitSeed(t)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	for i := 0; i < 2; i++ {
		w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
		if w.Code != 429 {
			t.Fatalf("rate limit status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 || len(attempts.entries()) != 1 {
		t.Fatalf("cooldown consumed attempts calls=%d rows=%+v", calls.Load(), attempts.entries())
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	state, successes := b.state, b.consecutiveSuccess
	b.mu.Unlock()
	if state != stateHalfOpen || successes != 0 {
		t.Fatalf("429 counted as health recovery state=%v successes=%d", state, successes)
	}
	now.Add(int64(2 * time.Second))
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("lease/cooldown not released status=%d calls=%d", w.Code, calls.Load())
	}
	rows := attempts.entries()
	if len(rows) != 2 || rows[1].Sequence != 1 {
		t.Fatalf("cooldown skip invented budget/attempt: %+v", rows)
	}
	b.mu.Lock()
	successes = b.consecutiveSuccess
	b.mu.Unlock()
	if successes != 1 {
		t.Fatalf("429 changed success accounting: %d", successes)
	}
}

func TestProbeLeaseCompatibilityBudgetStopReleasesWithoutRecovery(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	pc.MaxHTTPAttempts = 1
	var calls atomic.Int32
	var breakers *BreakerStore
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	_, m, _ := rateLimitSeed(t)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
	if w.Code != 502 || calls.Load() != 1 {
		t.Fatalf("compat budget stop status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
	rows := attempts.entries()
	if len(rows) != 1 || rows[0].Status != 400 {
		t.Fatalf("budget rejection invented HTTP attempt: %+v", rows)
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	state, successes := b.state, b.consecutiveSuccess
	b.mu.Unlock()
	if state != stateHalfOpen || successes != 0 {
		t.Fatalf("budget stop changed health state=%v successes=%d", state, successes)
	}
	w = doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("budget stop leaked permit status=%d calls=%d", w.Code, calls.Load())
	}
}

func TestProbeLeaseReleasedBeforeBlockedAttemptPersistence(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls, writes atomic.Int32
	var breakers *BreakerStore
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		breakers = d.Breakers
		d.AttemptWrite = func(*shared.UpstreamAttempt) error {
			if writes.Add(1) == 1 {
				close(entered)
				<-release
			}
			return nil
		}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, budgetChatSuccess) }))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	_, m, _ := rateLimitSeed(t)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- doReq(t, r, "/v1/chat/completions", rateLimitRequest, key) }()
	waitProbeSignal(t, entered)
	// Observability latency is outside the physical upstream probe lifetime.
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("attempt persistence retained permit status=%d calls=%d", w.Code, calls.Load())
	}
	once.Do(func() { close(release) })
	if w := <-done; w.Code != 200 {
		t.Fatalf("first result=%d", w.Code)
	}
}

func TestProbeLeaseClientWriteFailureReleasesWithoutRecoveryOrReplay(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	pc.CircuitSuccessThreshold = 2
	pc.RetryCount = 3
	var primary, fallback atomic.Int32
	var breakers *BreakerStore
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitModelName(t, r) == "upstream-model" {
			primary.Add(1)
		} else {
			fallback.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, rateLimitContent+deliveryFinish+"data: [DONE]\n\n")
	}))
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	body := `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	w := &rateLimitClientFailureWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: true}
	r.ServeHTTP(w, req)
	if primary.Load() != 1 || fallback.Load() != 0 || len(attempts.entries()) != 1 {
		t.Fatalf("client delivery failure replayed primary=%d fallback=%d rows=%+v", primary.Load(), fallback.Load(), attempts.entries())
	}
	v, _ := breakers.m.Load(m.ID)
	b := v.(*circuitBreaker)
	b.mu.Lock()
	state, successes := b.state, b.consecutiveSuccess
	b.mu.Unlock()
	if state != stateHalfOpen || successes != 0 {
		t.Fatalf("client write counted as recovery state=%v successes=%d", state, successes)
	}
	result := doReq(t, r, "/v1/chat/completions", body, key)
	if result.Code != 200 || primary.Load() != 2 {
		t.Fatalf("client write leaked lease status=%d primary=%d", result.Code, primary.Load())
	}
	b.mu.Lock()
	successes = b.consecutiveSuccess
	b.mu.Unlock()
	if successes != 1 {
		t.Fatalf("client write changed success count=%d", successes)
	}
}
