package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// setupGateway 初始化内存 SQLite、种子数据、mock 上游，返回路由与 apikey。
func setupGateway(t *testing.T, chType protocol.Provider, upstream http.Handler) (*gin.Engine, string) {
	t.Helper()
	closeDB := func() {
		if shared.DB != nil {
			if sqlDB, err := shared.DB.DB(); err == nil {
				sqlDB.Close()
			}
		}
	}
	closeDB() // 关闭上一个测试遗留的连接

	cfg := &shared.Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if _, err := shared.InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	t.Cleanup(closeDB) // 先于 TempDir 清理关闭 DB，避免目录删除失败
	if err := shared.AutoMigrate(
		&channel.Channel{}, &model.Model{}, &group.Group{}, &group.GroupItem{},
		&shared.APIKey{}, &shared.Log{},
	); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)

	ch := channel.Channel{Name: "mock", Type: chType, BaseURL: srv.URL, Key: "sk-test", Enabled: true}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	m := model.Model{ChannelID: ch.ID, Name: "upstream-model", Enabled: true}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	g := group.Group{Name: "my-model", Strategy: group.StrategyFailover}
	if err := shared.DB.Create(&g).Error; err != nil {
		t.Fatalf("建分组失败: %v", err)
	}
	if err := shared.DB.Create(&group.GroupItem{GroupID: g.ID, ModelID: m.ID, Priority: 0}).Error; err != nil {
		t.Fatalf("建分组项失败: %v", err)
	}

	k := shared.APIKey{Name: "test", Key: "test-key", Enabled: true}
	if err := shared.DB.Create(&k).Error; err != nil {
		t.Fatalf("建 APIKey 失败: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)
	return r, k.Key
}

func doReq(t *testing.T, r *gin.Engine, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRelayNonStreamOpenAIToOpenAI(t *testing.T) {
	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	var upstreamReq map[string]any
	if err := json.Unmarshal(gotBody, &upstreamReq); err != nil {
		t.Fatalf("解析上游请求失败: %v", err)
	}
	if upstreamReq["model"] != "upstream-model" {
		t.Errorf("上游收到 model = %v, want upstream-model", upstreamReq["model"])
	}
	if !strings.Contains(w.Body.String(), `"content":"hi"`) {
		t.Errorf("响应内容不匹配: %s", w.Body.String())
	}
}

func TestRelayNonStreamOpenAIToAnthropic(t *testing.T) {
	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"hello from anthropic"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`))
	})

	r, key := setupGateway(t, protocol.ProviderAnthropic, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}]}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	var upstreamReq map[string]any
	if err := json.Unmarshal(gotBody, &upstreamReq); err != nil {
		t.Fatalf("解析上游请求失败: %v", err)
	}
	if _, ok := upstreamReq["messages"]; !ok {
		t.Errorf("上游应收到 anthropic messages 格式: %s", gotBody)
	}
	if !strings.Contains(w.Body.String(), "hello from anthropic") {
		t.Errorf("响应内容不匹配: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"object":"chat.completion"`) {
		t.Errorf("应转回 openai 格式: %s", w.Body.String())
	}
}

func TestRelayStreamOpenAIToOpenAI(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"hi"`) {
		t.Errorf("流式响应未包含内容: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("流式响应缺少 [DONE]: %s", body)
	}
}

func TestRelayStreamAnthropicToOpenAI(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"upstream-model\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":10}}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderAnthropic, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"role":"assistant"`) {
		t.Errorf("缺少 role chunk: %s", body)
	}
	if !strings.Contains(body, `"content":"hi"`) {
		t.Errorf("缺少 content chunk: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Errorf("缺少 finish_reason: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("缺少 [DONE]: %s", body)
	}
}

func TestRelayGroupNotFound(t *testing.T) {
	r, key := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	w := doReq(t, r, "/v1/chat/completions", `{"model":"no-such-group","messages":[]}`, key)
	if w.Code != http.StatusNotFound {
		t.Errorf("状态码 = %d, want 404", w.Code)
	}
}

func TestRelayUnauthorized(t *testing.T) {
	r, _ := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model"}`, "wrong-key")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("状态码 = %d, want 401", w.Code)
	}
}

func TestListModels(t *testing.T) {
	r, key := setupGateway(t, protocol.ProviderOpenAI, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	// 再种一个空分组，应被 /v1/models 过滤
	if err := shared.DB.Create(&group.Group{Name: "empty-group", Strategy: group.StrategyManual}).Error; err != nil {
		t.Fatalf("建空分组失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("应只返回 1 个模型（空分组被过滤），实际 %d", len(resp.Data))
	}
	m := resp.Data[0]
	if m.ID != "my-model" || m.Object != "model" || m.OwnedBy != "portunus" {
		t.Errorf("模型条目不匹配: %+v", m)
	}
	if m.Created == 0 {
		t.Errorf("created 应为非零时间戳")
	}
}
