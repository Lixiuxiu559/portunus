package gateway

import (
	"io"
	"net/http"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// TestRelayStreamLogUsageSameProtocolAnthropic 验证渠道类型与客户端协议相同
//（Anthropic 渠道 + /v1/messages 客户端）的直通流式时，上游 message_delta 里的
// usage 也能落库（非流式走 UsageFromResponse 有解，流式曾因裸 identityStream 丢失）。
func TestRelayStreamLogUsageSameProtocolAnthropic(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"upstream-model\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":100,\"cache_read_input_tokens\":60,\"cache_creation_input_tokens\":30}}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":50}}\n\n")
		fl.Flush()
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderAnthropic, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}

	var entry shared.Log
	if err := shared.LogDB.Order("id desc").First(&entry).Error; err != nil {
		t.Fatalf("查日志失败: %v", err)
	}
	if entry.InputToken != 10 { // 100 - 60(cached) - 30(cache_creation) = 10
		t.Errorf("input_token = %d, want 10", entry.InputToken)
	}
	if entry.OutputToken != 50 {
		t.Errorf("output_token = %d, want 50", entry.OutputToken)
	}
	if entry.CacheReadToken != 60 {
		t.Errorf("cache_read_token = %d, want 60", entry.CacheReadToken)
	}
	if entry.CacheWriteToken != 30 {
		t.Errorf("cache_write_token = %d, want 30", entry.CacheWriteToken)
	}
}
// TestRelayStreamLogUsageOpenAIUpstream 验证 Anthropic 客户端 + OpenAI 上游流式时，
// 上游返回的 usage chunk 能正确累积到调用日志：
// 输入 token = 非缓存输入（prompt - cached - cache_creation），缓存输入/输出单独落库。
func TestRelayStreamLogUsageOpenAIUpstream(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n")
		fl.Flush()
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150,\"prompt_tokens_details\":{\"cached_tokens\":60,\"cache_creation_tokens\":30}}}\n\n")
		fl.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	})

	r, key := setupGateway(t, protocol.ProviderOpenAI, upstream)
	w := doReq(t, r, "/v1/messages", `{"model":"my-model","max_tokens":100,"messages":[{"role":"user","content":"hello"}],"stream":true}`, key)
	if w.Code != 200 {
		t.Fatalf("状态码 = %d, body=%s", w.Code, w.Body.String())
	}

	var entry shared.Log
	if err := shared.LogDB.Order("id desc").First(&entry).Error; err != nil {
		t.Fatalf("查日志失败: %v", err)
	}
	if entry.InputToken != 10 { // 100 - 60(cached) - 30(cache_creation) = 10
		t.Errorf("input_token = %d, want 10", entry.InputToken)
	}
	if entry.OutputToken != 50 {
		t.Errorf("output_token = %d, want 50", entry.OutputToken)
	}
	if entry.CacheReadToken != 60 {
		t.Errorf("cache_read_token = %d, want 60", entry.CacheReadToken)
	}
	if entry.CacheWriteToken != 30 {
		t.Errorf("cache_write_token = %d, want 30", entry.CacheWriteToken)
	}
}