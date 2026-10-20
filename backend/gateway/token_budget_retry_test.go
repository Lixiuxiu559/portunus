package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

const budgetUnsupportedLegacy = `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`
const budgetUnsupportedModern = `{"error":{"message":"Unsupported parameter: 'max_completion_tokens'. Use 'max_tokens' instead.","type":"invalid_request_error","param":"max_completion_tokens","code":"unsupported_parameter"}}`
const budgetChatSuccess = `{"id":"chat-budget","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

type budgetAttempt struct {
	body      map[string]any
	requestID string
}

func captureBudgetAttempt(t *testing.T, r *http.Request) budgetAttempt {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("读取上游请求: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Errorf("解析上游请求: %v", err)
	}
	return budgetAttempt{body: body, requestID: r.Header.Get("X-Request-Id")}
}

func assertBudgetAttempts(t *testing.T, attempts []budgetAttempt, budget float64, fields ...string) {
	t.Helper()
	if len(attempts) != len(fields) {
		t.Fatalf("上游调用 %d 次，期望 %d", len(attempts), len(fields))
	}
	var baseline map[string]any
	for i, a := range attempts {
		if a.body[fields[i]] != budget {
			t.Errorf("第%d次 %s=%v，期望 %v", i+1, fields[i], a.body[fields[i]], budget)
		}
		other := "max_completion_tokens"
		if fields[i] == other {
			other = "max_tokens"
		}
		if _, ok := a.body[other]; ok {
			t.Errorf("第%d次不应同时发送 %s: %#v", i+1, other, a.body)
		}
		if a.body["model"] != "upstream-model" {
			t.Errorf("应使用未知品牌上游模型: %#v", a.body)
		}
		if a.requestID == "" || a.requestID != attempts[0].requestID {
			t.Errorf("第%d次应保持同一非空 X-Request-Id", i+1)
		}
		withoutBudget := make(map[string]any, len(a.body))
		for k, v := range a.body {
			if k != "max_tokens" && k != "max_completion_tokens" {
				withoutBudget[k] = v
			}
		}
		if i == 0 {
			baseline = withoutBudget
		} else if !reflect.DeepEqual(baseline, withoutBudget) {
			t.Errorf("切换字段不得改变其他请求内容: %#v != %#v", baseline, withoutBudget)
		}
	}
}

func TestRelayTokenBudgetCodexCompatibilityRetry(t *testing.T) {
	var attempts []budgetAttempt
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, captureBudgetAttempt(t, r))
		w.Header().Set("Content-Type", "application/json")
		if len(attempts) == 1 {
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		}
		io.WriteString(w, budgetChatSuccess)
	})
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","max_output_tokens":8192,"reasoning":{"effort":"high"},"input":"hello"}`, key)
	assertBudgetAttempts(t, attempts, 8192, "max_tokens", "max_completion_tokens")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"ok"`) {
		t.Fatalf("客户端应收到成功的Responses响应: %d %s", w.Code, w.Body.String())
	}
}

func TestRelayTokenBudgetClaudeStreamCompatibilityRetry(t *testing.T) {
	var attempts []budgetAttempt
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, captureBudgetAttempt(t, r))
		if len(attempts) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"id\":\"chat-budget\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: {\"id\":\"chat-budget\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 0
	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":256,"stream":true,"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`, key)
	assertBudgetAttempts(t, attempts, 256, "max_tokens", "max_completion_tokens")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: message_stop") || !strings.Contains(w.Body.String(), `"text":"ok"`) {
		t.Fatalf("客户端应收到完整Anthropic SSE: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "unsupported_parameter") || strings.Contains(w.Body.String(), "event: error") {
		t.Errorf("首次400不得泄漏到流: %s", w.Body.String())
	}
}

func TestRelayTokenBudgetSameProtocolNoCompatibilityRetry(t *testing.T) {
	var attempts []budgetAttempt
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, captureBudgetAttempt(t, r))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, budgetUnsupportedLegacy)
	})
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/chat/completions", `{"model":"my-model","max_tokens":256,"custom_flag":"keep","messages":[{"role":"user","content":"hello"}]}`, key)
	assertBudgetAttempts(t, attempts, 256, "max_tokens")
	if attempts[0].body["custom_flag"] != "keep" {
		t.Error("同协议未知参数应原样透传")
	}
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Unsupported parameter:") {
		t.Fatalf("同协议应保留400: %d %s", w.Code, w.Body.String())
	}
}

func TestRelayTokenBudgetCompatibilityDoesNotPingPong(t *testing.T) {
	var attempts []budgetAttempt
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, captureBudgetAttempt(t, r))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		if len(attempts) == 1 {
			io.WriteString(w, budgetUnsupportedLegacy)
		} else {
			io.WriteString(w, budgetUnsupportedModern)
		}
	})
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 3
	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","max_output_tokens":256,"input":"hello"}`, key)
	assertBudgetAttempts(t, attempts, 256, "max_tokens", "max_completion_tokens")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Unsupported parameter: 'max_completion_tokens'") {
		t.Fatalf("应返回第二次错误: %d %s", w.Code, w.Body.String())
	}
}

func TestRelayTokenBudgetAbsentDoesNotInject(t *testing.T) {
	for _, request := range []struct{ path, body string }{
		{"/v1/responses", `{"model":"my-model","input":"hello"}`},
	} {
		t.Run(request.path, func(t *testing.T) {
			var attempts []budgetAttempt
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts = append(attempts, captureBudgetAttempt(t, r))
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, budgetChatSuccess)
			})
			r, key, _ := setupGateway(t, protocol.ProviderOpenAI, upstream)
			w := doReq(t, r, request.path, request.body, key)
			if w.Code != 200 || len(attempts) != 1 {
				t.Fatalf("请求失败: %d %s; calls=%d", w.Code, w.Body.String(), len(attempts))
			}
			for _, f := range []string{"max_tokens", "max_completion_tokens"} {
				if _, ok := attempts[0].body[f]; ok {
					t.Errorf("未指定预算不应注入 %s: %#v", f, attempts[0].body)
				}
			}
		})
	}
}

func TestRelayTokenBudgetOrdinaryRetryKeepsCompatibleField(t *testing.T) {
	var attempts []budgetAttempt
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, captureBudgetAttempt(t, r))
		w.Header().Set("Content-Type", "application/json")
		switch len(attempts) {
		case 1:
			w.WriteHeader(400)
			io.WriteString(w, budgetUnsupportedLegacy)
		case 2:
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"temporary overload"}}`)
		default:
			io.WriteString(w, budgetChatSuccess)
		}
	})
	pc := shared.DefaultProxyConfig()
	pc.RetryCount = 1
	r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/responses", `{"model":"my-model","max_output_tokens":256,"input":"hello"}`, key)
	assertBudgetAttempts(t, attempts, 256, "max_tokens", "max_completion_tokens", "max_completion_tokens")
	if w.Code != 200 {
		t.Fatalf("普通重试应继续使用协商后的字段: %d %s", w.Code, w.Body.String())
	}
}

func TestRelayTokenBudgetDoesNotRetryOtherErrors(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body, message string
	}{
		{"数值错误优先于不支持措辞", 400, `{"error":{"param":"max_tokens","code":"invalid_value","message":"max_tokens is not supported for value 8192; maximum is 4096"}}`, `maximum is 4096`},
		{"其他字段错误", 400, `{"error":{"param":"temperature","code":"unsupported_parameter","message":"Unsupported parameter: temperature"}}`, `Unsupported parameter: temperature`},
		{"鉴权错误", 401, budgetUnsupportedLegacy, `Unsupported parameter`},
		{"限流错误", 429, budgetUnsupportedLegacy, `Unsupported parameter`},
		{"服务错误", 500, budgetUnsupportedLegacy, `Unsupported parameter`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attempts []budgetAttempt
			upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts = append(attempts, captureBudgetAttempt(t, r))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			pc := shared.DefaultProxyConfig()
			pc.RetryCount = 0
			r, key, _ := setupGatewayDeps(t, withCfg(pc), protocol.ProviderOpenAI, upstream)
			w := doReq(t, r, "/v1/responses", `{"model":"my-model","max_output_tokens":8192,"input":"hello"}`, key)
			assertBudgetAttempts(t, attempts, 8192, "max_tokens")
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) {
				t.Fatalf("错误响应应原样保留其状态和原因: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
