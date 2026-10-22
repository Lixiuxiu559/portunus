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

func TestQuotaAPIPreviewSaveRefreshAndSecrets(t *testing.T) {
	calls := 0
	client := quotaHTTPFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://station.example/prefix/api/user/self" || r.Header.Get("Authorization") != "Bearer quota-private-token" || r.Header.Get("New-Api-User") != "42" {
			t.Errorf("bad query target/auth: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"group":"default","quota":1000000,"used_quota":500000}}`))}, nil
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
	if !strings.Contains(w.Body.String(), "1000000") || calls != 1 {
		t.Fatalf("raw preview: %s", w.Body.String())
	}
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
	src = decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 200))
	if src.State.Snapshot == nil || src.State.Status != "fresh" || calls != 2 {
		t.Fatalf("refresh: %+v calls=%d", src.State, calls)
	}
	quotaRequest(t, r, "GET", fmt.Sprintf("/api/quota/summaries?channel_ids=%d", ch.ID), nil, 200)
	quotaRequest(t, r, "GET", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), nil, 200)
	if calls != 2 {
		t.Fatal("cache read sent network request")
	}
	quotaRequest(t, r, "DELETE", fmt.Sprintf("/api/quota-sources/%d", src.ID), nil, 200)
	shared.DB.Model(&quota.State{}).Count(&stateCount)
	if stateCount != 0 {
		t.Fatal("source delete orphaned state")
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
