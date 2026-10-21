package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/quota"
)

func customQuotaInput() quota.SourceInput {
	return quota.SourceInput{
		Name: "私有站点余额", AdapterID: "custom.balance", QueryBaseURL: "https://station.example/prefix", CredentialMode: "dedicated",
		Options: quota.QueryOptions{Custom: &quota.CustomConfig{
			Request: quota.CustomRequest{Method: "POST", Path: "/balance?group=default", Headers: map[string]string{"X-Token": "{{token}}"}, Body: `{"account":"A"}`},
			Success: &quota.CustomCondition{Path: "/success", EqualsJSON: `true`},
			Items:   []quota.CustomItem{{ID: "cash", Label: "账户余额", Kind: "balance", Scope: "account", Unit: quota.Unit{Kind: "money", Code: "USD"}, RemainingPath: "/data/balance", Divisor: "100", OriginalUnit: &quota.Unit{Kind: "count", Code: "cents"}}},
		}},
	}
}

func TestCustomQuotaAPIConfigurationAndIdentity(t *testing.T) {
	calls := 0
	r, _, ch := quotaFixture(t, quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if req.URL.String() != "https://station.example/prefix/balance?group=default" || req.Method != "POST" || req.Header.Get("X-Token") != "quota-private-token" || string(body) != `{"account":"A"}` {
			t.Errorf("custom request did not match configured query (secrets omitted)")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"data":{"balance":"900719925474099312.34"}}`))}, nil
	}))
	in := customQuotaInput()
	token := "quota-private-token"
	in.Credentials = &quota.CredentialPatch{Token: &token}
	src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
	if calls != 0 || !src.HasToken || src.Options.Custom == nil {
		t.Fatal("create not passive or config lost")
	}
	// Typed JSON config should compare by value after each HTTP/DB decoding.
	in.Credentials = nil
	in.ExpectedRevision = src.ConfigRevision
	same := decodeQuotaSource(t, quotaRequest(t, r, "PUT", fmt.Sprintf("/api/quota-sources/%d", src.ID), in, 200))
	if same.ConfigRevision != src.ConfigRevision {
		t.Fatal("no-op config caused invalidation")
	}
	w := quotaRequest(t, r, "POST", "/api/quota-sources/preview", quota.PreviewInput{SourceInput: in, ChannelID: ch.ID, SourceID: src.ID}, 200)
	var preview quota.View
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || preview.Snapshot == nil || len(preview.Snapshot.Items) != 1 {
		t.Fatalf("preview failed: %s", w.Body.String())
	}
	item := preview.Snapshot.Items[0]
	if item.Remaining == nil || *item.Remaining != "9007199254740993.1234" || item.Original == nil || *item.Original.Remaining != "900719925474099312.34" {
		t.Fatal("decimal conversion or original amounts lost")
	}
	// Saved credentials cannot be borrowed for a new host, including a draft preview.
	in.QueryBaseURL = "https://other.example"
	quotaRequest(t, r, "POST", "/api/quota-sources/preview", quota.PreviewInput{SourceInput: in, ChannelID: ch.ID, SourceID: src.ID}, 400)
	if calls != 1 {
		t.Fatal("old credential sent to new target")
	}
	changed := decodeQuotaSource(t, quotaRequest(t, r, "PUT", fmt.Sprintf("/api/quota-sources/%d", src.ID), in, 200))
	if changed.HasToken || changed.ConfigRevision <= src.ConfigRevision {
		t.Fatal("target change retained credential")
	}
	in.Enabled = true
	in.ExpectedRevision = changed.ConfigRevision
	quotaRequest(t, r, "PUT", fmt.Sprintf("/api/quota-sources/%d", src.ID), in, 400)
}

func TestCustomQuotaAPINoAuthAndInvalidConfiguration(t *testing.T) {
	calls := 0
	r, _, ch := quotaFixture(t, quotaHTTPFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Authorization") != "" || req.Header.Get("X-Token") != "" {
			t.Error("unauthenticated query carried credentials")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":{"balance":0},"success":true}`))}, nil
	}))
	in := customQuotaInput()
	in.CredentialMode = "none"
	in.Options.Custom.Request.Headers = nil
	in.Enabled = true
	src := decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 201))
	in.ExpectedRevision = src.ConfigRevision
	in.Options.Custom.Request.Headers = map[string]string{}
	unchanged := decodeQuotaSource(t, quotaRequest(t, r, "PUT", fmt.Sprintf("/api/quota-sources/%d", src.ID), in, 200))
	if unchanged.ConfigRevision != src.ConfigRevision {
		t.Fatal("empty headers caused no-op invalidation")
	}
	src = decodeQuotaSource(t, quotaRequest(t, r, "POST", fmt.Sprintf("/api/quota-sources/%d/refresh", src.ID), nil, 200))
	if calls != 1 || src.HasToken || src.State.Snapshot == nil || *src.State.Snapshot.Items[0].Remaining != "0" {
		t.Fatal("public zero balance query failed")
	}
	quotaRequest(t, r, "GET", fmt.Sprintf("/api/quota/summaries?channel_ids=%d", ch.ID), nil, 200)
	if calls != 1 {
		t.Fatal("cache read sent query")
	}
	in.Options.Custom.Request.Headers = map[string]string{"Authorization": "Bearer quota-private-token"}
	quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 400)
	in = customQuotaInput()
	in.AdapterID = "deepseek.balance"
	in.QueryBaseURL = "https://api.deepseek.com"
	in.CredentialMode = "inherit_channel_key"
	quotaRequest(t, r, "POST", fmt.Sprintf("/api/channels/%d/quota-sources", ch.ID), in, 400)
	if calls != 1 {
		t.Fatal("invalid custom config sent request")
	}
}
