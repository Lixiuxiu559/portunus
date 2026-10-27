package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/quota"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

type quotaHTTPFunc func(*http.Request) (*http.Response, error)

func (f quotaHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func quotaFixture(t *testing.T, client quota.HTTPDoer) (*gin.Engine, *quota.Service, *channel.Channel) {
	t.Helper()
	setupCascadeTest(t)
	if err := shared.AutoMigrate(&quota.Source{}, &quota.State{}); err != nil {
		t.Fatal(err)
	}
	ch, err := channel.Create(channel.CreateRequest{Name: "quota-channel", Type: protocol.ProviderOpenAI, BaseURL: "https://proxy.example/v1", Key: "channel-private-key"})
	if err != nil {
		t.Fatal(err)
	}
	svc := quota.NewService(shared.DB, client, func(tx *gorm.DB, id int64) (string, error) {
		var ch channel.Channel
		err := tx.First(&ch, id).Error
		return ch.Key, err
	})
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) { c.Set("quota_service", svc) })
	registerChannelRoutes(g)
	registerQuotaRoutes(g, svc)
	return r, svc, ch
}
func quotaRequest(t *testing.T, r http.Handler, method, path string, data any, status int) *httptest.ResponseRecorder {
	t.Helper()
	bytes, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	w := channelRequest(r, method, path, string(bytes))
	if w.Code != status {
		t.Fatalf("%s %s: %d want %d %s", method, path, w.Code, status, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "quota-private-token") || strings.Contains(w.Body.String(), "channel-private-key") {
		t.Fatal("secret in API response")
	}
	return w
}
func decodeQuotaSource(t *testing.T, w *httptest.ResponseRecorder) quota.SourceView {
	t.Helper()
	var src quota.SourceView
	if err := json.Unmarshal(w.Body.Bytes(), &src); err != nil {
		t.Fatal(err)
	}
	return src
}

func quotaNewAPIBodies() map[string]string {
	reset := time.Now().Add(time.Hour).Unix()
	return map[string]string{
		"/prefix/api/user/self":          `{"success":true,"data":{"quota":0,"used_quota":6643841489}}`,
		"/prefix/api/subscription/self":  fmt.Sprintf(`{"success":true,"data":{"billing_preference":"subscription_first","subscriptions":[{"subscription":{"id":11,"plan_id":3,"amount_total":100000000,"amount_used":0,"status":"active","start_time":1,"end_time":4102444800,"next_reset_time":%d}},{"subscription":{"id":12,"plan_id":3,"amount_total":50000000,"amount_used":49999999,"status":"active","start_time":1,"end_time":4102444800,"next_reset_time":%d}}]}}`, reset, reset),
		"/prefix/api/subscription/plans": `{"success":true,"data":[{"plan":{"id":3,"title":"月度套餐"}}]}`,
	}
}

func assertQuotaNewAPIAccount(t *testing.T, snapshot *quota.Snapshot) {
	t.Helper()
	if snapshot == nil || snapshot.NewAPI == nil || len(snapshot.Items) != 3 {
		t.Fatalf("missing wallet or independent subscriptions: %+v", snapshot)
	}
	items := map[string]quota.Item{}
	for _, item := range snapshot.Items {
		items[item.ID] = item
	}
	for id, remaining := range map[string]string{"account_quota": "0", "subscription:11": "100000000", "subscription:12": "1"} {
		item, ok := items[id]
		if !ok || item.Remaining == nil || *item.Remaining != remaining || item.Unit.Code != "QUOTA" {
			t.Fatalf("%s: %+v, want remaining %s QUOTA", id, item, remaining)
		}
		if id != "account_quota" && (item.PlanID != "3" || item.Label != "月度套餐") {
			t.Fatalf("same-plan subscription lost its identity or title: %+v", item)
		}
	}
	// Assert the public JSON contract used by the channel card as well as the detail items.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		NewAPI struct {
			BillingPreference string `json:"billing_preference"`
			Summary           *struct {
				Count          int        `json:"count"`
				UnlimitedCount int        `json:"unlimited_count"`
				Remaining      string     `json:"remaining"`
				Unit           quota.Unit `json:"unit"`
				ExpiresAt      *time.Time `json:"expires_at"`
				ResetsAt       *time.Time `json:"resets_at"`
			} `json:"subscription_summary"`
		} `json:"newapi"`
	}
	if err = json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	summary := public.NewAPI.Summary
	if public.NewAPI.BillingPreference != "subscription_first" || summary == nil || summary.Count != 2 || summary.UnlimitedCount != 0 || summary.Remaining != "100000001" || summary.Unit.Code != "QUOTA" || summary.ExpiresAt == nil || summary.ResetsAt == nil {
		t.Fatalf("incorrect subscription-only aggregate: %s", encoded)
	}
}

func TestQuotaAPIPreviewSaveRefreshAndSecrets(t *testing.T) {
	calls := 0
	bodies := quotaNewAPIBodies()
	client := quotaHTTPFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, ok := bodies[r.URL.Path]
		if !ok || r.URL.Scheme != "https" || r.URL.Host != "station.example" || r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer quota-private-token" || r.Header.Get("New-Api-User") != "42" {
			t.Errorf("bad query target/auth: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	r, _, ch := quotaFixture(t, client)
	token := "quota-private-token"
	in := quota.SourceInput{Name: "站点账户", AdapterID: "newapi.account", QueryBaseURL: "https://station.example/prefix", CredentialMode: "dedicated", Credentials: &quota.CredentialPatch{Token: &token}, Options: quota.QueryOptions{UserID: "42"}}
	w := quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201)
	src := decodeQuotaSource(t, w)
	if src.Enabled || calls != 0 || !src.HasToken {
		t.Fatalf("create must be passive: %+v calls=%d", src, calls)
	}
	// 一个渠道只允许一个查询来源：再次创建必须冲突，不能静默追加。
	quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 409)
	preview := quota.PreviewInput{SourceInput: in, ChannelID: ch.ID, SourceID: src.ID}
	preview.Credentials = nil
	w = quotaRequest(t, r, "POST", "/api/quota-sources/preview", preview, 200)
	var previewView quota.View
	if err := json.Unmarshal(w.Body.Bytes(), &previewView); err != nil {
		t.Fatal(err)
	}
	if previewView.Status != "fresh" || calls != 3 {
		t.Fatalf("raw preview: %s", w.Body.String())
	}
	assertQuotaNewAPIAccount(t, previewView.Snapshot)
	var stateCount int64
	shared.DB.Model(&quota.State{}).Count(&stateCount)
	if stateCount != 0 {
		t.Fatal("preview persisted state")
	}
	quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 400)
	in.Enabled = true
	in.Credentials = nil
	in.ExpectedRevision = src.ConfigRevision
	src = decodeQuotaSource(t, quotaRequest(t, r, "PUT", fmt.Sprintf("/api/quota-sources/%d", src.ID), in, 200))
	quotaRequest(t, r, "PUT", fmt.Sprintf("/api/quota-sources/%d", src.ID), in, 409)
	if calls != 3 {
		t.Fatal("saving configuration sent an upstream query")
	}
	src = decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 200))
	if src.State.Snapshot == nil || src.State.Status != "fresh" || calls != 6 {
		t.Fatalf("refresh: %+v calls=%d", src.State, calls)
	}
	assertQuotaNewAPIAccount(t, src.State.Snapshot)
	quotaRequest(t, r, "GET", fmt.Sprintf("/api/quota/summaries?channel_ids=%d", ch.ID), nil, 200)
	quotaRequest(t, r, "GET", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), nil, 200)
	if calls != 6 {
		t.Fatal("cache read sent network request")
	}
	quotaRequest(t, r, "DELETE", fmt.Sprintf("/api/quota-sources/%d", src.ID), nil, 200)
	shared.DB.Model(&quota.State{}).Count(&stateCount)
	if stateCount != 0 {
		t.Fatal("source delete orphaned state")
	}
}

func TestQuotaAPINewAPIPartialRateLimitPreservesDataAndCooldown(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limited   map[string]string
		wait      time.Duration
		itemCount int
		calls     int
	}{
		{"wallet", map[string]string{"/prefix/api/user/self": "120"}, 120 * time.Second, 2, 3},
		{"subscriptions", map[string]string{"/prefix/api/subscription/self": "120"}, 120 * time.Second, 1, 2},
		{"plans", map[string]string{"/prefix/api/subscription/plans": "120"}, 120 * time.Second, 3, 3},
		{"longest wait", map[string]string{"/prefix/api/user/self": "120", "/prefix/api/subscription/plans": "240"}, 240 * time.Second, 2, 3},
		{"default wait", map[string]string{"/prefix/api/subscription/self": ""}, time.Minute, 1, 2},
		{"bounded wait", map[string]string{"/prefix/api/subscription/self": "7200"}, time.Hour, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			limited := true
			bodies := quotaNewAPIBodies()
			client := quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				body, ok := bodies[req.URL.Path]
				if !ok {
					t.Fatalf("unexpected upstream path %s", req.URL.Path)
				}
				if req.Header.Get("Authorization") != "Bearer quota-private-token" || req.Header.Get("New-Api-User") != "42" || req.Method != http.MethodGet {
					t.Fatal("wrong query credentials or method")
				}
				if retry, hit := tc.limited[req.URL.Path]; limited && hit {
					return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {retry}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			r, _, ch := quotaFixture(t, client)
			token := "quota-private-token"
			in := quota.SourceInput{Name: "站点账户", AdapterID: "newapi.account", Enabled: true, AutoRefreshMinutes: 5, QueryBaseURL: "https://station.example/prefix", CredentialMode: "dedicated", Credentials: &quota.CredentialPatch{Token: &token}, Options: quota.QueryOptions{UserID: "42"}}
			src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
			before := time.Now()
			refreshPath := fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID)
			src = decodeQuotaSource(t, quotaRequest(t, r, "POST", refreshPath, nil, 200))
			after := time.Now()
			if calls != tc.calls || src.State.Snapshot == nil || src.State.Failure != nil || src.State.NextAllowedAt == nil || len(src.State.Snapshot.Items) != tc.itemCount {
				t.Fatalf("partial read lost usable data or cooldown: %+v calls=%d", src.State, calls)
			}
			if src.State.NextAllowedAt.Before(before.Add(tc.wait)) || src.State.NextAllowedAt.After(after.Add(tc.wait)) {
				t.Fatalf("cooldown %s outside expected %s window", src.State.NextAllowedAt, tc.wait)
			}
			info := src.State.Snapshot.NewAPI
			if info == nil {
				t.Fatal("missing per-section query status")
			}
			parts := map[string]*quota.QueryPartStatus{"/prefix/api/user/self": &info.Wallet, "/prefix/api/subscription/self": &info.Subscriptions, "/prefix/api/subscription/plans": info.Plans}
			for path := range tc.limited {
				part := parts[path]
				if part == nil || part.Status != "error" || part.Failure == nil || part.Failure.Category != "rate_limited" {
					t.Fatalf("missing partial rate-limit status for %s: %+v", path, part)
				}
			}
			var stored quota.State
			if err := shared.DB.First(&stored, "source_id = ?", src.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.NextScheduledAt == nil || stored.NextScheduledAt.Before(*src.State.NextAllowedAt) {
				t.Fatal("automatic query scheduled before cooldown expires")
			}
			quotaRequest(t, r, "POST", refreshPath, nil, 429)
			w := quotaRequest(t, r, "GET", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), nil, 200)
			var list []quota.SourceView
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].State.Snapshot == nil || list[0].State.NextAllowedAt == nil || len(list[0].State.Snapshot.Items) != tc.itemCount {
				t.Fatalf("cache did not preserve partial data and cooldown: %s", w.Body.String())
			}
			quotaRequest(t, r, "GET", fmt.Sprintf("/api/quota/summaries?channel_ids=%d", ch.ID), nil, 200)
			if calls != tc.calls {
				t.Fatal("cooldown retry or cached read sent another upstream request")
			}
			// Advance only the persisted cooldown, without waiting or rewriting a real source.
			if err := shared.DB.Model(&quota.State{}).Where("source_id = ?", src.ID).Update("next_allowed_at", time.Now().Add(-time.Second)).Error; err != nil {
				t.Fatal(err)
			}
			limited = false
			src = decodeQuotaSource(t, quotaRequest(t, r, "POST", refreshPath, nil, 200))
			if src.State.NextAllowedAt != nil || src.State.Failure != nil || calls != tc.calls+3 {
				t.Fatalf("successful retry did not clear cooldown: %+v calls=%d", src.State, calls)
			}
			assertQuotaNewAPIAccount(t, src.State.Snapshot)
		})
	}
}

func TestQuotaAPINewAPIBothPrimaryReadsFail(t *testing.T) {
	calls := 0
	r, _, ch := quotaFixture(t, quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		status := http.StatusUnauthorized
		if req.URL.Path == "/api/subscription/self" {
			status = http.StatusNotFound
		} else if req.URL.Path != "/api/user/self" {
			t.Fatalf("unexpected upstream request %s", req.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}))
	token := "quota-private-token"
	in := quota.SourceInput{Name: "站点账户", AdapterID: "newapi.account", Enabled: true, QueryBaseURL: "https://station.example", CredentialMode: "dedicated", Credentials: &quota.CredentialPatch{Token: &token}, Options: quota.QueryOptions{UserID: "42"}}
	src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
	src = decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 200))
	if calls != 2 || src.State.Snapshot != nil || src.State.LastSuccessAt != nil || src.State.Failure == nil || src.State.Failure.Category != "authentication" || src.State.Failure.UpstreamStatus != 401 {
		t.Fatalf("complete failure was incorrectly published as partial success: %+v calls=%d", src.State, calls)
	}
}

func TestQuotaChannelKeyInvalidationAndRollback(t *testing.T) {
	client := quotaHTTPFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.34"}]}`))}, nil
	})
	r, _, ch := quotaFixture(t, client)
	in := quota.SourceInput{Name: "余额", AdapterID: "deepseek.balance", CredentialMode: "inherit_channel_key", Enabled: true}
	src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
	src = decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 200))
	if src.State.Snapshot == nil {
		t.Fatal("missing initial snapshot")
	}
	// 模型导入失败时，Key 与来源版本变更必须同时回滚。
	quotaRequest(t, r, "PUT", fmt.Sprintf("/api/channels/%d", ch.ID), map[string]any{"key": "different-key", "models": []string{""}}, 400)
	var saved quota.Source
	shared.DB.First(&saved, src.ID)
	if saved.ConfigRevision != src.ConfigRevision {
		t.Fatal("source revision escaped rollback")
	}
	quotaRequest(t, r, "PUT", fmt.Sprintf("/api/channels/%d", ch.ID), map[string]any{"name": "renamed"}, 200)
	shared.DB.First(&saved, src.ID)
	if saved.ConfigRevision != src.ConfigRevision {
		t.Fatal("name change invalidated quota")
	}
	quotaRequest(t, r, "PUT", fmt.Sprintf("/api/channels/%d", ch.ID), map[string]any{"key": "different-key"}, 200)
	shared.DB.First(&saved, src.ID)
	if saved.ConfigRevision <= src.ConfigRevision {
		t.Fatal("key change did not invalidate revision")
	}
	w := quotaRequest(t, r, "GET", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), nil, 200)
	var list []quota.SourceView
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].State.Snapshot != nil {
		t.Fatal("old identity snapshot still displayed")
	}
	quotaRequest(t, r, "DELETE", fmt.Sprintf("/api/channels/%d", ch.ID), nil, 200)
	var count int64
	shared.DB.Model(&quota.Source{}).Count(&count)
	if count != 0 {
		t.Fatal("channel deletion orphaned quota source")
	}
}

func TestQuotaPreviewRejectsCrossChannelSecretReuse(t *testing.T) {
	calls := 0
	r, _, ch := quotaFixture(t, quotaHTTPFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("must not send") }))
	token := "quota-private-token"
	in := quota.SourceInput{Name: "private", AdapterID: "newapi.account", CredentialMode: "dedicated", QueryBaseURL: "https://station.example", Options: quota.QueryOptions{UserID: "1"}, Credentials: &quota.CredentialPatch{Token: &token}}
	src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
	ch2, err := channel.Create(channel.CreateRequest{Name: "other", Type: protocol.ProviderOpenAI, BaseURL: "https://other.example/v1", Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	in.Credentials = nil
	quotaRequest(t, r, "POST", "/api/quota-sources/preview", quota.PreviewInput{SourceInput: in, ChannelID: ch2.ID, SourceID: src.ID}, 400)
	if calls != 0 {
		t.Fatal("cross channel query sent")
	}
	quotaRequest(t, r, "GET", "/api/quota/summaries?channel_ids=1,bad", nil, 400)
}

// 同一来源在查询中返回202，渠道事务提交后立即取消旧身份请求。
func TestQuotaBusyAndChannelCommitCancellation(t *testing.T) {
	for _, action := range []string{"key", "delete"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			r, svc, ch := quotaFixture(t, quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
				close(started)
				<-req.Context().Done()
				close(stopped)
				return nil, req.Context().Err()
			}))
			in := quota.SourceInput{Name: "余额", AdapterID: "deepseek.balance", CredentialMode: "inherit_channel_key", Enabled: true}
			src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
			done := make(chan struct{})
			go func() { defer close(done); _, _ = svc.Refresh(context.Background(), src.ID) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("query did not start")
			}
			w := quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 202)
			if w.Header().Get("Retry-After") != "2" {
				t.Fatal("missing polling advice")
			}
			w = quotaRequest(t, r, "POST", "/api/quota-sources/preview", quota.PreviewInput{SourceInput: in, ChannelID: ch.ID, SourceID: src.ID}, 202)
			if w.Header().Get("Retry-After") != "2" {
				t.Fatal("missing preview polling advice")
			}
			// 回滚事务不能取消正在进行的有效查询。
			quotaRequest(t, r, "PUT", fmt.Sprintf("/api/channels/%d", ch.ID), map[string]any{"key": "changed", "models": []string{""}}, 400)
			select {
			case <-stopped:
				t.Fatal("rollback canceled query")
			default:
			}
			if action == "key" {
				quotaRequest(t, r, "PUT", fmt.Sprintf("/api/channels/%d", ch.ID), map[string]any{"key": "changed"}, 200)
			} else {
				quotaRequest(t, r, "DELETE", fmt.Sprintf("/api/channels/%d", ch.ID), nil, 200)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("committed channel change did not cancel query")
			}
		})
	}
}
