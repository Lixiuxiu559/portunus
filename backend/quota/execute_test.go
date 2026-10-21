package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type quotaDoer func(*http.Request) (*http.Response, error)

func (d quotaDoer) Do(r *http.Request) (*http.Response, error) { return d(r) }
func executeFixture(t *testing.T, doer HTTPDoer) (*Snapshot, *Failure) {
	t.Helper()
	a, _ := LookupAdapter("deepseek.balance")
	return Execute(context.Background(), doer, a, ResolvedSource{Token: "secret-token"})
}
func TestExecuteSuccessAndStatusClassification(t *testing.T) {
	for _, tc := range []struct {
		status   int
		category string
		retry    bool
	}{{200, "", false}, {401, "authentication", false}, {403, "permission", false}, {429, "rate_limited", true}, {500, "temporary", true}, {503, "temporary", true}, {302, "configuration", false}, {400, "business", false}} {
		calls := 0
		s, f := executeFixture(t, quotaDoer(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != "https://api.deepseek.com/user/balance" {
				t.Fatal(r.URL)
			}
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > 15*time.Second {
				t.Fatal("request deadline missing")
			}
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"99999"}}, Body: io.NopCloser(strings.NewReader(deepSeekFixture))}, nil
		}))
		if calls != 1 {
			t.Fatal("query repeated")
		}
		if tc.status == 200 {
			if f != nil || s == nil {
				t.Fatal(f)
			}
			continue
		}
		if s != nil || f == nil || f.Category != tc.category || f.Retryable != tc.retry || f.UpstreamStatus != tc.status {
			t.Fatalf("%d => %+v", tc.status, f)
		}
		if tc.status == 429 && f.RetryAfterSeconds != 3600 {
			t.Fatal(f)
		}
	}
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("secret-token in body") }
func (failingBody) Close() error             { return nil }
func TestExecuteResponseLimitsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		body   io.ReadCloser
		length int64
		code   string
	}{{io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1))), 0, "response_too_large"}, {io.NopCloser(strings.NewReader("{}")), maxResponseBytes + 1, "response_too_large"}, {failingBody{}, 0, "response_read_error"}, {io.NopCloser(strings.NewReader("<html>secret-token</html>")), 0, "invalid_response"}} {
		_, f := executeFixture(t, quotaDoer(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: tc.body, ContentLength: tc.length}, nil
		}))
		if f == nil || f.Code != tc.code {
			t.Fatalf("want %s got %+v", tc.code, f)
		}
		b, _ := json.Marshal(f)
		if strings.Contains(string(b), "secret-token") {
			t.Fatal("leaked credential")
		}
	}
	_, f := executeFixture(t, quotaDoer(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("https://secret-token@example.com")
	}))
	if f.Category != "temporary" || strings.Contains(f.Message, "secret-token") {
		t.Fatal(f)
	}
}
func TestExecuteContextCancellation(t *testing.T) {
	a, _ := LookupAdapter("deepseek.balance")
	for _, cancelled := range []bool{true, false} {
		ctx := context.Background()
		var cancel context.CancelFunc
		if cancelled {
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		} else {
			ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
		}
		_, f := Execute(ctx, quotaDoer(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }), a, ResolvedSource{Token: "fixture"})
		want := "timeout"
		if cancelled {
			want = "cancelled"
		}
		if f.Code != want {
			t.Fatalf("got %+v", f)
		}
	}
}

type quotaTransport func(*http.Request) (*http.Response, error)

func (r quotaTransport) RoundTrip(req *http.Request) (*http.Response, error) { return r(req) }
func TestExecuteRejectsRedirect(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.example/target"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	a, _ := LookupAdapter("newapi.account")
	_, f := Execute(context.Background(), client, a, ResolvedSource{QueryBaseURL: "https://example.com", Token: "fixture", Options: QueryOptions{UserID: "1"}})
	if f == nil || f.Code != "redirect_rejected" || calls != 1 {
		t.Fatalf("redirect followed: calls=%d failure=%+v", calls, f)
	}
}

func TestExecuteInvalidConfigurationDoesNotSend(t *testing.T) {
	calls := 0
	a, _ := LookupAdapter("deepseek.balance")
	_, f := Execute(context.Background(), quotaDoer(func(*http.Request) (*http.Response, error) { calls++; return nil, nil }), a, ResolvedSource{Token: "fixture", QueryBaseURL: "https://evil.example"})
	if calls != 0 || f.Category != "configuration" {
		t.Fatal(f)
	}
}
func TestRetryAfterBoundaries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		value string
		want  int64
	}{{"-1", 0}, {"abc", 0}, {"0", 0}, {"60", 60}, {"7200", 3600}, {now.Add(time.Minute).Format(http.TimeFormat), 60}, {now.Add(2 * time.Hour).Format(http.TimeFormat), 3600}, {now.Add(-time.Hour).Format(http.TimeFormat), 0}} {
		if got := retryAfter(tc.value, now); got != tc.want {
			t.Fatalf("%s got %d", tc.value, got)
		}
	}
	client := NewHTTPClient()
	if client.Timeout != 15*time.Second || client.CheckRedirect == nil {
		t.Fatal("missing dedicated client policy")
	}
}

func TestExecuteCustomPOSTIsSingleBoundedJSONRead(t *testing.T) {
	c := customFixture()
	c.Request.Method = "POST"
	c.Request.Body = `{"account_id":7}`
	calls := 0
	s, f := Execute(context.Background(), quotaDoer(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(body) != c.Request.Body || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Fatal("request mismatch")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"balance":"1.25"}}`)), Header: http.Header{}}, nil
	}), customAdapter{}, customSource(c))
	if calls != 1 || f != nil || *s.Items[0].Remaining != "1.25" {
		t.Fatalf("calls=%d failure=%+v", calls, f)
	}
	for _, tc := range []struct {
		d Descriptor
		s RequestSpec
	}{
		{Descriptor{}, RequestSpec{Method: "POST", Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{}`)}},
		{Descriptor{RequestMethods: []string{"POST"}}, RequestSpec{Method: "DELETE"}},
		{Descriptor{RequestMethods: []string{"POST"}}, RequestSpec{Method: "POST", Headers: http.Header{"Content-Type": {"text/html"}}, Body: []byte(`{}`)}},
		{Descriptor{RequestMethods: []string{"POST"}}, RequestSpec{Method: "POST", Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`invalid`)}},
		{Descriptor{RequestMethods: []string{"POST"}}, RequestSpec{Method: "POST", Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(strings.Repeat(" ", maxCustomBodyBytes+1))}},
		{Descriptor{RequestMethods: []string{"POST"}}, RequestSpec{Method: "GET", Body: []byte(`{}`)}},
	} {
		if validRequestMethod(tc.d, tc.s) {
			t.Fatal("unapproved request", tc.s.Method)
		}
	}
}
