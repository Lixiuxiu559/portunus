package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestRelayNamedAPIKeyUsageAttribution(t *testing.T) {
	upstreamCalls := 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	})
	r, _, logs := setupGatewayDeps(t, func(d *Deps) {
		d.AuthIdentity = shared.AuthenticateAPIKeyIdentity
	}, protocol.ProviderOpenAI, upstream)
	if err := shared.AutoMigrate(&shared.APIKey{}); err != nil {
		t.Fatal(err)
	}
	claude, err := shared.CreateAPIKey("Claude Code")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := shared.CreateAPIKey("Codex")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`
	for _, key := range []*shared.APIKey{claude, codex} {
		w := doReq(t, r, "/v1/chat/completions", body, key.Key)
		if w.Code != 200 {
			t.Fatalf("relay status=%d body=%s", w.Code, w.Body.String())
		}
		entry := logs.last()
		if entry == nil || entry.APIKeyID != key.ID || entry.APIKeyName != key.Name || entry.ModelName != "upstream-model" || entry.InputToken != 10 || entry.OutputToken != 5 {
			t.Fatalf("模型和用量未归属于正确Key: %+v", entry)
		}
	}
	if err := shared.RevokeAPIKey(claude.ID); err != nil {
		t.Fatal(err)
	}
	if w := doReq(t, r, "/v1/chat/completions", body, claude.Key); w.Code != 401 {
		t.Fatal("停用 Key 仍可调用上游")
	}
	if upstreamCalls != 2 {
		t.Fatalf("停用请求不应产生上游调用: %d", upstreamCalls)
	}
	if w := doReq(t, r, "/v1/chat/completions", body, codex.Key); w.Code != 200 {
		t.Fatal("停用其他Key影响Codex")
	}
}

func TestRelayKeyNameSnapshotSurvivesInFlightChanges(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	})
	r, _, logs := setupGatewayDeps(t, func(d *Deps) { d.AuthIdentity = shared.AuthenticateAPIKeyIdentity }, protocol.ProviderOpenAI, upstream)
	if err := shared.AutoMigrate(&shared.APIKey{}); err != nil {
		t.Fatal(err)
	}
	key, err := shared.CreateAPIKey("调用时旧名称")
	if err != nil {
		t.Fatal(err)
	}
	for index, wantName := range []string{"调用时旧名称", "调用时新名称"} {
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			result <- doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`, key.Key)
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("上游未收到请求")
		}
		if index == 0 {
			_, err = shared.RenameAPIKey(key.ID, "调用时新名称")
		} else {
			err = shared.DeleteAPIKey(key.ID)
		}
		release <- struct{}{}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case w := <-result:
			if w.Code != 200 {
				t.Fatalf("已鉴权请求应完成: %d %s", w.Code, w.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("请求未完成")
		}
		if entry := logs.last(); entry == nil || entry.APIKeyName != wantName || entry.APIKeyID != key.ID {
			t.Fatalf("在途变更篡改名称快照: %+v", entry)
		}
	}
	if w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model"}`, key.Key); w.Code != 401 {
		t.Fatal("删除后新请求仍通过")
	}
}
