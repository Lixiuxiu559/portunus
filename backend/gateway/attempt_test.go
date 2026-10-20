package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

type attemptRecorder struct {
	mu       sync.Mutex
	rows     []shared.UpstreamAttempt
	writeErr error
}

func (a *attemptRecorder) write(row *shared.UpstreamAttempt) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, *row)
	return a.writeErr
}
func (a *attemptRecorder) entries() []shared.UpstreamAttempt {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]shared.UpstreamAttempt(nil), a.rows...)
}

func TestAttemptMixedBudgetRetryAndFailover(t *testing.T) {
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1
	recorder := &attemptRecorder{}
	var calls []budgetAttempt
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, captureBudgetAttempt(t, r))
		switch len(calls) {
		case 1:
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
		case 2, 3:
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"busy"}}`)
		default:
			io.WriteString(w, budgetChatSuccess)
		}
	})
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, upstream)
	var first channel.Channel
	if err := shared.DB.First(&first).Error; err != nil {
		t.Fatal(err)
	}
	second := channel.Channel{Name: "fallback", Type: first.Type, BaseURL: first.BaseURL, Key: "secret"}
	if err := shared.DB.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: second.ID, Name: "fallback-model"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	var g group.Group
	if err := shared.DB.First(&g).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: 1}).Error; err != nil {
		t.Fatal(err)
	}
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":256}`, key)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"ok"`) {
		t.Fatalf("client response=%d %s", w.Code, w.Body.String())
	}
	rows := recorder.entries()
	if len(rows) != 4 || len(calls) != 4 {
		t.Fatalf("records=%d HTTP calls=%d, want 4", len(rows), len(calls))
	}
	reasons := []string{"initial", "budget_compat", "retry", "failover"}
	relays := []int{1, 1, 2, 3}
	fields := []string{"max_tokens", "max_completion_tokens", "max_completion_tokens", "max_tokens"}
	statuses := []int{400, 503, 503, 200}
	for i, row := range rows {
		wantChannel := first.ID
		if i == 3 {
			wantChannel = second.ID
		}
		if row.Sequence != i+1 || row.RelayAttempt != relays[i] || row.Reason != reasons[i] || row.BudgetField != fields[i] || row.Status != statuses[i] || row.ChannelID != wantChannel || row.Success != (i == 3) {
			t.Errorf("attempt %d: %+v", i+1, row)
		}
		if row.RequestID == "" || row.RequestID != w.Header().Get("X-Request-Id") || row.RequestID != calls[i].requestID {
			t.Errorf("request correlation lost: %+v", row)
		}
		if calls[i].body[fields[i]] != float64(256) {
			t.Errorf("actual HTTP budget differs from record: %+v", calls[i])
		}
		if row.ClientProtocol != string(protocol.ProviderOpenAIResponses) || row.UpstreamProtocol != string(protocol.ProviderOpenAI) || row.Stream || row.StartedAt.IsZero() || row.CreatedAt.Before(row.StartedAt) || row.DurationMs < 0 {
			t.Errorf("metadata: %+v", row)
		}
		if i < 3 && row.ErrKind != shared.ErrKindUpstream {
			t.Errorf("failure attribution=%+v", row)
		}
	}
	// 兼容重试独立记录，但不增加旧日志行或重复记用量。
	logs.mu.Lock()
	defer logs.mu.Unlock()
	if len(logs.logs) != 3 {
		t.Fatalf("legacy log rows=%d, want 3 relay calls", len(logs.logs))
	}
	var input, output int64
	for _, row := range logs.logs {
		input += row.InputToken
		output += row.OutputToken
	}
	if input != 1 || output != 1 {
		t.Errorf("legacy usage duplicated: input=%d output=%d", input, output)
	}
}

type attemptRoundTripper func(*http.Request) (*http.Response, error)

func (f attemptRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAttemptTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		kind  string
		count int
	}{{"network", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, shared.ErrKindNetwork, 2}, {"cancel", context.Canceled, shared.ErrKindClientCancel, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &attemptRecorder{}
			calls := 0
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 1
			r, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(pc)(d)
				d.AttemptWrite = recorder.write
				d.Client = &http.Client{Transport: attemptRoundTripper(func(r *http.Request) (*http.Response, error) { calls++; return nil, tc.err })}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP server call") }))
			w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[]}`, key)
			rows := recorder.entries()
			if calls != tc.count || len(rows) != tc.count || w.Code == 200 {
				t.Fatalf("calls=%d records=%d client=%d", calls, len(rows), w.Code)
			}
			for i, row := range rows {
				if row.Sequence != i+1 || row.Status != 0 || row.Success || row.ErrKind != tc.kind || strings.Contains(row.ErrMsg, "http://") {
					t.Errorf("bad transport record: %+v", row)
				}
			}
		})
	}
}

func TestAttemptCommittedStreamDoesNotRetry(t *testing.T) {
	recorder := &attemptRecorder{}
	calls := 0
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 2
	pc.StreamIdleTimeoutSeconds = 1
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeStalledSSE(t, w, r, true)
	}))
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[],"stream":true}`, key)
	rows := recorder.entries()
	if calls != 1 || len(rows) != 1 {
		t.Fatalf("committed stream retried: calls=%d records=%d", calls, len(rows))
	}
	if !strings.Contains(w.Body.String(), `"content":"hi"`) || !rows[0].Stream || rows[0].Success || rows[0].ErrKind != shared.ErrKindStreamInterrupt || logs.last().ErrKind != rows[0].ErrKind {
		t.Fatalf("committed outcome lost: body=%s row=%+v", w.Body.String(), rows[0])
	}
}

func TestAttemptNotCreatedBeforeHTTP(t *testing.T) {
	for _, precompose := range []bool{true, false} {
		name := "circuit_open"
		if precompose {
			name = "compose_error"
		}
		t.Run(name, func(t *testing.T) {
			recorder := &attemptRecorder{}
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 0
			pc.CircuitFailureThreshold = 1
			var breakers *BreakerStore
			calls := 0
			r, key, logs := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write; breakers = d.Breakers }, protocol.ProviderAnthropic, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			body := `{"model":"my-model","messages":[]}`
			if precompose {
				body = `{"model":"my-model","messages":"invalid"}`
			} else {
				var m model.Model
				if err := shared.DB.First(&m).Error; err != nil {
					t.Fatal(err)
				}
				breakers.Record(m.ID, true)
			}
			w := doReq(t, r, "/v1/chat/completions", body, key)
			if calls != 0 || len(recorder.entries()) != 0 || w.Code == 200 {
				t.Fatalf("no HTTP must mean no attempt: calls=%d rows=%v status=%d", calls, recorder.entries(), w.Code)
			}
			want := shared.ErrKindCircuitOpen
			if precompose {
				want = shared.ErrKindConvert
			}
			if logs.last() == nil || logs.last().ErrKind != want {
				t.Fatalf("legacy failure log lost: %+v", logs.last())
			}
		})
	}
}

func TestAttemptWriteFailureDoesNotRetryUpstream(t *testing.T) {
	recorder := &attemptRecorder{writeErr: errors.New("log store unavailable")}
	calls := 0
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, budgetChatSuccess) }))
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[]}`, key)
	if w.Code != 200 || calls != 1 || len(recorder.entries()) != 1 || logs.last() == nil || !logs.last().Success {
		t.Fatalf("logging changed relay: code=%d calls=%d records=%v", w.Code, calls, recorder.entries())
	}
}

func TestAttemptConcurrentRequestsKeepIndependentSequences(t *testing.T) {
	recorder := &attemptRecorder{}
	var mu sync.Mutex
	calls := make(map[string]int)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		id := r.Header.Get("X-Request-Id")
		calls[id]++
		n := calls[id]
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		}
		io.WriteString(w, budgetChatSuccess)
	})
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	r, key, _ := setupGatewayDeps(t, func(d *Deps) { withCfg(pc)(d); d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, upstream)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":16}`, key)
			if w.Code != 200 {
				t.Errorf("concurrent response: %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	byRequest := make(map[string][]shared.UpstreamAttempt)
	for _, row := range recorder.entries() {
		byRequest[row.RequestID] = append(byRequest[row.RequestID], row)
	}
	if len(byRequest) != 8 {
		t.Fatalf("request count=%d, want 8", len(byRequest))
	}
	for id, rows := range byRequest {
		if id == "" || calls[id] != 2 || len(rows) != 2 || rows[0].Sequence != 1 || rows[1].Sequence != 2 || rows[0].Reason != "initial" || rows[1].Reason != "budget_compat" || rows[0].RelayAttempt != 1 || rows[1].RelayAttempt != 1 {
			t.Errorf("request %s: calls=%d rows=%+v", id, calls[id], rows)
		}
	}
}
