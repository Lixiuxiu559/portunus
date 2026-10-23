package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// 每个阶段都使用真实请求 context 阻塞，避免把每次 Do 的独立 timeout
// 误当成整个客户端请求的累计 deadline。
func TestNonStreamCumulativeDeadlineAcrossTargets(t *testing.T) {
	for _, phase := range []string{"headers", "success_body", "error_body", "compatibility_body", "compatibility_response"} {
		t.Run(phase, func(t *testing.T) {
			cfg := shared.DefaultProxyConfig()
			cfg.NonStreamTimeoutSeconds = 1
			cfg.FirstByteTimeoutSeconds = 5
			cfg.RetryCount = 0
			cfg.RetryBackoffBaseMs = 20
			cfg.RetryBackoffMaxMs = 40
			attempts := &attemptRecorder{}
			calls := 0
			var breakers *BreakerStore
			router, key, logs := setupGatewayDeps(t, func(d *Deps) {
				withCfg(cfg)(d)
				d.AttemptWrite = attempts.write
				breakers = d.Breakers
				d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						select {
						case <-time.After(550 * time.Millisecond):
						case <-req.Context().Done():
							return nil, req.Context().Err()
						}
						status, body := 503, ""
						if phase == "compatibility_response" {
							status, body = 400, budgetUnsupportedLegacy
						}
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
					}
					if phase == "headers" {
						<-req.Context().Done()
						return nil, req.Context().Err()
					}
					status, prefix := 200, budgetChatSuccess
					if phase == "error_body" {
						status, prefix = 503, `{"error":{"message":"private unfinished error"}}`
					}
					if phase == "compatibility_body" {
						status, prefix = 400, budgetUnsupportedLegacy
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: &requestControlBody{ctx: req.Context(), prefix: strings.NewReader(prefix)}}, nil
				})}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") }))
			requestControlFallback(t)
			started := time.Now()
			result := doReq(t, router, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":32}`, key)
			elapsed := time.Since(started)
			rows := attempts.entries()
			if result.Code != 504 || calls != 2 || len(rows) != 2 {
				t.Fatalf("phase=%s status=%d calls=%d attempts=%+v body=%s", phase, result.Code, calls, rows, result.Body.String())
			}
			if elapsed < 900*time.Millisecond || elapsed > 1500*time.Millisecond {
				t.Fatalf("deadline reset across attempts: %s", elapsed)
			}
			if rows[1].ErrKind != shared.ErrKindNonStreamDeadline || rows[1].Sequence != 2 {
				t.Fatalf("deadline attribution: %+v", rows[1])
			}
			if phase == "compatibility_response" {
				if rows[0].Status != 400 || rows[0].ErrKind != shared.ErrKindUpstream || rows[1].Reason != "budget_compat" {
					t.Fatalf("compat attempt attribution: %+v", rows)
				}
			} else if rows[1].Reason != "failover" {
				t.Fatalf("expected target failover: %+v", rows)
			}
			if strings.Contains(result.Body.String(), "private unfinished") || strings.Contains(result.Body.String(), "unsupported_parameter") || strings.Contains(result.Body.String(), `"text":"ok"`) {
				t.Fatalf("uncommitted upstream body leaked: %s", result.Body.String())
			}
			if entry := logs.last(); entry == nil || entry.Success || entry.ErrKind != shared.ErrKindNonStreamDeadline {
				t.Fatalf("deadline relay log: %+v", entry)
			}
			if phase != "compatibility_response" {
				assertNoLocalControlBreakerFailure(t, breakers, rows[1].ModelID)
			}
		})
	}
}

func assertNoLocalControlBreakerFailure(t *testing.T, breakers *BreakerStore, modelID int64) {
	t.Helper()
	if value, ok := breakers.m.Load(modelID); ok {
		breaker := value.(*circuitBreaker)
		breaker.mu.Lock()
		failures := breaker.consecutiveFailure
		breaker.mu.Unlock()
		if failures != 0 {
			t.Fatalf("local control failure fed breaker: %d", failures)
		}
	}
}

func TestNonStreamCanceledBackoffDoesNotCreateAttempt(t *testing.T) {
	for _, compatibility := range []bool{false, true} {
		name := "ordinary_retry"
		if compatibility {
			name = "compatibility_retry"
		}
		t.Run(name, func(t *testing.T) {
			cfg := shared.DefaultProxyConfig()
			cfg.RetryCount = 3
			cfg.RetryBackoffBaseMs = 500
			cfg.RetryBackoffMaxMs = 500
			attempts := &attemptRecorder{}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			router, key, _ := setupGatewayDeps(t, func(d *Deps) {
				withCfg(cfg)(d)
				d.AttemptWrite = attempts.write
				d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					time.AfterFunc(40*time.Millisecond, cancel)
					status, body := 503, ""
					if compatibility {
						status, body = 400, budgetUnsupportedLegacy
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
			}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") }))
			requestControlFallback(t)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"my-model","input":"hello","max_output_tokens":32}`)).WithContext(parent)
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			started := time.Now()
			router.ServeHTTP(httptest.NewRecorder(), req)
			rows := attempts.entries()
			if calls != 1 || len(rows) != 1 || rows[0].Sequence != 1 || rows[0].ErrKind != shared.ErrKindUpstream {
				t.Fatalf("canceled backoff sent or invented attempt: calls=%d rows=%+v", calls, rows)
			}
			if compatibility && rows[0].Status != 400 {
				t.Fatalf("original rejection lost: %+v", rows)
			}
			if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
				t.Fatalf("backoff ignored cancellation: %s", elapsed)
			}
		})
	}
}

func TestNonStreamDeadlineDuringCompatibilityBackoff(t *testing.T) {
	cfg := shared.DefaultProxyConfig()
	cfg.NonStreamTimeoutSeconds = 1
	cfg.RetryBackoffBaseMs = 1000
	cfg.RetryBackoffMaxMs = 1000
	attempts := &attemptRecorder{}
	calls := 0
	var breakers *BreakerStore
	router, key, logs := setupGatewayDeps(t, func(d *Deps) {
		withCfg(cfg)(d)
		breakers = d.Breakers
		d.AttemptWrite = attempts.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			calls++
			select {
			case <-time.After(550 * time.Millisecond):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(budgetUnsupportedLegacy))}, nil
		})}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") }))
	requestControlFallback(t)
	result := doReq(t, router, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":32}`, key)
	rows := attempts.entries()
	if result.Code != 504 || calls != 1 || len(rows) != 1 || rows[0].Status != 400 || rows[0].ErrKind != shared.ErrKindUpstream {
		t.Fatalf("expiry fabricated compatibility attempt: status=%d calls=%d rows=%+v body=%s", result.Code, calls, rows, result.Body.String())
	}
	if entry := logs.last(); entry == nil || entry.ErrKind != shared.ErrKindNonStreamDeadline {
		t.Fatalf("expiry attribution=%+v", entry)
	}
	assertNoLocalControlBreakerFailure(t, breakers, rows[0].ModelID)
}

func TestNonStreamBackoffPreservesCompatibilityAndFailoverSequence(t *testing.T) {
	cfg := shared.DefaultProxyConfig()
	cfg.RetryCount = 1
	cfg.RetryBackoffBaseMs = 30
	cfg.RetryBackoffMaxMs = 40
	attempts := &attemptRecorder{}
	var sent []time.Time
	var bodies []budgetAttempt
	router, key, _ := setupGatewayDeps(t, func(d *Deps) {
		withCfg(cfg)(d)
		d.AttemptWrite = attempts.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			sent = append(sent, time.Now())
			bodies = append(bodies, captureBudgetAttempt(t, req))
			status, body := 200, budgetChatSuccess
			switch len(sent) {
			case 1:
				status, body = 400, budgetUnsupportedLegacy
			case 2, 3:
				status, body = 503, ""
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
	}, protocol.ProviderOpenAI, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") }))
	requestControlFallback(t)
	result := doReq(t, router, "/v1/responses", `{"model":"my-model","input":"hello","max_output_tokens":32}`, key)
	rows := attempts.entries()
	if result.Code != 200 || len(rows) != 4 || len(sent) != 4 {
		t.Fatalf("compatible success changed: status=%d attempts=%+v body=%s", result.Code, rows, result.Body.String())
	}
	reasons := []string{"initial", "budget_compat", "retry", "failover"}
	fields := []string{"max_tokens", "max_completion_tokens", "max_completion_tokens", "max_tokens"}
	for i, row := range rows {
		if row.Reason != reasons[i] || row.Sequence != i+1 || row.BudgetField != fields[i] || bodies[i].body[fields[i]] != float64(32) {
			t.Errorf("attempt %d changed: %+v body=%+v", i+1, row, bodies[i])
		}
		if i > 0 && sent[i].Sub(sent[i-1]) < 10*time.Millisecond {
			t.Errorf("attempt %d bypassed backoff: %s", i+1, sent[i].Sub(sent[i-1]))
		}
	}
}

func TestNonStreamNativeOperationFailureDoesNotReplay(t *testing.T) {
	cfg := shared.DefaultProxyConfig()
	cfg.RetryCount = 3
	cfg.RetryBackoffBaseMs = 1
	cfg.RetryBackoffMaxMs = 2
	attempts := &attemptRecorder{}
	calls := 0
	router, key, logs := setupGatewayDeps(t, func(d *Deps) {
		withCfg(cfg)(d)
		d.AttemptWrite = attempts.write
		d.Client = &http.Client{Transport: attemptRoundTripper(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`))}, nil
		})}
	}, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected server request") }))
	requestControlFallback(t)
	result := doReq(t, router, "/v1/responses", `{"model":"my-model","input":"click button","tools":[{"type":"computer_use_preview"}]}`, key)
	if result.Code != 503 || calls != 1 || len(attempts.entries()) != 1 || logs.last() == nil || logs.last().Success {
		t.Fatalf("unsafe nonstream request replayed: status=%d calls=%d attempts=%+v log=%+v", result.Code, calls, attempts.entries(), logs.last())
	}
}
