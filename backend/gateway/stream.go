package gateway

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// relayStream 把上游流式响应逐 chunk 转成客户端协议并 SSE 写回。
// 一旦开始写（提交 200 + SSE 头），后续失败仅中断流，不再返回错误触发 failover。
func relayStream(c *gin.Context, clientProto protocol.Provider, upstreamResp *http.Response, conv protocol.StreamConverter) error {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(upstreamResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 放大缓冲，避免长行截断

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		chunks, err := conv.Convert([]byte(data))
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			writeSSE(c.Writer, clientProto, chunk)
		}
		c.Writer.Flush()
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	finals, err := conv.Finish()
	if err != nil {
		return err
	}
	for _, chunk := range finals {
		writeSSE(c.Writer, clientProto, chunk)
	}
	// OpenAI / Responses 客户端补 [DONE]；Anthropic 客户端已在 message_stop 事件里结束
	if clientProto != protocol.ProviderAnthropic {
		c.Writer.WriteString("data: [DONE]\n\n")
	}
	c.Writer.Flush()
	return nil
}

// writeSSE 按客户端协议补 SSE 帧。
func writeSSE(w io.Writer, clientProto protocol.Provider, payload []byte) {
	if clientProto == protocol.ProviderAnthropic {
		var ev struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &ev) == nil && ev.Type != "" {
			io.WriteString(w, "event: "+ev.Type+"\n")
		}
	}
	io.WriteString(w, "data: "+string(payload)+"\n\n")
}
