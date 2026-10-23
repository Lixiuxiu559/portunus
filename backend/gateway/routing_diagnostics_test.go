package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/resource"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
)

type routingRecorder struct{ rows []shared.RoutingTrace }

func (r *routingRecorder) write(row *shared.RoutingTrace) error {
	cp := *row
	cp.Events = append([]shared.RoutingEvent(nil), row.Events...)
	r.rows = append(r.rows, cp)
	return nil
}
func (r *routingRecorder) one(t *testing.T) shared.RoutingTrace {
	t.Helper()
	if len(r.rows) != 1 {
		t.Fatalf("routing rows=%d", len(r.rows))
	}
	return r.rows[0]
}
func routingEvents(row shared.RoutingTrace, kind string) []shared.RoutingEvent {
	var out []shared.RoutingEvent
	for _, e := range row.Events {
		if e.Event == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestRoutingSkippedCandidatesKeepActualAttemptSequence(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.CircuitFailureThreshold = 1
	traces, attempts := &routingRecorder{}, &attemptRecorder{}
	var bs *BreakerStore
	var cooldowns *RateLimitStore
	var calls atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		bs = d.Breakers
		cooldowns = NewRateLimitStore(pc)
		d.Cooldowns = cooldowns
		d.RoutingWrite = traces.write
		d.AttemptWrite = attempts.write
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := rateLimitModelName(t, r); got != "third-model" {
			t.Errorf("called skipped model %q", got)
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	c, m, g := rateLimitSeed(t)
	second := rateLimitAdd(t, c, g, "second-model", 1)
	third := rateLimitAdd(t, c, g, "third-model", 2)
	cooldowns.Record(router.Target{Channel: c, Model: m}, "30")
	bs.Record(second.ID, true)
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	row := traces.one(t)
	skips := routingEvents(row, "candidate_skipped")
	selected := routingEvents(row, "candidate_selected")
	if w.Code != 200 || calls.Load() != 1 || row.HTTPAttempts != 1 || row.StopReason != "relay_succeeded" || row.CandidateCount != 3 || row.FinalCandidate != 3 || row.FinalModelID != third.ID || row.RequestID != w.Header().Get("X-Request-Id") {
		t.Fatalf("response=%d trace=%+v", w.Code, row)
	}
	if len(skips) != 2 || skips[0].Candidate != 1 || skips[0].Reason != "rate_limit_cooldown" || skips[0].Scope != "model" || skips[0].RetryAfter < 1 || skips[1].Candidate != 2 || skips[1].Reason != "circuit_open" {
		t.Fatalf("skips=%+v", skips)
	}
	if len(selected) != 1 || selected[0].Reason != "backup" || selected[0].Candidate != 3 || selected[0].RelayAttempt != 1 {
		t.Fatalf("selected=%+v", selected)
	}
	rows := attempts.entries()
	if len(rows) != 1 || rows[0].Sequence != 1 || rows[0].Reason != "initial" || rows[0].ModelID != third.ID {
		t.Fatalf("HTTP attempts=%+v", rows)
	}
}

func TestRoutingManualProbeBusyNeverSelectsBackup(t *testing.T) {
	traces, attempts := &routingRecorder{}, &attemptRecorder{}
	var bs *BreakerStore
	var calls atomic.Int32
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { bs = d.Breakers; d.RoutingWrite = traces.write; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	c, m, g := rateLimitSeed(t)
	rateLimitAdd(t, c, g, "backup-model", 1)
	var item group.GroupItem
	if err := shared.DB.Where("group_id = ? AND model_id = ?", g.ID, m.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Model(&g).Updates(map[string]any{"strategy": group.StrategyManual, "active_item_id": item.ID}).Error; err != nil {
		t.Fatal(err)
	}
	seedExpiredProbeCircuit(t, bs, m.ID)
	lease, err := bs.Acquire(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(breakerIgnored)
	w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
	row := traces.one(t)
	skips := routingEvents(row, "candidate_skipped")
	if w.Code != 503 || calls.Load() != 0 || len(attempts.entries()) != 0 || row.HTTPAttempts != 0 || row.CandidateCount != 1 || row.FinalCandidate != 0 || row.StopReason != "no_available_target" || len(skips) != 1 || skips[0].Reason != "probe_busy" || len(routingEvents(row, "candidate_selected")) != 0 {
		t.Fatalf("status=%d trace=%+v", w.Code, row)
	}
}

func TestRoutingStreamFinalOutcomeDoesNotUseNilReturnAsSuccess(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncated", true: "terminal"}[complete], func(t *testing.T) {
			traces, attempts := &routingRecorder{}, &attemptRecorder{}
			var calls atomic.Int32
			r, key, _ := setupGatewayDeps(t, func(d *Deps) { d.RoutingWrite = traces.write; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, testChunkLine)
				if complete {
					io.WriteString(w, deliveryFinish+"data: [DONE]\n\n")
				}
			}))
			w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hi"}],"stream":true}`, key)
			row := traces.one(t)
			reason := "committed_failure"
			if complete {
				reason = "relay_succeeded"
			}
			if w.Code != 200 || calls.Load() != 1 || !row.DeliveryStarted || row.StopReason != reason || row.HTTPAttempts != 1 || len(attempts.entries()) != 1 {
				t.Fatalf("trace=%+v body=%s", row, w.Body.String())
			}
			finishes := routingEvents(row, "relay_finished")
			if len(finishes) != 1 || (finishes[0].Reason == "succeeded") != complete {
				t.Fatalf("finishes=%+v", finishes)
			}
		})
	}
}

func TestRoutingRejectedRequestsHaveNoHTTPAttempt(t *testing.T) {
	for _, tc := range []struct {
		name, body, stop string
		limit            int64
		status           int
	}{
		{"invalid", "{", "request_invalid", 0, 400}, {"resource", rateLimitRequest, "resource_limit", 4, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			traces, attempts := &routingRecorder{}, &attemptRecorder{}
			var calls atomic.Int32
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				if tc.limit > 0 {
					d.Cfg.ResourceLimits.MaxRequestBytes = tc.limit
				}
				d.RoutingWrite = traces.write
				d.AttemptWrite = attempts.write
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			w := doReq(t, r, "/v1/chat/completions", tc.body, key)
			row := traces.one(t)
			if w.Code != tc.status || row.StopReason != tc.stop || row.HTTPAttempts != 0 || row.CandidateCount != 0 || calls.Load() != 0 || len(attempts.entries()) != 0 {
				t.Fatalf("status=%d trace=%+v", w.Code, row)
			}
		})
	}
}

func TestRoutingCompatibilityHTTPAttemptsShareProbe(t *testing.T) {
	traces, attempts := &routingRecorder{}, &attemptRecorder{}
	var bs *BreakerStore
	var calls atomic.Int32
	pc := shared.DefaultProxyConfig()
	pc.RetryBackoffBaseMs = 1
	pc.RetryBackoffMaxMs = 1
	r, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(pc)(d)
		bs = d.Breakers
		d.RoutingWrite = traces.write
		d.AttemptWrite = attempts.write
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		}
		io.WriteString(w, budgetChatSuccess)
	}))
	_, m, _ := rateLimitSeed(t)
	seedExpiredProbeCircuit(t, bs, m.ID)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":64}`, key)
	row := traces.one(t)
	acquired, finished := routingEvents(row, "probe_acquired"), routingEvents(row, "probe_finished")
	if w.Code != 200 || calls.Load() != 2 || row.HTTPAttempts != 2 || len(acquired) != 1 || len(finished) != 1 || !acquired[0].Probe || !finished[0].Probe || finished[0].Reason != "succeeded" || row.StopReason != "relay_succeeded" {
		t.Fatalf("status=%d trace=%+v", w.Code, row)
	}
	rows := attempts.entries()
	if len(rows) != 2 || rows[0].Reason != "initial" || rows[1].Reason != "budget_compat" || rows[1].RelayAttempt != rows[0].RelayAttempt {
		t.Fatalf("attempts=%+v", rows)
	}
}

func TestRoutingWriterFailureDoesNotAffectRelay(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil", true: "failure"}[enabled], func(t *testing.T) {
			var calls, writes atomic.Int32
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				if enabled {
					d.RoutingWrite = func(*shared.RoutingTrace) error { writes.Add(1); return errors.New("secret-driver-value") }
				}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, budgetChatSuccess) }))
			w := doReq(t, r, "/v1/chat/completions", rateLimitRequest, key)
			if w.Code != 200 || calls.Load() != 1 || strings.Contains(w.Body.String(), "secret-driver-value") || (writes.Load() == 1) != enabled {
				t.Fatalf("status=%d calls=%d writes=%d body=%s", w.Code, calls.Load(), writes.Load(), w.Body.String())
			}
		})
	}
}

func TestRoutingCollectorBoundedAndSecretFree(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	d := newRoutingDiagnostics(c, "safe-id", protocol.ProviderOpenAI)
	target := d.target(router.Target{}, 1, 1)
	for n := 0; n < shared.MaxRoutingEvents+7; n++ {
		target.event("relay_finished", "failed", errors.New("secret-upstream-payload"))
	}
	d.row.FinalCandidate = 42
	d.row.FinalChannelID = 23
	d.row.FinalModelID = 24
	d.row.HTTPAttempts = 5
	d.stop("candidates_exhausted", errors.New("secret-url-credential"), "relay_failed")
	if len(d.row.Events) != shared.MaxRoutingEvents || d.row.DroppedEvents != 7 || d.row.Events[255].Sequence != 256 || d.row.FinalCandidate != 42 || d.row.HTTPAttempts != 5 || d.row.StopReason != "candidates_exhausted" {
		t.Fatalf("bounded row=%+v", d.row)
	}
	encoded, err := json.Marshal(d.row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-") {
		t.Fatalf("secret persisted: %s", encoded)
	}
}

func TestRoutingStopReasonSafetyPriority(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&attemptBudgetError{}, shared.ErrKindAttemptBudget},
		{context.Canceled, shared.ErrKindClientCancel},
		{protocol.ErrStreamInvalid, "conversion_safety_stop"},
		{protocol.ErrSSEEventTooLarge, "conversion_safety_stop"},
		{&precommitBufferError{}, "conversion_safety_stop"},
		{&streamReplayUnsafeError{err: errors.New("upstream")}, "replay_unsafe"},
		{&streamReplayUnsafeError{err: &resource.LimitError{Scope: resource.Request}}, shared.ErrKindResourceLimit},
		{&streamCommittedError{err: context.Canceled}, "committed_failure"},
	}
	for _, tc := range cases {
		if got := routingStopReason(tc.err); got != tc.want {
			t.Errorf("%T stop=%q want %q", tc.err, got, tc.want)
		}
	}
}

func TestRoutingHardStopNeverVisitsBackup(t *testing.T) {
	for _, mode := range []string{"budget", "cancel", "unsafe"} {
		t.Run(mode, func(t *testing.T) {
			traces, attempts := &routingRecorder{}, &attemptRecorder{}
			var calls atomic.Int32
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 3
			pc.MaxHTTPAttempts = 1
			pc.RetryBackoffBaseMs = 1
			pc.RetryBackoffMaxMs = 1
			provider := protocol.ProviderOpenAI
			path, body := "/v1/chat/completions", rateLimitRequest
			if mode == "unsafe" {
				provider = protocol.ProviderOpenAIResponses
				path = "/v1/responses"
				body = `{"model":"my-model","input":"click","stream":true,"tools":[{"type":"computer_use_preview","display_width":1024,"display_height":768,"environment":"browser"}]}`
			}
			r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.RoutingWrite = traces.write; d.AttemptWrite = attempts.write }, provider, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(503)
				io.WriteString(w, `{"error":{"message":"secret-upstream-error"}}`)
			}))
			c, _, g := rateLimitSeed(t)
			rateLimitAdd(t, c, g, "backup-model", 1)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			if mode == "cancel" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			row := traces.one(t)
			want, cnt := shared.ErrKindAttemptBudget, 1
			if mode == "cancel" {
				want, cnt = shared.ErrKindClientCancel, 0
			}
			if mode == "unsafe" {
				want = "replay_unsafe"
			}
			if row.StopReason != want || row.HTTPAttempts != cnt || int(calls.Load()) != cnt || len(attempts.entries()) != cnt {
				t.Fatalf("stop=%s calls=%d trace=%+v", want, calls.Load(), row)
			}
			for _, e := range routingEvents(row, "candidate_selected") {
				if e.Candidate != 1 {
					t.Fatalf("hard stop selected backup: %+v", e)
				}
			}
			encoded, _ := json.Marshal(row)
			if strings.Contains(string(encoded), "secret-upstream-error") {
				t.Fatal("upstream body leaked into diagnostic")
			}
		})
	}
}

func TestRoutingProbeReportsStaleFinishWithoutHealing(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	d := newRoutingDiagnostics(c, "stale-probe", protocol.ProviderOpenAI)
	bs := NewBreakerStore(shared.DefaultProxyConfig())
	seedExpiredProbeCircuit(t, bs, 77)
	permit := &breakerAttempt{store: bs, modelID: 77, routing: d.target(router.Target{}, 1, 1)}
	if err := permit.acquire(); err != nil {
		t.Fatal(err)
	}
	bs.Record(77, true) // A newer health generation invalidates this in-flight probe.
	permit.finish(true, failDecision{})
	finished := routingEvents(d.row, "probe_finished")
	if len(finished) != 1 || finished[0].Reason != "stale_lease" || !finished[0].Probe {
		t.Fatalf("stale result=%+v", d.row)
	}
	if allowed, _ := bs.Allow(77); allowed {
		t.Fatal("stale success healed a newer open circuit")
	}
	if d.row.HTTPAttempts != 0 {
		t.Fatal("diagnostic probe events manufactured HTTP attempts")
	}
}

func TestRoutingStreamValidationStopsWithoutExhaustingCandidates(t *testing.T) {
	for _, payload := range []string{`data: {"choices":[{"index":0,"delta":{},"finish_reason":"invalid_reason"}]}` + "\n\n", "data: " + strings.Repeat("x", (1<<20)+1) + "\n\n"} {
		traces, attempts := &routingRecorder{}, &attemptRecorder{}
		var calls atomic.Int32
		r, key, _ := setupGatewayDeps(t, func(d *Deps) { d.RoutingWrite = traces.write; d.AttemptWrite = attempts.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, payload)
		}))
		c, _, g := rateLimitSeed(t)
		rateLimitAdd(t, c, g, "backup", 1)
		w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hi"}],"stream":true}`, key)
		row := traces.one(t)
		if w.Code != 502 || row.StopReason != "conversion_safety_stop" || row.StopErrKind != shared.ErrKindConvert || row.HTTPAttempts != 1 || calls.Load() != 1 || len(attempts.entries()) != 1 || len(routingEvents(row, "candidate_selected")) != 1 {
			t.Fatalf("status=%d trace=%+v", w.Code, row)
		}
	}
}
