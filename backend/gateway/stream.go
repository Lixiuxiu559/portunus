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

// streamCommittedError 表示流已提交（已写 200 头 + SSE 头），之后的失败无法 failover。
type streamCommittedError struct{ err error }

func (e *streamCommittedError) Error() string { return e.err.Error() }
func (e *streamCommittedError) Unwrap() error { return e.err }

// relayStream 把上游流式响应逐 chunk 转成客户端协议并 SSE 写回。
// 提交 200 头被推迟到成功转换出首个有效事件之后，因此「首包前」的失败仍可返回错误触发重试 / failover；
// 「首包后」的失败（静默断连 / 中途转换错误）返回 streamCommittedError，仅中断流、不再换家。
func relayStream(c *gin.Context, clientProto protocol.Provider, upstreamResp *http.Response, conv protocol.StreamConverter) error {
	scanner := bufio.NewScanner(upstreamResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 放大缓冲，避免长行截断

	committed := false
	for scanner.Scan() {
		payload, ok := parseSSEDataLine(scanner.Text())
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		chunks, err := conv.Convert([]byte(payload))
		if err != nil {
			return wrapStreamErr(committed, err)
		}
		if !committed {
			writeStreamHeaders(c)
			committed = true
		}
		for _, chunk := range chunks {
			writeSSE(c.Writer, clientProto, chunk)
		}
		c.Writer.Flush()
	}
	if err := scanner.Err(); err != nil {
		return wrapStreamErr(committed, err)
	}

	finals, err := conv.Finish()
	if err != nil {
		return wrapStreamErr(committed, err)
	}
	if !committed {
		// 上游 200 但未产出有效事件（如空流）：提交头后再收尾，交给客户端结束。
		writeStreamHeaders(c)
		committed = true
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

// wrapStreamErr 把错误包装为普通错误（首包前，可 failover）或 streamCommittedError（首包后，不可 failover）。
func wrapStreamErr(committed bool, err error) error {
	if committed {
		return &streamCommittedError{err: err}
	}
	return err
}

// parseSSEDataLine 解析一行 SSE：是 `data:` 行则返回去除前缀与空白的载荷（trim 后非空），
// 否则返回 ok=false。`[DONE]` 原样作为载荷返回。
func parseSSEDataLine(line string) (payload string, ok bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return "", false
	}
	return payload, true
}

// writeStreamHeaders 提交 SSE 响应头与 200 状态码。仅调用一次（首个有效事件后）。
func writeStreamHeaders(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)
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
