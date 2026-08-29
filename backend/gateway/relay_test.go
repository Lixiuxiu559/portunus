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
		&shared.APIKey{},
	); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := shared.InitLogDB(cfg); err != nil {
		t.Fatalf("初始化日志库失败: %v", err)
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

func TestRelayStreamAnthropicClientOpenAIUpstreamEstimate(t *testing.T) {
	var gotBody []byte
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		// OpenAI 流式：无 usage chunk（模拟上游没开 include_usage 或忽略）
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	// Anthropic 客户端请求，带上 message_start 所需的 model/max_tokens
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"stream":true}`, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}

	// 上游请求应带 stream_options.include_usage=true
	var upstreamReq map[string]any
	if err := json.Unmarshal(gotBody, &upstreamReq); err != nil {
		t.Fatalf("解析上游请求失败: %v", err)
	}
	so, ok := upstreamReq["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Errorf("上游请求应带 stream_options.include_usage=true: %s", gotBody)
	}

	// 响应里 message_start 的 usage.input_tokens 应有非零预估（hello ≈ 1 token）
	body := w.Body.String()
	if !strings.Contains(body, `"type":"message_start"`) {
		t.Fatalf("缺少 message_start 事件: %s", body)
	}
	if !strings.Contains(body, `"input_tokens":2`) {
		t.Errorf("message_start 的 input_tokens 应为非零预估值: %s", body)
	}
	if !strings.Contains(body, `"text_delta"`) || !strings.Contains(body, `"text":"hi"`) {
		t.Errorf("流式响应未包含 text_delta 内容: %s", body)
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

// TestRelayStreamAnthropicClientOpenAIToolUseToAnthropic 复现真实场景：Claude Code 客户端走
// /v1/messages（Anthropic 协议），上游为 OpenAI Chat Completions 流式返回工具调用。
// 断言 Anthropic 流完整：tool_use 块 start（开 id/name）、input_json_delta（参数）、
// content_block_stop、stop_reason=tool_use 四个信号缺一不可——任何缺失都会让客户端
// 渲染出 Tool use interrupted。
func TestRelayStreamAnthropicClientOpenAIToolUseToAnthropic(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		// 真实 OpenAI 工具调用流式：开场 chunk 带 id/name，参数增量分片，最后 finish_reason=tool_calls
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_abc\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"Bei\"}}]},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"jing\\\"}\"}}]},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	// Claude Code 的 Anthropic 流式请求：带 stream:true + get_weather 工具定义
	body := `{"model":"my-model","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"北京天气怎么样?"}],"tools":[{"name":"get_weather","description":"查询天气","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"tool_choice":{"type":"auto"}}`
	w := doReq(t, r, "/v1/messages", body, key)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	// 工具调用完整性的四个信号
	if !strings.Contains(got, `"type":"tool_use"`) {
		t.Errorf("缺少 tool_use 块 start（Claude Code 会渲染 Tool use interrupted）:\n%s", got)
	}
	if !strings.Contains(got, `"name":"get_weather"`) {
		t.Errorf("tool_use 块缺 name=get_weather:\n%s", got)
	}
	if !strings.Contains(got, `"partial_json"`) {
		t.Errorf("缺少参数增量 input_json_delta（工具参数为空）:\n%s", got)
	}
	if !strings.Contains(got, `"type":"content_block_stop"`) {
		t.Errorf("缺少 content_block_stop（块未正常收尾）:\n%s", got)
	}
	if !strings.Contains(got, `"stop_reason":"tool_use"`) {
		t.Errorf("缺少 stop_reason=tool_use（客户端看不到工具调用结束）:\n%s", got)
	}
	// 拼装的工具参数要完整：流式里参数是分片 partial_json 增量，客户端会累积拼接成完整 JSON。
	// 收集所有 content_block_delta.input_json_delta 增量，断言拼装结果 = {"city":"Beijing"}。
	var assembledArgs string
	for _, raw := range strings.Split(got, "\n\n") {
		if !strings.HasPrefix(raw, "event: content_block_delta\ndata: ") {
			continue
		}
		payload := strings.TrimPrefix(raw, "event: content_block_delta\ndata: ")
		var ev struct {
			Delta struct {
				Type        string `json:"type"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		if ev.Delta.Type == "input_json_delta" {
			assembledArgs += ev.Delta.PartialJSON
		}
	}
	if assembledArgs != `{"city":"Beijing"}` {
		t.Errorf("拼装的工具参数不完整: %q, want {\"city\":\"Beijing\"}", assembledArgs)
	}
}
