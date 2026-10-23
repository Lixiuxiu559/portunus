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
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func channelConcurrencyCount(s *ChannelConcurrencyStore, id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[id]
}

// Headers are already available, but the relay must retain capacity until the body
// has been read and closed. A second group uses another model on the same channel.
func TestChannelConcurrencySharedAcrossModelsGroupsAndFixedFallback(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "failover"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.MaxHTTPAttempts = 1
			pc.RetryCount = 2
			store := NewChannelConcurrencyStore()
			attempts := &attemptRecorder{}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var primary, fallback atomic.Int32
			var traceMu sync.Mutex
			var traces []shared.RoutingTrace
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(pc)(d)
				d.Concurrency, d.AttemptWrite = store, attempts.write
				d.RoutingWrite = func(row *shared.RoutingTrace) error {
					cp := *row
					cp.Events = append([]shared.RoutingEvent(nil), row.Events...)
					traceMu.Lock()
					traces = append(traces, cp)
					traceMu.Unlock()
					return nil
				}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if rateLimitModelName(t, req) == "fallback-model" {
					fallback.Add(1)
					io.WriteString(w, budgetChatSuccess)
					return
				}
				if primary.Add(1) == 1 {
					io.WriteString(w, budgetChatSuccess[:1])
					w.(http.Flusher).Flush()
					close(entered)
					<-release
					io.WriteString(w, budgetChatSuccess[1:])
					return
				}
				io.WriteString(w, budgetChatSuccess)
			}))
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			c, m, g := rateLimitSeed(t)
			if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
				t.Fatal(err)
			}
			otherModel := model.Model{ChannelID: c.ID, Name: "same-channel-model"}
			if err := shared.DB.Create(&otherModel).Error; err != nil {
				t.Fatal(err)
			}
			fallbackModel := rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 2)
			// Both same-channel candidates must be skipped without spending the sole budget.
			if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: otherModel.ID, Priority: 1}).Error; err != nil {
				t.Fatal(err)
			}
			other := group.Group{Name: "other-group", Strategy: group.StrategyFailover}
			if err := shared.DB.Create(&other).Error; err != nil {
				t.Fatal(err)
			}
			item := group.GroupItem{GroupID: other.ID, ModelID: otherModel.ID, Priority: 0}
			if err := shared.DB.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			if err := shared.DB.Create(&group.GroupItem{GroupID: other.ID, ModelID: fallbackModel.ID, Priority: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if manual {
				var active group.GroupItem
				if err := shared.DB.Where("group_id = ? AND model_id = ?", g.ID, m.ID).First(&active).Error; err != nil {
					t.Fatal(err)
				}
				for _, x := range []struct{ id, active int64 }{{g.ID, active.ID}, {other.ID, item.ID}} {
					if err := shared.DB.Model(&group.Group{}).Where("id = ?", x.id).Updates(map[string]any{"strategy": group.StrategyManual, "active_item_id": x.active}).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- doReq(t, r, "/v1/chat/completions", rateLimitRequest, key) }()
			waitProbeSignal(t, entered)
			if count := channelConcurrencyCount(store, c.ID); count != 1 {
				t.Fatalf("body occupancy=%d", count)
			}
			const n = 6
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
					t.Errorf("busy status=%d want=%d", status, want)
				}
			}
			if primary.Load() != 1 {
				t.Fatalf("full channel sent %d primary requests", primary.Load())
			}
			rows := attempts.entries()
			if manual {
				if fallback.Load() != 0 || len(rows) != 0 {
					t.Fatalf("manual switched/invented attempts fallback=%d rows=%+v", fallback.Load(), rows)
				}
			} else {
				if fallback.Load() != n || len(rows) != n {
					t.Fatalf("fallback=%d attempts=%d", fallback.Load(), len(rows))
				}
				for _, row := range rows {
					if row.Sequence != 1 || row.Reason != "initial" || row.ChannelID == c.ID {
						t.Errorf("skips spent HTTP attempt: %+v", row)
					}
				}
			}
			traceMu.Lock()
			for _, row := range traces {
				var skips int
				for _, e := range row.Events {
					if e.Event == "candidate_skipped" && e.Reason == "channel_capacity" {
						skips++
						if e.CapacityLimit == nil || *e.CapacityLimit != 1 || e.InFlight == nil || *e.InFlight != 1 {
							t.Errorf("capacity evidence=%+v", e)
						}
					}
				}
				if skips < 1 || row.HTTPAttempts != map[bool]int{true: 0, false: 1}[manual] {
					t.Errorf("diagnostic skips=%d row=%+v", skips, row)
				}
			}
			if len(traces) != n {
				t.Errorf("diagnostic rows=%d", len(traces))
			}
			traceMu.Unlock()
			once.Do(func() { close(release) })
			if w := <-done; w.Code != 200 {
				t.Fatalf("first status=%d body=%s", w.Code, w.Body.String())
			}
			if count := channelConcurrencyCount(store, c.ID); count != 0 {
				t.Fatalf("body leaked count=%d", count)
			}
			if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 || primary.Load() != 2 {
				t.Fatalf("fixed primary not resumed status=%d primary=%d", w.Code, primary.Load())
			}
		})
	}
}

// Unlimited requests must also be counted: enabling the limit or rotating a key
// while they are live cannot reset the channel's occupancy.
func TestChannelConcurrencyDefaultUnlimitedAndLiveLimitChange(t *testing.T) {
	store := NewChannelConcurrencyStore()
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var primary, fallback atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { d.Concurrency = store }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rateLimitModelName(t, req) == "fallback-model" {
			fallback.Add(1)
			io.WriteString(w, budgetChatSuccess)
			return
		}
		if primary.Add(1) <= 2 {
			entered <- struct{}{}
			<-release
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c, _, g := rateLimitSeed(t)
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	done := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- doReq(t, r, "/v1/chat/completions", rateLimitRequest, key) }()
	}
	waitProbeSignal(t, entered)
	waitProbeSignal(t, entered)
	if count := channelConcurrencyCount(store, c.ID); count != 2 {
		t.Fatalf("default unlimited occupancy=%d", count)
	}
	if err := shared.DB.Model(&c).Updates(map[string]any{"max_concurrent_requests": 1, "key": "rotated-key"}).Error; err != nil {
		t.Fatal(err)
	}
	if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 || primary.Load() != 2 || fallback.Load() != 1 {
		t.Fatalf("limit/key change reset live bucket status=%d primary=%d backup=%d", w.Code, primary.Load(), fallback.Load())
	}
	once.Do(func() { close(release) })
	for i := 0; i < 2; i++ {
		if w := <-done; w.Code != 200 {
			t.Fatalf("unlimited status=%d", w.Code)
		}
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("unlimited leaked count=%d", count)
	}
	if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 || primary.Load() != 3 {
		t.Fatalf("primary did not resume status=%d primary=%d", w.Code, primary.Load())
	}
}

func TestChannelConcurrencyCompatibilitySharesOneSlot(t *testing.T) {
	store := NewChannelConcurrencyStore()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var primary, fallback atomic.Int32
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { d.Concurrency = store; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rateLimitModelName(t, req) == "fallback-model" {
			fallback.Add(1)
			io.WriteString(w, budgetChatSuccess)
			return
		}
		switch primary.Add(1) {
		case 1:
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		case 2:
			close(entered)
			<-release
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c, _, g := rateLimitSeed(t)
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
	}()
	waitProbeSignal(t, entered)
	if count := channelConcurrencyCount(store, c.ID); count != 1 {
		t.Fatalf("compat occupancy=%d", count)
	}
	if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 || fallback.Load() != 1 || primary.Load() != 2 {
		t.Fatalf("compat released early status=%d primary=%d backup=%d", w.Code, primary.Load(), fallback.Load())
	}
	once.Do(func() { close(release) })
	if w := <-done; w.Code != 200 {
		t.Fatalf("compat status=%d body=%s", w.Code, w.Body.String())
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("compat leaked=%d", count)
	}
	var compat int
	for _, row := range attempts.entries() {
		if row.Reason == "budget_compat" {
			compat++
		}
	}
	if compat != 1 || len(attempts.entries()) != 3 {
		t.Fatalf("compat attempts=%+v", attempts.entries())
	}
}

func TestChannelConcurrencyStreamRetainsSlotUntilTerminal(t *testing.T) {
	store := NewChannelConcurrencyStore()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var primary, fallback atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { d.Concurrency = store }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rateLimitModelName(t, req) == "fallback-model" {
			fallback.Add(1)
			io.WriteString(w, budgetChatSuccess)
			return
		}
		primary.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, rateLimitContent)
		w.(http.Flusher).Flush()
		close(entered)
		<-release
		io.WriteString(w, deliveryFinish+"data: [DONE]\n\n")
	}))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c, _, g := rateLimitSeed(t)
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
	}()
	waitProbeSignal(t, entered)
	if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 || primary.Load() != 1 || fallback.Load() != 1 {
		t.Fatalf("stream released early status=%d primary=%d backup=%d", w.Code, primary.Load(), fallback.Load())
	}
	if count := channelConcurrencyCount(store, c.ID); count != 1 {
		t.Fatalf("stream occupancy=%d", count)
	}
	once.Do(func() { close(release) })
	if w := <-done; w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("stream result=%d %s", w.Code, w.Body.String())
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("stream leaked=%d", count)
	}
}

func TestChannelConcurrencyFailurePathsReleaseSlot(t *testing.T) {
	for _, kind := range []string{"http500", "http429", "resource", "cancel", "compat_budget"} {
		t.Run(kind, func(t *testing.T) {
			store := NewChannelConcurrencyStore()
			pc := shared.DefaultProxyConfig()
			pc.MaxHTTPAttempts = 1
			pc.RetryCount = 0
			pc.ResourceLimits.MaxResponseBytes = 512
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempts := &attemptRecorder{}
			var calls atomic.Int32
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(pc)(d)
				d.Concurrency = store
				d.AttemptWrite = attempts.write
				d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls.Add(1)
					if kind == "cancel" {
						cancel()
						return nil, req.Context().Err()
					}
					status, body := 200, budgetChatSuccess
					switch kind {
					case "http500":
						status, body = 500, `{"error":{"message":"busy"}}`
					case "http429":
						status, body = 429, `{"error":{"message":"busy"}}`
					case "resource":
						body = strings.Repeat("x", 513)
					case "compat_budget":
						status, body = 400, budgetUnsupportedLegacy
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server transport") }))
			c, _, _ := rateLimitSeed(t)
			if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
				t.Fatal(err)
			}
			path, body := "/v1/chat/completions", rateLimitRequest
			if kind == "compat_budget" {
				path, body = "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`
			}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+key)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if calls.Load() != 1 || len(attempts.entries()) != 1 {
				t.Fatalf("failure replayed/invented attempt kind=%s calls=%d rows=%+v", kind, calls.Load(), attempts.entries())
			}
			if count := channelConcurrencyCount(store, c.ID); count != 0 {
				t.Fatalf("%s leaked count=%d", kind, count)
			}
			slot, err := store.Acquire(c.ID, 1)
			if err != nil {
				t.Fatalf("%s could not reacquire: %v", kind, err)
			}
			slot.Release()
		})
	}
}

func TestChannelConcurrencyReleasedBeforeAttemptPersistence(t *testing.T) {
	store := NewChannelConcurrencyStore()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls, writes atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		d.Concurrency = store
		d.AttemptWrite = func(*shared.UpstreamAttempt) error {
			if writes.Add(1) == 1 {
				close(entered)
				<-release
			}
			return nil
		}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { calls.Add(1); io.WriteString(w, budgetChatSuccess) }))
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	c, _, _ := rateLimitSeed(t)
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- doReq(t, r, "/v1/chat/completions", rateLimitRequest, key) }()
	waitProbeSignal(t, entered)
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("persistence retained count=%d", count)
	}
	if w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("blocked persistence retained slot status=%d calls=%d", w.Code, calls.Load())
	}
	once.Do(func() { close(release) })
	if w := <-done; w.Code != 200 {
		t.Fatalf("first status=%d", w.Code)
	}
}

// The second cooldown check occurs inside doRequest, after the advisory candidate
// checks. Filling capacity there deterministically reproduces an admission race.
func TestChannelConcurrencyNativeAdmissionRaceIsNotReplay(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	store := NewChannelConcurrencyStore()
	cooldowns := NewRateLimitStore(pc)
	var checks atomic.Int32
	var channelID int64
	var held *channelCapacityLease
	now := time.Now()
	cooldowns.now = func() time.Time {
		if checks.Add(1) == 2 {
			var err error
			held, err = store.Acquire(channelID, 1)
			if err != nil {
				t.Errorf("race fixture acquire: %v", err)
			}
		}
		return now
	}
	attempts := &attemptRecorder{}
	traces := &routingRecorder{}
	var primary, fallback atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		d.Concurrency = store
		d.Cooldowns = cooldowns
		d.AttemptWrite = attempts.write
		d.RoutingWrite = traces.write
	}, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rateLimitModelName(t, req) == "fallback-model" {
			fallback.Add(1)
			io.WriteString(w, `{"id":"resp","object":"response","status":"completed","output":[]}`)
			return
		}
		primary.Add(1)
		w.WriteHeader(500)
		io.WriteString(w, `{"error":{"message":"failed"}}`)
	}))
	c, _, g := rateLimitSeed(t)
	channelID = c.ID
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	body := `{"model":"my-model","input":"click button","tools":[{"type":"computer_use_preview"}]}`
	w := doReq(t, r, "/v1/responses", body, key)
	if held == nil {
		t.Fatal("admission race barrier not visited")
	}
	defer held.Release()
	if w.Code != 200 || primary.Load() != 0 || fallback.Load() != 1 {
		t.Fatalf("unsent native admission blocked fallback status=%d primary=%d backup=%d body=%s", w.Code, primary.Load(), fallback.Load(), w.Body.String())
	}
	rows := attempts.entries()
	if len(rows) != 1 || rows[0].Sequence != 1 || rows[0].ChannelID == c.ID {
		t.Fatalf("admission fabricated HTTP attempt: %+v", rows)
	}
	row := traces.one(t)
	blocked := routingEvents(row, "admission_blocked")
	if len(blocked) != 1 || blocked[0].Reason != "channel_capacity" || row.HTTPAttempts != 1 {
		t.Fatalf("race diagnostic=%+v", row)
	}
	if count := channelConcurrencyCount(store, c.ID); count != 1 {
		t.Fatalf("rejection released another relay's slot count=%d", count)
	}
	held.Release()
	// Once a real HTTP request was sent, the same non-replayable operation must
	// still stop after its error. Capacity admission cannot relax this safety rule.
	w = doReq(t, r, "/v1/responses", body, key)
	if w.Code != 500 || primary.Load() != 1 || fallback.Load() != 1 {
		t.Fatalf("sent unsafe operation replayed status=%d primary=%d backup=%d", w.Code, primary.Load(), fallback.Load())
	}
	rows = attempts.entries()
	if len(rows) != 2 || rows[1].Sequence != 1 || rows[1].ChannelID != c.ID {
		t.Fatalf("unsafe attempt evidence=%+v", rows)
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("unsafe failure leaked capacity=%d", count)
	}
	if len(traces.rows) != 2 || traces.rows[1].StopReason != "replay_unsafe" {
		t.Fatalf("unsafe stop diagnostic=%+v", traces.rows)
	}
}

func TestChannelConcurrencyBreakerRaceReleasesAcquiredCapacity(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	store := NewChannelConcurrencyStore()
	cooldowns := NewRateLimitStore(pc)
	breakers := NewBreakerStore(pc)
	var checks atomic.Int32
	var modelID int64
	var held *breakerLease
	now := time.Now()
	cooldowns.now = func() time.Time {
		if checks.Add(1) == 2 {
			var err error
			held, err = breakers.Acquire(modelID)
			if err != nil {
				t.Errorf("probe fixture acquire: %v", err)
			}
		}
		return now
	}
	attempts := &attemptRecorder{}
	var primary, fallback atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		d.Concurrency = store
		d.Cooldowns = cooldowns
		d.Breakers = breakers
		d.AttemptWrite = attempts.write
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rateLimitModelName(t, req) == "fallback-model" {
			fallback.Add(1)
		} else {
			primary.Add(1)
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	c, m, g := rateLimitSeed(t)
	modelID = m.ID
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	seedExpiredProbeCircuit(t, breakers, m.ID)
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	if held == nil {
		t.Fatal("probe race barrier not visited")
	}
	defer held.Finish(breakerIgnored)
	if w.Code != 200 || primary.Load() != 0 || fallback.Load() != 1 || len(attempts.entries()) != 1 {
		t.Fatalf("probe contention status=%d primary=%d backup=%d attempts=%+v", w.Code, primary.Load(), fallback.Load(), attempts.entries())
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("breaker acquisition failure leaked capacity=%d", count)
	}
	if err := held.Check(); err != nil {
		t.Fatalf("capacity cleanup invalidated another probe: %v", err)
	}
}

func TestChannelConcurrencyCommittedClientWriteFailureReleasesWithoutReplay(t *testing.T) {
	store := NewChannelConcurrencyStore()
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	var primary, fallback atomic.Int32
	attempts := &attemptRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.Concurrency = store; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rateLimitModelName(t, req) == "fallback-model" {
			fallback.Add(1)
		} else {
			primary.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, rateLimitContent+deliveryFinish+"data: [DONE]\n\n")
	}))
	c, _, g := rateLimitSeed(t)
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	body := `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	w := &rateLimitClientFailureWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: true}
	r.ServeHTTP(w, req)
	rows := attempts.entries()
	if w.writes != 2 || primary.Load() != 1 || fallback.Load() != 0 || len(rows) != 1 || rows[0].Success || rows[0].ErrKind != shared.ErrKindClientWrite {
		t.Fatalf("client failure replayed or lost attribution writes=%d primary=%d backup=%d attempts=%+v", w.writes, primary.Load(), fallback.Load(), rows)
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("client delivery failure retained capacity=%d", count)
	}
	result := doReq(t, r, "/v1/chat/completions", body, key)
	if result.Code != 200 || primary.Load() != 2 || fallback.Load() != 0 || !strings.Contains(result.Body.String(), "[DONE]") {
		t.Fatalf("client failure leaked slot status=%d primary=%d backup=%d body=%s", result.Code, primary.Load(), fallback.Load(), result.Body.String())
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("recovery request retained capacity=%d", count)
	}
}

type channelConcurrencyDeadlineBody struct {
	read  func([]byte) (int, error)
	close func() error
}

func (b *channelConcurrencyDeadlineBody) Read(p []byte) (int, error) { return b.read(p) }
func (b *channelConcurrencyDeadlineBody) Close() error               { return b.close() }

func TestChannelConcurrencyFirstContentDeadlineReleasesWithoutFallback(t *testing.T) {
	store := NewChannelConcurrencyStore()
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	var channelID int64
	var calls, closed atomic.Int32
	attempts := &attemptRecorder{}
	traces := &routingRecorder{}
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		d.Concurrency = store
		d.AttemptWrite = attempts.write
		d.RoutingWrite = traces.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			n := calls.Add(1)
			var responseBody io.ReadCloser = io.NopCloser(strings.NewReader(rateLimitContent + deliveryFinish + "data: [DONE]\n\n"))
			if n == 1 {
				control := controlFrom(req.Context())
				if control == nil {
					t.Fatal("missing request control")
				}
				responseBody = &channelConcurrencyDeadlineBody{
					read: func([]byte) (int, error) {
						if count := channelConcurrencyCount(store, channelID); count != 1 {
							t.Errorf("body read has no capacity: %d", count)
						}
						// Expire the actual request-level first-content deadline only once body
						// processing begins; no timer sleep or artificial upstream timeout is used.
						control.mu.Lock()
						control.deadline = time.Now().Add(-time.Second)
						control.mu.Unlock()
						if err := control.check(); err == nil {
							t.Error("first-content deadline did not expire")
						}
						return 0, req.Context().Err()
					},
					close: func() error {
						closed.Add(1)
						if count := channelConcurrencyCount(store, channelID); count != 1 {
							t.Errorf("capacity released before body close: %d", count)
						}
						return nil
					},
				}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: responseBody}, nil
		})}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected actual server transport") }))
	c, _, g := rateLimitSeed(t)
	channelID = c.ID
	if err := shared.DB.Model(&c).Update("max_concurrent_requests", 1).Error; err != nil {
		t.Fatal(err)
	}
	backup := rateLimitAdd(t, rateLimitOtherChannel(t, c), g, "fallback-model", 1)
	body := `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`
	w := doReq(t, r, "/v1/chat/completions", body, key)
	rows := attempts.entries()
	if calls.Load() != 1 || closed.Load() < 1 || len(rows) != 1 || rows[0].ModelID == backup.ID || rows[0].ErrKind != shared.ErrKindFirstContentDeadline || rows[0].Success {
		t.Fatalf("deadline replayed or lost attribution status=%d calls=%d closes=%d attempts=%+v", w.Code, calls.Load(), closed.Load(), rows)
	}
	if count := channelConcurrencyCount(store, c.ID); count != 0 {
		t.Fatalf("deadline retained capacity=%d", count)
	}
	row := traces.one(t)
	if row.StopReason != shared.ErrKindFirstContentDeadline || row.HTTPAttempts != 1 || row.DeliveryStarted {
		t.Fatalf("deadline diagnosis=%+v", row)
	}
	for _, event := range routingEvents(row, "candidate_selected") {
		if event.Candidate != 1 {
			t.Fatalf("deadline selected backup: %+v", event)
		}
	}
	w = doReq(t, r, "/v1/chat/completions", body, key)
	if w.Code != 200 || calls.Load() != 2 || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("deadline leaked slot status=%d calls=%d body=%s", w.Code, calls.Load(), w.Body.String())
	}
	rows = attempts.entries()
	if len(rows) != 2 || rows[1].ModelID == backup.ID {
		t.Fatalf("deadline poisoned primary: %+v", rows)
	}
}
