package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/quota"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestQuotaNewAPIUnitDetectionDoesNotReadCredentialsOrSave(t *testing.T) {
	calls := 0
	r, _, _ := quotaFixture(t, quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != "https://newapi.example/subpath/api/status" || req.Header.Get("Authorization") != "" || req.Header.Get("New-Api-User") != "" {
			t.Fatal("detector sent credentials or changed prefix")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"quota_per_unit":500000,"quota_display_type":"CNY","usd_exchange_rate":7}}`))}, nil
	}))
	w := quotaRequest(t, r, "POST", "/api/quota/newapi/detect", map[string]string{"query_base_url": "https://newapi.example/subpath"}, 200)
	var result struct {
		Options quota.QueryOptions `json:"options"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Options.QuotaPerUnit != "500000/7" || result.Options.DisplayUnit != "CNY" || calls != 1 {
		t.Fatal(w.Body.String(), err)
	}
	for _, entity := range []any{&quota.Source{}, &quota.State{}} {
		var count int64
		if err := shared.DB.Model(entity).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("detector persisted account data", count, err)
		}
	}
	quotaRequest(t, r, "POST", "/api/quota/newapi/detect", map[string]string{"query_base_url": "https://user:secret@newapi.example"}, 400)
	if calls != 1 {
		t.Fatal("invalid base reached upstream")
	}
}

func TestQuotaNewAPIUnitDetectionInsufficientSettings(t *testing.T) {
	r, _, _ := quotaFixture(t, quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"email_verification":false}}`))}, nil
	}))
	w := quotaRequest(t, r, "POST", "/api/quota/newapi/detect", map[string]string{"query_base_url": "https://newapi.example"}, 422)
	if !strings.Contains(w.Body.String(), "手动配置") || strings.Contains(w.Body.String(), "options") {
		t.Fatal(w.Body.String())
	}
}
