package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

const nativeSearchRequest = `{"model":"my-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`

func TestRelayConversionDiagnosticsDoNotRejectOrSkipTarget(t *testing.T) {
	var calls atomic.Int32
	var received []byte
	recorder := &attemptRecorder{}
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { d.Cfg.MaxHTTPAttempts = 1; d.AttemptWrite = recorder.write }, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"c1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	// A compatible backup must not replace the ordinary first target merely because
	// the first target's compatibility report detects a hosted-tool downgrade.
	var ch channel.Channel
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatal(err)
	}
	addConversionTarget(t, protocol.ProviderAnthropic, ch.BaseURL)
	w := doReq(t, r, "/v1/messages", nativeSearchRequest, key)
	if w.Code != 200 || calls.Load() != 1 || len(recorder.entries()) != 1 {
		t.Fatalf("diagnostic rejected normal call: %d %d %s", w.Code, calls.Load(), w.Body.String())
	}
	if strings.Contains(string(received), "web_search") {
		t.Fatalf("existing tool downgrade changed: %s", received)
	}
	entry := logs.last()
	if entry == nil || !entry.Success || !entry.ConversionChecked || entry.ConversionScope != "request_native_v1" || entry.ConversionCompatible || entry.ConversionIssueCount == 0 || entry.ConversionPolicy != "" || entry.HostedToolsStripped != 1 {
		t.Fatalf("diagnostic summary lost: %+v", entry)
	}
	rows := recorder.entries()
	if rows[0].ChannelID != ch.ID {
		t.Fatalf("diagnostics unexpectedly changed target: %+v", rows)
	}
	serialized, _ := json.Marshal(entry)
	if strings.Contains(string(serialized), "conversion_policy") {
		t.Fatalf("retired policy exposed: %s", serialized)
	}
}

func TestRelayUnverifiedNativeContinuationIsDiagnosedAndPreserved(t *testing.T) {
	var calls atomic.Int32
	var received []byte
	r, key, logs := setupGatewayDeps(t, nil, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		received, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"id":"r1","object":"response","status":"completed","model":"upstream","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`)
	}))
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hi","previous_response_id":"secret-response-reference"}`, key)
	if w.Code != 200 || calls.Load() != 1 || !strings.Contains(string(received), `"previous_response_id":"secret-response-reference"`) {
		t.Fatalf("native continuation rejected or changed: %d calls=%d request=%s", w.Code, calls.Load(), received)
	}
	entry := logs.last()
	if entry == nil || !entry.Success || !entry.ConversionChecked || entry.ConversionCompatible || entry.ConversionIssueCount == 0 {
		t.Fatalf("unverified origin diagnostic lost: %+v", entry)
	}
	data, _ := json.Marshal(entry)
	if strings.Contains(string(data), "secret-response-reference") {
		t.Fatal("diagnostic leaked native reference")
	}
}

func addConversionTarget(t *testing.T, provider protocol.Provider, baseURL string) {
	t.Helper()
	ch := channel.Channel{Name: "native-backup", Type: provider, BaseURL: baseURL, Key: "test"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "native-model"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatal(err)
	}
	var g group.Group
	if err := shared.DB.Where("name = ?", "my-model").First(&g).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: 1}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestRelayNativeContinuationStreamFailureDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	recorder := &attemptRecorder{}
	r, key, logs := setupGatewayDeps(t, func(d *Deps) { d.Cfg.RetryCount = 3; d.AttemptWrite = recorder.write }, protocol.ProviderOpenAIResponses, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		request, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(request), `"previous_response_id":"secret-response-reference"`) {
			t.Errorf("native reference lost: %s", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"uncommitted\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
	}))
	var ch channel.Channel
	if err := shared.DB.First(&ch).Error; err != nil {
		t.Fatal(err)
	}
	addConversionTarget(t, protocol.ProviderOpenAIResponses, ch.BaseURL)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","input":"hi","previous_response_id":"secret-response-reference","stream":true}`, key)
	if w.Code != 502 || calls.Load() != 1 || len(recorder.entries()) != 1 || strings.Contains(w.Body.String(), "uncommitted") {
		t.Fatalf("unsafe continuation replayed or prelude leaked: code=%d calls=%d attempts=%+v body=%s", w.Code, calls.Load(), recorder.entries(), w.Body.String())
	}
	entry := logs.last()
	if entry == nil || entry.Success || !entry.ConversionChecked || entry.ConversionCompatible || entry.StreamOutcome != "truncated" || entry.StreamDeliveryStarted {
		t.Fatalf("unsafe stream diagnostics lost: %+v", entry)
	}
	data, _ := json.Marshal(entry)
	if strings.Contains(string(data), "secret-response-reference") {
		t.Fatal("diagnostic leaked reference")
	}
}
