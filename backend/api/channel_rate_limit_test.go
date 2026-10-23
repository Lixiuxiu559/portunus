package api

import (
	"encoding/json"
	"fmt"
	"github.com/Lixiuxiu559/portunus/backend/channel"
	"testing"
)

func TestChannelRateLimitScopeAPI(t *testing.T) {
	setupCascadeTest(t)
	r := channelTestRouter()
	w := channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"scope","key":"secret","rate_limit_scope":"channel"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var c channel.Response
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.RateLimitScope != "channel" {
		t.Fatalf("scope=%s", c.RateLimitScope)
	}
	path := fmt.Sprintf("/api/channels/%d", c.ID)
	for _, body := range []string{`{"rate_limit_scope":"all","name":"bad"}`, `{"rate_limit_scope":"MODEL"}`} {
		w = channelRequest(r, "PUT", path, body)
		if w.Code != 400 {
			t.Fatalf("invalid update: %d %s", w.Code, w.Body.String())
		}
		saved, err := channel.Get(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Name != "scope" || saved.RateLimitScope != "channel" {
			t.Fatalf("invalid update persisted: %+v", saved)
		}
	}
	w = channelRequest(r, "PUT", path, `{"name":"renamed"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.RateLimitScope != "channel" {
		t.Fatal("old client update lost scope")
	}
	w = channelRequest(r, "PUT", path, `{"rate_limit_scope":""}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.RateLimitScope != "model" {
		t.Fatal("empty should reset scope")
	}
	w = channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"invalid","key":"secret","rate_limit_scope":"all"}`)
	if w.Code != 400 {
		t.Fatalf("invalid create: %d %s", w.Code, w.Body.String())
	}
	w = channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"default","key":"secret"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.RateLimitScope != "model" {
		t.Fatal("omitted should default model")
	}
}
