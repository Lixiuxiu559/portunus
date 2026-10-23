package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestRequestControlBudgetLifecycle(t *testing.T) {
	for _, max := range []int{0, 2} {
		c := newRequestControl(context.Background(), max, 0)
		defer c.close()
		want := max
		if want == 0 {
			want = 4
		}
		for i := 0; i < want; i++ {
			if err := c.available(); err != nil {
				t.Fatal(err)
			}
			if err := c.reserve(); err != nil {
				t.Fatal(err)
			}
		}
		for _, err := range []error{c.available(), c.reserve()} {
			var exhausted *attemptBudgetError
			if !errors.As(err, &exhausted) {
				t.Fatalf("budget max=%d: %v", max, err)
			}
			d := failSpec(err)
			if !d.StopRequest || d.Retryable || d.BreakerFail {
				t.Fatalf("local budget disposition=%+v", d)
			}
		}
	}
}

func TestRequestControlExpiryAndCommit(t *testing.T) {
	t.Run("expiry forbids reservation and commitment", func(t *testing.T) {
		c := newRequestControl(context.Background(), 3, 20*time.Millisecond)
		defer c.close()
		select {
		case <-c.ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("timer did not cancel")
		}
		for _, err := range []error{c.check(), c.available(), c.reserve(), c.commit()} {
			var expired *firstContentDeadlineError
			if !errors.As(err, &expired) {
				t.Fatalf("deadline lost: %v", err)
			}
			d := failSpec(err)
			if !d.StopRequest || d.Retryable || d.BreakerFail {
				t.Fatalf("deadline disposition=%+v", d)
			}
		}
	})
	t.Run("commit removes timer but preserves parent cancellation", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		c := newRequestControl(parent, 3, 40*time.Millisecond)
		defer c.close()
		if err := c.reserve(); err != nil {
			t.Fatal(err)
		}
		if err := c.commit(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(70 * time.Millisecond)
		if err := c.check(); err != nil {
			t.Fatalf("committed stream expired: %v", err)
		}
		if err := c.reserve(); err == nil {
			t.Fatal("delivery allowed replay")
		}
		cancel()
		if !errors.Is(c.check(), context.Canceled) {
			t.Fatal("parent cancellation ignored")
		}
	})
	t.Run("concurrent reservation stays bounded", func(t *testing.T) {
		c := newRequestControl(context.Background(), 4, 0)
		defer c.close()
		var wg sync.WaitGroup
		var mu sync.Mutex
		accepted := 0
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if c.reserve() == nil {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if accepted != 4 {
			t.Fatalf("accepted=%d", accepted)
		}
	})
}

func requestControlFallback(t *testing.T) {
	t.Helper()
	var ch channel.Channel
	var g group.Group
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.First(&g).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "fallback-model"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: 1}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestRequestControlRelaySharedHTTPBudget(t *testing.T) {
	for _, max := range []int{1, 3, 4} {
		t.Run(string(rune('0'+max)), func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.MaxHTTPAttempts = max
			pc.RetryCount = 1
			recorder := &attemptRecorder{}
			calls := 0
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch calls {
				case 1:
					w.WriteHeader(400)
					io.WriteString(w, budgetUnsupportedLegacy)
				case 2, 3:
					w.WriteHeader(503)
				default:
					io.WriteString(w, budgetChatSuccess)
				}
			}))
			requestControlFallback(t)
			w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
			rows := recorder.entries()
			if calls != max || len(rows) != max {
				t.Fatalf("max=%d HTTP=%d records=%d", max, calls, len(rows))
			}
			if (w.Code == 200) != (max == 4) {
				t.Fatalf("max=%d response=%d %s", max, w.Code, w.Body.String())
			}
			for i, row := range rows {
				if row.Sequence != i+1 {
					t.Fatalf("sequence gap: %+v", rows)
				}
			}
			if max >= 3 && (rows[1].Reason != "budget_compat" || rows[2].Reason != "retry") {
				t.Fatalf("compatibility and normal retry must share budget: %+v", rows)
			}
			if max == 4 && rows[3].Reason != "failover" {
				t.Fatalf("last attempt was not fallback: %+v", rows)
			}
			if entry := logs.last(); entry == nil || entry.ConversionPolicy != "" {
				t.Fatalf("retired conversion policy was written: %+v", entry)
			}
		})
	}
}

// The body blocks at a semantic stage while respecting the actual request context.
// This models a stalled HTTP body without a server goroutine that outlives the test.
type requestControlBody struct {
	ctx    context.Context
	prefix *strings.Reader
}

func (b *requestControlBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *requestControlBody) Close() error { return nil }

func TestRequestControlCumulativeDeadlineAcrossTargets(t *testing.T) {
	for _, phase := range []string{"headers", "metadata", "compatibility_body"} {
		t.Run(phase, func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 0
			pc.FirstContentTimeoutSeconds = 1
			pc.FirstByteTimeoutSeconds = 5
			recorder := &attemptRecorder{}
			calls := 0
			var breakers *BreakerStore
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(pc)(d)
				breakers = d.Breakers
				d.AttemptWrite = recorder.write
				d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						select {
						case <-time.After(550 * time.Millisecond):
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
						return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
					}
					if phase == "headers" {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					status := 200
					prefix := ": heartbeat\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
					if phase == "compatibility_body" {
						status = 400
						prefix = budgetUnsupportedLegacy
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &requestControlBody{ctx: req.Context(), prefix: strings.NewReader(prefix)}}, nil
				})}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") }))
			requestControlFallback(t)
			start := time.Now()
			w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","stream":true,"max_output_tokens":32}`, key)
			elapsed := time.Since(start)
			if w.Code != 504 || calls != 2 || len(recorder.entries()) != 2 {
				t.Fatalf("phase=%s status=%d calls=%d rows=%+v body=%s", phase, w.Code, calls, recorder.entries(), w.Body.String())
			}
			if elapsed < 900*time.Millisecond || elapsed > 1400*time.Millisecond {
				t.Fatalf("deadline reset across targets: %s", elapsed)
			}
			rows := recorder.entries()
			if rows[1].ErrKind != shared.ErrKindFirstContentDeadline {
				t.Fatalf("deadline attribution=%+v", rows[1])
			}
			if v, ok := breakers.m.Load(rows[1].ModelID); ok {
				b := v.(*circuitBreaker)
				b.mu.Lock()
				failures := b.consecutiveFailure
				b.mu.Unlock()
				if failures != 0 {
					t.Fatalf("local expiry fed breaker: %d", failures)
				}
			}
		})
	}
}

func TestRequestControlDeliveredStreamOutlivesDeadline(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.FirstContentTimeoutSeconds = 1
	pc.StreamIdleTimeoutSeconds = 5
	recorder := &attemptRecorder{}
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			t.Error("committed stream canceled by first-content timer")
			return
		case <-time.After(1150 * time.Millisecond):
		}
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[],"stream":true}`, key)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "[DONE]") || len(recorder.entries()) != 1 || !logs.last().Success {
		t.Fatalf("long stream failed: %d %s row=%+v", w.Code, w.Body.String(), logs.last())
	}
}

func TestRequestControlSkippedTargetsDoNotSpendBudget(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.MaxHTTPAttempts = 1
	pc.CircuitFailureThreshold = 1
	recorder := &attemptRecorder{}
	var breakers *BreakerStore
	calls := 0
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); breakers = d.Breakers; d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, budgetChatSuccess) }))
	var first model.Model
	if err := shared.DB.First(&first).Error; err != nil {
		t.Fatal(err)
	}
	breakers.Record(first.ID, true)
	requestControlFallback(t)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[]}`, key)
	rows := recorder.entries()
	if w.Code != 200 || calls != 1 || len(rows) != 1 || rows[0].Sequence != 1 || rows[0].ModelID == first.ID {
		t.Fatalf("skipped target spent HTTP budget: code=%d calls=%d rows=%+v body=%s", w.Code, calls, rows, w.Body.String())
	}
}

func TestRequestControlClientCancellationNeverStartsNewAttempt(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before_HTTP", false: "during_HTTP"}[before], func(t *testing.T) {
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 3
			recorder := &attemptRecorder{}
			calls := 0
			var breakers *BreakerStore
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(pc)(d)
				breakers = d.Breakers
				d.AttemptWrite = recorder.write
				d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					cancel()
					<-req.Context().Done()
					return nil, req.Context().Err()
				})}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") }))
			requestControlFallback(t)
			if before {
				cancel()
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"my-model","messages":[],"stream":true}`)).WithContext(parent)
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := 1
			if before {
				want = 0
			}
			rows := recorder.entries()
			if calls != want || len(rows) != want {
				t.Fatalf("cancellation replayed HTTP: calls=%d records=%+v", calls, rows)
			}
			for _, row := range rows {
				if row.ErrKind != shared.ErrKindClientCancel {
					t.Fatalf("cancellation attribution=%+v", row)
				}
				if v, ok := breakers.m.Load(row.ModelID); ok {
					b := v.(*circuitBreaker)
					b.mu.Lock()
					failures := b.consecutiveFailure
					b.mu.Unlock()
					if failures != 0 {
						t.Fatalf("client cancellation fed breaker: %d", failures)
					}
				}
			}
		})
	}
}
