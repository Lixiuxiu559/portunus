package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDetectNewAPIUnitsTypesAndExactRatios(t *testing.T) {
	for _, tc := range []struct{ name, data, ratio, unit string }{
		{"USD", `{"quota_per_unit":500000,"quota_display_type":"USD"}`, "500000", "USD"},
		{"CNY", `{"quota_per_unit":"500000","quota_display_type":"CNY","usd_exchange_rate":"7"}`, "500000/7", "CNY"},
		{"CNY decimal", `{"quota_per_unit":500000,"quota_display_type":"CNY","usd_exchange_rate":8}`, "62500", "CNY"},
		{"CUSTOM", `{"quota_per_unit":500000,"quota_display_type":"CUSTOM","custom_currency_symbol":"€","custom_currency_exchange_rate":0.9}`, "5000000/9", "€"},
		{"TOKENS", `{"quota_per_unit":500000,"quota_display_type":"TOKENS"}`, "1", "QUOTA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := detectNewAPIUnits([]byte(`{"success":true,"data":` + tc.data + `}`))
			if err != nil || got.QuotaPerUnit != tc.ratio || got.DisplayUnit != tc.unit || got.UserID != "" || got.Custom != nil {
				t.Fatalf("got %+v, %v", got, err)
			}
			if err := validateNewAPIOptions(QueryOptions{UserID: "1", QuotaPerUnit: got.QuotaPerUnit, DisplayUnit: got.DisplayUnit}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDetectNewAPIUnitsRejectsIncompleteAmbiguousAndInvalidData(t *testing.T) {
	for _, data := range []string{
		`{}`, `{"quota_per_unit":500000,"display_in_currency":true}`,
		`{"quota_per_unit":500000,"quota_display_type":"unknown"}`,
		`{"quota_per_unit":0,"quota_display_type":"USD"}`,
		`{"quota_per_unit":-1,"quota_display_type":"USD"}`,
		`{"quota_per_unit":true,"quota_display_type":"USD"}`,
		`{"quota_per_unit":null,"quota_display_type":"USD"}`,
		`{"quota_per_unit":500000,"quota_display_type":"CNY"}`,
		`{"quota_per_unit":500000,"quota_display_type":"CNY","usd_exchange_rate":0}`,
		`{"quota_per_unit":500000,"quota_display_type":"CNY","usd_exchange_rate":false}`,
		`{"quota_per_unit":500000,"quota_display_type":"CUSTOM","custom_currency_exchange_rate":1}`,
		`{"quota_per_unit":500000,"quota_display_type":"CUSTOM","custom_currency_exchange_rate":1,"custom_currency_symbol":" bad "}`,
		fmt.Sprintf(`{"quota_per_unit":%q,"quota_display_type":"USD"}`, strings.Repeat("1", 129)),
		fmt.Sprintf(`{"quota_per_unit":1,"quota_display_type":"CUSTOM","custom_currency_exchange_rate":1,"custom_currency_symbol":%q}`, strings.Repeat("a", 25)),
	} {
		if got, err := detectNewAPIUnits([]byte(`{"success":true,"data":` + data + `}`)); err == nil || got != (QueryOptions{}) {
			t.Fatalf("accepted invalid %s: %+v %v", data, got, err)
		}
	}
	for _, body := range []string{`{}`, `{"success":false,"data":{"quota_per_unit":1,"quota_display_type":"USD"}}`, `{"success":true,"data":null}`, `{"success":"true","data":{}}`, `{"success":true} trailing`} {
		if _, err := detectNewAPIUnits([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestNewAPIPreciseFractionConversionWalletAndSubscriptions(t *testing.T) {
	options, err := detectNewAPIUnits([]byte(`{"success":true,"data":{"quota_per_unit":500000,"quota_display_type":"CNY","usd_exchange_rate":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	options.UserID = "7"
	now := time.Now().Unix()
	snapshot, failure, _ := newAPIExecute(t, context.Background(), options, map[string]newAPIReply{
		"/api/user/self":          {200, `{"success":true,"data":{"quota":2500000,"used_quota":500000}}`},
		"/api/subscription/self":  {200, subscriptionsFixture(subscriptionFixture(1, 1, "3000000", "500000", "active", now-60, now+86400, 0))},
		"/api/subscription/plans": {200, `{"success":true,"data":[{"plan":{"id":1,"title":"test"}}]}`},
	})
	if failure != nil || snapshot == nil {
		t.Fatal(failure)
	}
	if len(snapshot.Items) != 2 || *snapshot.Items[0].Remaining != "35" || *snapshot.Items[0].Used != "7" || *snapshot.Items[1].Remaining != "35" || *snapshot.Items[1].Total != "42" || *snapshot.NewAPI.AccountSummary.Remaining != "70" || *snapshot.NewAPI.SubscriptionSummary.Remaining != "35" {
		b, _ := json.Marshal(snapshot)
		t.Fatalf("imprecise conversion %s", b)
	}
}

func TestNewAPIQuotaFractionValidationIsBoundedAndLocal(t *testing.T) {
	for _, valid := range []string{"500000/7", "1.5/0.5", "1e3/2", "500000", "0.0001"} {
		if _, err := parseNewAPIQuotaPerUnit(valid); err != nil {
			t.Fatalf("rejected %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "1/0", "-1/2", "1/-2", "1/2/3", "1/", "/2", " 1/2", strings.Repeat("1", 129), "1e129/2", "1/true"} {
		if _, err := parseNewAPIQuotaPerUnit(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if _, err := parseDecimal("500000/7"); err == nil {
		t.Fatal("generic decimal parser widened")
	}
}

func TestDetectNewAPIUnitsPublicRequestPrefixAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		fail   bool
	}{
		{"success", 200, `{"success":true,"data":{"quota_per_unit":500000,"quota_display_type":"USD"}}`, false},
		{"missing", 200, `{"success":true,"data":{"email_verification":false}}`, true},
		{"business", 200, `{"success":false,"message":"private upstream message"}`, true},
		{"404", 404, `private upstream message`, true},
		{"429", 429, `private upstream message`, true},
		{"redirect", 302, `private upstream message`, true},
		{"too large", 200, strings.Repeat("x", maxResponseBytes+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			svc := NewService(nil, quotaDoer(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://site.example/prefix/api/status" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("New-Api-User") != "" || r.Header.Get("Cookie") != "" {
					t.Fatalf("unsafe request %s headers %+v", r.URL, r.Header)
				}
				if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 15*time.Second {
					t.Fatal("unbounded request")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}), nil)
			got, err := svc.DetectNewAPIUnits(context.Background(), "https://site.example/prefix/")
			if calls != 1 || (err != nil) != tc.fail {
				t.Fatalf("calls %d got %+v error %v", calls, got, err)
			}
			if tc.fail && (got != (QueryOptions{}) || strings.Contains(err.Error(), "private upstream")) {
				t.Fatal("unsafe failure", err)
			}
		})
	}
	calls := 0
	svc := NewService(nil, quotaDoer(func(r *http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected") }), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.DetectNewAPIUnits(ctx, "https://site.example"); err == nil || calls != 0 {
		t.Fatal("cancelled request ran")
	}
	for _, base := range []string{"", "https://user:secret@site.example", "https://site.example?secret=x", "file:///tmp/status"} {
		if _, err := svc.DetectNewAPIUnits(context.Background(), base); err == nil {
			t.Fatal("invalid base accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid request sent")
	}
	if len(svc.slots) != 0 {
		t.Fatal("semaphore leaked")
	}
}

func TestDetectNewAPIUnitsRejectsRedirectsAndBoundsConcurrency(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: quotaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://other.example/status"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	svc := NewService(nil, client, nil)
	if _, err := svc.DetectNewAPIUnits(context.Background(), "https://site.example"); err == nil || calls != 1 {
		t.Fatalf("redirect not bounded: %d %v", calls, err)
	}
	svc.slots <- struct{}{}
	svc.slots <- struct{}{}
	if _, err := svc.DetectNewAPIUnits(context.Background(), "https://site.example"); err == nil || calls != 1 {
		t.Fatal("full concurrency budget reached upstream", calls, err)
	}
	<-svc.slots
	<-svc.slots
}
