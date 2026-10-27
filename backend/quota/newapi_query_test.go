package quota

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func subscriptionFixture(id, plan int, total, used, status string, start, end, reset int64) string {
	return fmt.Sprintf(`{"subscription":{"id":%d,"plan_id":%d,"amount_total":%s,"amount_used":%s,"status":%q,"start_time":%d,"end_time":%d,"next_reset_time":%d}}`, id, plan, total, used, status, start, end, reset)
}
func subscriptionsFixture(entries ...string) string {
	return `{"success":true,"data":{"billing_preference":"subscription_first","subscriptions":[` + strings.Join(entries, ",") + `]}}`
}

type newAPIReply struct {
	status int
	body   string
}

func newAPIExecute(t *testing.T, ctx context.Context, options QueryOptions, replies map[string]newAPIReply) (*Snapshot, *Failure, []string) {
	t.Helper()
	var paths []string
	s, f := Execute(ctx, quotaDoer(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		if r.URL.Scheme != "https" || r.URL.Host != "newapi.example" || r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("New-Api-User") != "7" || r.Method != "GET" {
			t.Fatal("request identity changed")
		}
		reply, ok := replies[r.URL.Path]
		if !ok {
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 15*time.Second {
			t.Fatal("unbounded request")
		}
		return &http.Response{StatusCode: reply.status, Header: http.Header{"Retry-After": {"21"}}, Body: io.NopCloser(strings.NewReader(reply.body))}, nil
	}), newAPIAdapter{}, ResolvedSource{QueryBaseURL: "https://newapi.example", Token: "fixture", Options: options})
	return s, f, paths
}
func TestNewAPIWalletZeroAndIndependentSubscriptions(t *testing.T) {
	now := time.Now().Unix()
	replies := map[string]newAPIReply{
		"/api/user/self":          {200, `{"success":true,"data":{"quota":0,"used_quota":0}}`},
		"/api/subscription/self":  {200, subscriptionsFixture(subscriptionFixture(11, 3, "1000", "100", "active", now-60, now+86400, now+3600), subscriptionFixture(12, 3, "2000", "600", "active", now-60, now+86400, 0))},
		"/api/subscription/plans": {200, `{"success":true,"data":[{"plan":{"id":3,"title":"月度套餐","currency":"USD"}}]}`},
	}
	s, f, paths := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7", QuotaPerUnit: "100", DisplayUnit: "credits"}, replies)
	if f != nil || s == nil {
		t.Fatal(f)
	}
	if len(paths) != 3 || len(s.Items) != 3 || *s.Items[0].Remaining != "0" || s.Items[0].Label != "钱包额度" || s.NewAPI.Wallet.Status != "ok" || s.NewAPI.Subscriptions.Status != "ok" || s.NewAPI.BillingPreference != "subscription_first" {
		t.Fatalf("snapshot %+v", s)
	}
	for i, id := range []string{"11", "12"} {
		item := s.Items[i+1]
		if item.ID != "subscription:"+id || item.SubscriptionID != id || item.PlanID != "3" || item.Label != "月度套餐" || item.Kind != "usage_window" || item.Scope != "plan" || item.Unit.Code != "credits" || item.AccountStatus != "active" || item.ExpiresAt == nil || item.ExpiresAt.Unix() != now+86400 {
			t.Fatalf("item %+v", item)
		}
	}
	first := s.Items[1]
	if *first.Total != "10" || *first.Used != "1" || *first.Remaining != "9" || *first.UsedPercent != 10 || first.Original == nil || *first.Original.Remaining != "900" || first.ResetsAt.Unix() != now+3600 || s.Items[2].ResetsAt != nil {
		t.Fatalf("conversion/reset %+v", first)
	}
}
func TestNewAPIPartialFailuresAndEmptySubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		wallet, sub                 newAPIReply
		walletState, subState, code string
		count                       int
	}{
		{"explicit empty", newAPIReply{200, newAPIFixture}, newAPIReply{200, subscriptionsFixture()}, "ok", "ok", "", 1},
		{"no wallet", newAPIReply{403, `{}`}, newAPIReply{200, subscriptionsFixture()}, "error", "ok", "", 0},
		{"missing subscriptions", newAPIReply{200, newAPIFixture}, newAPIReply{200, `{"success":true,"data":{"all_subscriptions":[]}}`}, "ok", "error", "invalid_response", 1},
		{"old version", newAPIReply{200, newAPIFixture}, newAPIReply{404, `{}`}, "ok", "unsupported", "upstream_http_error", 1},
		{"permission", newAPIReply{200, newAPIFixture}, newAPIReply{403, `{}`}, "ok", "error", "permission_denied", 1},
		{"business", newAPIReply{200, newAPIFixture}, newAPIReply{200, `{"success":false,"message":"private"}`}, "ok", "error", "upstream_business_error", 1},
		{"rate limit", newAPIReply{200, newAPIFixture}, newAPIReply{429, `{}`}, "ok", "error", "rate_limited", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, paths := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7"}, map[string]newAPIReply{"/api/user/self": tc.wallet, "/api/subscription/self": tc.sub})
			if f != nil || !validateSnapshot(s) || len(s.Items) != tc.count || len(paths) != 2 || s.NewAPI.Wallet.Status != tc.walletState || s.NewAPI.Subscriptions.Status != tc.subState || s.NewAPI.Plans != nil {
				t.Fatalf("snapshot=%+v failure=%+v", s, f)
			}
			if tc.code != "" && (s.NewAPI.Subscriptions.Failure == nil || s.NewAPI.Subscriptions.Failure.Code != tc.code) {
				t.Fatal(s.NewAPI.Subscriptions)
			}
			if tc.code == "rate_limited" && s.NewAPI.Subscriptions.Failure.RetryAfterSeconds != 21 {
				t.Fatal("missing retry-after")
			}
		})
	}
}
func TestNewAPISubscriptionsSurviveWalletAndPlansFailures(t *testing.T) {
	now := time.Now().Unix()
	for _, planReply := range []newAPIReply{{403, `{}`}, {404, `{}`}, {200, `{"success":false}`}, {200, `{"success":true,"data":null}`}} {
		s, f, _ := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7"}, map[string]newAPIReply{
			"/api/user/self": {401, `{}`}, "/api/subscription/self": {200, subscriptionsFixture(subscriptionFixture(1, 2, "5", "2", "active", now-1, now+3600, 0))}, "/api/subscription/plans": planReply,
		})
		if f != nil || len(s.Items) != 1 || s.NewAPI.Wallet.Failure.Code != "authentication_failed" || s.NewAPI.Plans.Status == "ok" || s.Items[0].Label != "套餐 #2" || *s.Items[0].Remaining != "3" {
			t.Fatalf("snapshot=%+v f=%+v", s, f)
		}
	}
}
func TestNewAPILimitsExpiryAndExactDecimal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	entries := []string{
		subscriptionFixture(1, 2, "0", "9007199254740993.123456789", "active", now.Unix()-1, now.Unix()+7200, now.Unix()+3600),
		subscriptionFixture(2, 2, "5", "2", "active", now.Unix()-10, now.Unix(), 0),
		subscriptionFixture(3, 2, "5", "2", "cancelled", now.Unix()-1, now.Unix()+7200, 0),
		subscriptionFixture(4, 2, "5", "2", "active", now.Unix()+1, now.Unix()+7200, 0),
		subscriptionFixture(5, 2, "9007199254740993.123456789", "0.000000001", "active", now.Unix()-1, now.Unix()+7200, 0),
	}
	items, pref, f := parseNewAPISubscriptions(ResolvedSource{Options: QueryOptions{UserID: "7", QuotaPerUnit: "10", DisplayUnit: "CNY"}}, []byte(subscriptionsFixture(entries...)), now)
	if f != nil || len(items) != 2 || pref != "subscription_first" {
		t.Fatal(items, f)
	}
	if items[0].LimitState != "unlimited" || items[0].Remaining != nil || *items[0].Total != "0" || *items[0].Used != "900719925474099.3123456789" || items[0].ExpiresAt.Equal(*items[0].ResetsAt) {
		t.Fatal(items[0])
	}
	if *items[1].Remaining != "900719925474099.3123456788" || *items[1].Used != "0.0000000001" || *items[1].Original.Remaining != "9007199254740993.123456788" {
		t.Fatal(items[1])
	}
}
func TestNewAPIMalformedSubscriptionsNeverBecomeZero(t *testing.T) {
	now := time.Now().Unix()
	valid := subscriptionFixture(1, 2, "10", "2", "active", now-1, now+3600, 0)
	for _, entry := range []string{
		`{}`, strings.Replace(valid, `"amount_total":10,`, "", 1), strings.Replace(valid, `"amount_used":2`, `"amount_used":null`, 1), strings.Replace(valid, `"amount_total":10`, `"amount_total":true`, 1), strings.Replace(valid, `"amount_used":2`, `"amount_used":-1`, 1), strings.Replace(valid, `"next_reset_time":0`, `"next_reset_time":"bad"`, 1), strings.Replace(valid, `"id":1`, `"id":1.1`, 1), strings.Replace(valid, `"plan_id":2`, `"plan_id":null`, 1), valid + "," + valid,
	} {
		if items, _, f := parseNewAPISubscriptions(ResolvedSource{}, []byte(subscriptionsFixture(entry)), time.Now()); f == nil || items != nil {
			t.Fatalf("accepted %s", entry)
		}
	}
	for _, body := range []string{`null`, `{}`, `{"success":true,"data":{}}`, `{"success":true,"data":{"subscriptions":null}}`, `{"success":true,"data":{"subscriptions":{}}}`, `{"success":true,"data":{"subscriptions":[],"billing_preference":false}}`} {
		if _, _, f := parseNewAPISubscriptions(ResolvedSource{}, []byte(body), time.Now()); f == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
func TestNewAPIBothFailuresPreserveIdentityFailure(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		s, f, _ := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7"}, map[string]newAPIReply{"/api/user/self": {status, `{}`}, "/api/subscription/self": {404, `{}`}})
		if s != nil || f == nil || f.UpstreamStatus != status {
			t.Fatalf("snapshot=%+v failure=%+v", s, f)
		}
	}
}
func TestNewAPICancellationDoesNotPublishPartialSnapshot(t *testing.T) {
	for _, cancelPath := range []string{"/api/user/self", "/api/subscription/self", "/api/subscription/plans"} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		s, f := Execute(ctx, quotaDoer(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path == cancelPath {
				cancel()
				return nil, ctx.Err()
			}
			body := newAPIFixture
			if r.URL.Path == "/api/subscription/self" {
				now := time.Now().Unix()
				body = subscriptionsFixture(subscriptionFixture(1, 2, "10", "2", "active", now-1, now+3600, 0))
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		}), newAPIAdapter{}, ResolvedSource{QueryBaseURL: "https://newapi.example", Token: "fixture", Options: QueryOptions{UserID: "7"}})
		cancel()
		if s != nil || f == nil || f.Code != "cancelled" || calls > 3 {
			t.Fatalf("cancel %s: snapshot=%+v failure=%+v calls=%d", cancelPath, s, f, calls)
		}
	}
}

func TestNewAPICompositeDeadlineAndInvalidConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	calls := 0
	s, f := Execute(ctx, quotaDoer(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(newAPIFixture))}, nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}), newAPIAdapter{}, ResolvedSource{QueryBaseURL: "https://newapi.example", Token: "fixture", Options: QueryOptions{UserID: "7"}})
	if s != nil || f == nil || f.Code != "timeout" || calls != 2 {
		t.Fatalf("snapshot=%+v failure=%+v calls=%d", s, f, calls)
	}
	for _, source := range []ResolvedSource{
		{QueryBaseURL: "https://newapi.example", Token: "fixture"},
		{QueryBaseURL: "https://secret@newapi.example", Token: "fixture", Options: QueryOptions{UserID: "7"}},
		{QueryBaseURL: "https://newapi.example", Token: "fixture", Options: QueryOptions{UserID: "7", QuotaPerUnit: "0", DisplayUnit: "USD"}},
	} {
		calls = 0
		_, f = Execute(context.Background(), quotaDoer(func(*http.Request) (*http.Response, error) { calls++; return nil, nil }), newAPIAdapter{}, source)
		if f == nil || f.Category != "configuration" || calls != 0 {
			t.Fatal("invalid configuration sent", f, calls)
		}
	}
}
func TestNewAPICombinedResponseBudget(t *testing.T) {
	body := newAPIFixture + strings.Repeat(" ", maxResponseBytes-len(newAPIFixture)-20)
	s, f, paths := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7"}, map[string]newAPIReply{
		"/api/user/self": {200, body}, "/api/subscription/self": {200, subscriptionsFixture()},
	})
	if f != nil || s == nil || len(paths) != 2 || s.NewAPI.Subscriptions.Failure == nil || s.NewAPI.Subscriptions.Failure.Code != "response_too_large" || len(s.Items) != 1 {
		t.Fatalf("snapshot=%+v failure=%+v", s, f)
	}
}

func TestNewAPISubscriptionSummaryUsesOriginalQuota(t *testing.T) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name      string
		entries   []string
		options   QueryOptions
		remaining string
		unlimited int
	}{
		{"empty", nil, QueryOptions{UserID: "7"}, "0", 0},
		{"independent and tiny", []string{
			subscriptionFixture(1, 2, "9007199254740993.123456789", "0", "active", now-1, now+3600, now+1800),
			subscriptionFixture(2, 2, "0.000000001", "0", "active", now-1, now+7200, now+900),
		}, QueryOptions{UserID: "7"}, "9007199254740993.12345679", 0},
		{"convert sum once", []string{
			subscriptionFixture(1, 2, "1", "0", "active", now-1, now+3600, now+1800),
			subscriptionFixture(2, 2, "1", "0", "active", now-1, now+7200, now+900),
		}, QueryOptions{UserID: "7", QuotaPerUnit: "3", DisplayUnit: "credits"}, "0.666666666666666667", 0},
		{"mixed unlimited", []string{
			subscriptionFixture(1, 2, "0", "5", "active", now-1, now+3600, now+1800),
			subscriptionFixture(2, 2, "1", "0", "active", now-1, now+7200, now+900),
		}, QueryOptions{UserID: "7"}, "", 1},
		{"all unlimited", []string{subscriptionFixture(1, 2, "0", "5", "active", now-1, now+3600, now+1800)}, QueryOptions{UserID: "7"}, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _ := newAPIExecute(t, context.Background(), tc.options, map[string]newAPIReply{
				"/api/user/self":          {200, `{"success":true,"data":{"quota":999999,"used_quota":1}}`},
				"/api/subscription/self":  {200, subscriptionsFixture(tc.entries...)},
				"/api/subscription/plans": {200, `{"success":true,"data":[]}`},
			})
			if f != nil || s == nil || s.NewAPI.SubscriptionSummary == nil {
				t.Fatalf("missing summary: %+v %v", s, f)
			}
			summary := s.NewAPI.SubscriptionSummary
			if summary.Count != len(tc.entries) || summary.UnlimitedCount != tc.unlimited || len(s.Items) != 1+len(tc.entries) {
				t.Fatalf("mixed wallet/subscriptions: %+v", summary)
			}
			if tc.unlimited > 0 {
				if summary.Remaining != nil {
					t.Fatal("unlimited subscriptions have a finite aggregate")
				}
			} else if summary.Remaining == nil || *summary.Remaining != tc.remaining {
				t.Fatalf("remaining: %+v want %s", summary, tc.remaining)
			}
			unit := "QUOTA"
			if tc.options.DisplayUnit != "" {
				unit = tc.options.DisplayUnit
			}
			if summary.Unit.Code != unit {
				t.Fatal(summary.Unit)
			}
			if len(tc.entries) > 0 && (summary.ExpiresAt == nil || summary.ExpiresAt.Unix() != now+3600) {
				t.Fatal("wrong earliest expiry", summary)
			}
			if len(tc.entries) > 1 && (summary.ResetsAt == nil || summary.ResetsAt.Unix() != now+900) {
				t.Fatal("wrong next reset", summary)
			}
		})
	}
}

func TestNewAPIPrimaryFailuresKeepLongestRateLimit(t *testing.T) {
	for _, walletStatus := range []int{500, 429} {
		s, f, _ := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7"}, map[string]newAPIReply{
			"/api/user/self": {walletStatus, `{}`}, "/api/subscription/self": {429, `{}`},
		})
		if s != nil || f == nil || f.Category != "rate_limited" || f.RetryAfterSeconds != 21 {
			t.Fatalf("lost primary rate limit: snapshot=%+v failure=%+v", s, f)
		}
	}
	_, f := Execute(context.Background(), quotaDoer(func(r *http.Request) (*http.Response, error) {
		retry := "30"
		if r.URL.Path == "/api/subscription/self" {
			retry = "120"
		}
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {retry}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}), newAPIAdapter{}, ResolvedSource{QueryBaseURL: "https://newapi.example", Token: "fixture", Options: QueryOptions{UserID: "7"}})
	if f == nil || f.RetryAfterSeconds != 120 {
		t.Fatal("lost longer subscription cooldown", f)
	}
}

func TestNewAPIHundredSubscriptionsPlusWallet(t *testing.T) {
	now := time.Now().Unix()
	var entries []string
	for i := 1; i <= 100; i++ {
		entries = append(entries, subscriptionFixture(i, 2, "10", "1", "active", now-1, now+3600, 0))
	}
	s, f, _ := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7"}, map[string]newAPIReply{
		"/api/user/self": {200, newAPIFixture}, "/api/subscription/self": {200, subscriptionsFixture(entries...)}, "/api/subscription/plans": {200, `{"success":true,"data":[]}`},
	})
	if f != nil || s == nil || len(s.Items) != 101 || s.NewAPI.SubscriptionSummary.Count != 100 || *s.NewAPI.SubscriptionSummary.Remaining != "900" {
		t.Fatalf("lost subscriptions at capacity: %+v %v", s, f)
	}
}

func TestNewAPIAccountSummaryCombinesWalletAndSubscriptionsBeforeConversion(t *testing.T) {
	now := time.Now().Unix()
	for _, tc := range []struct {
		name, wallet, total, used, want string
		unlimited                       bool
	}{
		{"exact conversion", "1", "1", "0", "1", false},
		{"tiny remainder", "0", "1", "0", "0.666666666666666667", false},
		{"unlimited", "100", "0", "1", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _ := newAPIExecute(t, context.Background(), QueryOptions{UserID: "7", QuotaPerUnit: "3", DisplayUnit: "CNY"}, map[string]newAPIReply{
				"/api/user/self":          {200, fmt.Sprintf(`{"success":true,"data":{"quota":%s,"used_quota":0}}`, tc.wallet)},
				"/api/subscription/self":  {200, subscriptionsFixture(subscriptionFixture(1, 2, tc.total, tc.used, "active", now-1, now+3600, 0), subscriptionFixture(2, 2, tc.total, tc.used, "active", now-1, now+3600, 0))},
				"/api/subscription/plans": {200, `{"success":true,"data":[]}`},
			})
			if f != nil || s == nil || s.NewAPI.AccountSummary == nil {
				t.Fatal(s, f)
			}
			total := s.NewAPI.AccountSummary
			if total.Unit.Code != "CNY" || len(s.Items) != 3 {
				t.Fatal(total)
			}
			if tc.unlimited {
				if total.LimitState != "unlimited" || total.Remaining != nil {
					t.Fatal(total)
				}
			} else if total.Remaining == nil || *total.Remaining != tc.want || total.ExpiresAt == nil {
				t.Fatal(total)
			}
			if s.Items[0].Original == nil || *s.Items[0].Original.Remaining != tc.wallet {
				t.Fatal("detail lost original wallet")
			}
		})
	}
}
