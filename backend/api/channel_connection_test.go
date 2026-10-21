package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func channelRequest(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func channelTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerChannelRoutes(r.Group("/api"))
	return r
}

func TestChannelSelectedModelsTransaction(t *testing.T) {
	setupCascadeTest(t)
	r := channelTestRouter()
	w := channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"selected","key":"test-secret","auto_sync":false,"models":[" m1 ","m1","m2"]}`)
	if w.Code != 201 || strings.Contains(w.Body.String(), "test-secret") {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var saved channel.Response
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AutoSync || saved.BaseURL != "https://api.openai.com/v1" || saved.Type != protocol.ProviderOpenAI {
		t.Fatalf("defaults/false: %+v", saved)
	}
	models, total, err := model.List(saved.ID, "", 1, 100)
	if err != nil || total != 2 {
		t.Fatalf("selected: %d %v", total, err)
	}
	id := models[0].ID
	w = channelRequest(r, "PUT", "/api/channels/"+strconv.FormatInt(saved.ID, 10), `{"name":"updated","models":["m1","m3"]}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	models, total, err = model.List(saved.ID, "", 1, 100)
	if err != nil || total != 3 || models[0].ID != id {
		t.Fatalf("import changed existing IDs: %+v %d %v", models, total, err)
	}
	// 模型校验失败必须同时回滚新渠道和渠道更新。
	w = channelRequest(r, "POST", "/api/channels", `{"provider_id":"openai","name":"rollback","key":"k","models":["ok",""]}`)
	if w.Code != 400 {
		t.Fatalf("invalid model status: %d %s", w.Code, w.Body.String())
	}
	var count int64
	shared.DB.Model(&channel.Channel{}).Where("name = ?", "rollback").Count(&count)
	if count != 0 {
		t.Fatal("partial channel survived")
	}
	w = channelRequest(r, "PUT", fmt.Sprintf("/api/channels/%d", saved.ID), `{"name":"must-rollback","models":[""]}`)
	if w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	ch, err := channel.Get(saved.ID)
	if err != nil || ch.Name != "updated" {
		t.Fatalf("update did not roll back: %+v %v", ch, err)
	}
}

func TestChannelDraftUsesSavedKeyWithoutWriting(t *testing.T) {
	setupCascadeTest(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/draft/v1/models" || r.Header.Get("Authorization") != "Bearer saved-secret" {
			t.Errorf("draft target/auth: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		io.WriteString(w, `{"data":[{"id":"from-draft"}]}`)
	}))
	defer srv.Close()
	ch, err := channel.Create(channel.CreateRequest{Name: "saved", Type: protocol.ProviderOpenAI, BaseURL: srv.URL + "/saved/v1", Key: "saved-secret"})
	if err != nil {
		t.Fatal(err)
	}
	r := channelTestRouter()
	body := fmt.Sprintf(`{"channel_id":%d,"base_url":%q}`, ch.ID, srv.URL+"/draft/v1")
	w := channelRequest(r, "POST", "/api/channels/preview-models", body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from-draft") || calls != 1 {
		t.Fatalf("preview: %d %s calls=%d", w.Code, w.Body.String(), calls)
	}
	stored, _ := channel.Get(ch.ID)
	if stored.BaseURL != ch.BaseURL || stored.Key != ch.Key {
		t.Fatal("draft changed saved connection")
	}
	w = channelRequest(r, "PUT", fmt.Sprintf("/api/channels/%d", ch.ID), `{"base_url":""}`)
	if w.Code != 400 {
		t.Fatal("explicit empty address must not switch to preset")
	}
	w = channelRequest(r, "POST", "/api/channels/resolve", fmt.Sprintf(`{"channel_id":%d,"base_url":""}`, ch.ID))
	if w.Code != 400 {
		t.Fatal("draft empty address must not switch to preset")
	}
	w = channelRequest(r, "POST", "/api/channels/resolve", body)
	if w.Code != 200 || strings.Contains(w.Body.String(), "saved-secret") || !strings.Contains(w.Body.String(), "/draft/v1/chat/completions") {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	w = channelRequest(r, "POST", "/api/channels/preview-models", fmt.Sprintf(`{"channel_id":%d}`, ch.ID+100))
	if w.Code != 404 {
		t.Fatalf("missing channel: %d", w.Code)
	}
	_, total, _ := model.List(ch.ID, "", 1, 100)
	if total != 0 {
		t.Fatal("preview persisted models")
	}
}

func TestDraftInheritsSavedCompatibilityFlag(t *testing.T) {
	setupCascadeTest(t)
	flag := false
	ch, err := channel.Create(channel.CreateRequest{Name: "compat-off", Type: protocol.ProviderOpenAI, BaseURL: "http://localhost/v1", Key: "secret", ThinkingCompat: &flag})
	if err != nil {
		t.Fatal(err)
	}
	draft := connectionDraft{ChannelID: ch.ID}
	_, cfg, err := draft.open(true)
	if err != nil || cfg.ThinkingCompat {
		t.Fatalf("saved false not inherited: %+v %v", cfg, err)
	}
	flag = true
	draft.ThinkingCompat = &flag
	_, cfg, err = draft.open(true)
	if err != nil || !cfg.ThinkingCompat {
		t.Fatalf("explicit override not applied: %+v %v", cfg, err)
	}
}

func TestChannelResolveAndProbeValidation(t *testing.T) {
	r := channelTestRouter()
	for _, body := range []string{
		`{"provider_id":"generic","base_url":"https://user:secret@example.org/v1"}`,
		`{"base_url":"https://example.org/v1?key=secret"}`,
		`{"base_url":"https://example.org/v1/chat/completions"}`,
		`{"base_url":"https://example.org/v1","adapter_id":"unknown"}`,
	} {
		w := channelRequest(r, "POST", "/api/channels/resolve", body)
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("invalid resolve: %d %s", w.Code, w.Body.String())
		}
	}
	for _, budget := range []int{0, -1, 129} {
		w := channelRequest(r, "POST", "/api/channels/test", fmt.Sprintf(`{"model":"m","max_output_tokens":%d}`, budget))
		if w.Code != 400 {
			t.Fatalf("accepted budget %d: %s", budget, w.Body.String())
		}
	}
	w := channelRequest(r, "GET", "/api/upstream-presets", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "通用兼容服务") || !strings.Contains(w.Body.String(), "source_url") {
		t.Fatalf("presets: %d %s", w.Code, w.Body.String())
	}
}

func TestChannelProbeRouteUsesDraftAndExplicitBudget(t *testing.T) {
	setupCascadeTest(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/draft/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer route-secret" {
			t.Errorf("unexpected probe connection: %s", r.URL.Path)
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			Stream    bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "test-model" || body.MaxTokens != 16 {
			t.Errorf("probe payload: %+v", body)
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
		}
	}))
	defer srv.Close()
	ch, err := channel.Create(channel.CreateRequest{Name: "probe-saved", Type: protocol.ProviderOpenAI, BaseURL: srv.URL + "/saved/v1", Key: "route-secret"})
	if err != nil {
		t.Fatal(err)
	}
	r := channelTestRouter()
	for _, stream := range []bool{false, true} {
		body := fmt.Sprintf(`{"channel_id":%d,"base_url":%q,"model":"test-model","stream":%t,"max_output_tokens":16}`, ch.ID, srv.URL+"/draft/v1", stream)
		w := channelRequest(r, "POST", "/api/channels/test", body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) || strings.Contains(w.Body.String(), "route-secret") {
			t.Fatalf("probe route: %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("probe unexpectedly retried: %d", calls)
	}
	stored, _ := channel.Get(ch.ID)
	if stored.BaseURL != ch.BaseURL {
		t.Fatal("probe changed saved address")
	}
	_, total, _ := model.List(ch.ID, "", 1, 100)
	if total != 0 {
		t.Fatal("probe persisted model")
	}
}
