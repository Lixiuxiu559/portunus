package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
)

// relayMeta 是三种客户端协议请求体的最小公共字段。
type relayMeta struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// upstreamStatusError 表示上游返回非 2xx，携带状态码供上层透出。
type upstreamStatusError struct{ status int }

func (e *upstreamStatusError) Error() string { return "上游返回非 2xx 状态码" }

// handleRelay 返回一个 relay handler，客户端协议由 clientProto 固定。
func handleRelay(clientProto protocol.Provider) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
			return
		}

		var meta relayMeta
		if err := json.Unmarshal(body, &meta); err != nil || meta.Model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 model 字段"})
			return
		}

		g, err := group.GetByName(meta.Model)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "分组不存在: " + meta.Model})
			return
		}

		targets, err := router.Resolve(g)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		apiKeyID := c.GetInt64("api_key_id")

		var lastErr error
		for _, t := range targets {
			out, status, err := relayToTarget(c, clientProto, meta.Stream, g, t, body, apiKeyID)
			if err == nil {
				if !meta.Stream {
					c.Writer.Header().Set("Content-Type", "application/json")
					c.Writer.WriteHeader(status)
					c.Writer.Write(out)
				}
				return
			}
			lastErr = err
		}

		status := http.StatusBadGateway
		var se *upstreamStatusError
		if errors.As(lastErr, &se) {
			status = se.status
		}
		c.JSON(status, gin.H{"error": "上游调用失败"})
	}
}

// relayToTarget 对单个 target 完成一次 relay。
// 非流式：返回转换后的响应体（由调用方写入，以支持 failover 缓冲）；
// 流式：直接写客户端，一旦提交（写入 200 头）不再返回错误触发 failover。
func relayToTarget(c *gin.Context, clientProto protocol.Provider, stream bool, g *group.Group, t router.Target, originalBody []byte, apiKeyID int64) (out []byte, status int, err error) {
	start := time.Now()
	success := false
	var usage *protocol.Usage
	defer func() {
		logCall(apiKeyID, g, t, status, success, usage, time.Since(start).Milliseconds())
	}()

	reqBody, err := rewriteModel(originalBody, t.Model.Name)
	if err != nil {
		return nil, 0, err
	}

	upBody, err := protocol.ConvertRequest(clientProto, t.Channel.Type, reqBody)
	if err != nil {
		return nil, 0, err
	}

	resp, err := doRequest(buildURL(t.Channel, t.Model.Name, stream), buildHeaders(t.Channel), upBody)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return nil, status, &upstreamStatusError{status: status}
	}

	if stream {
		conv, convErr := protocol.NewStreamConverter(t.Channel.Type, clientProto)
		if convErr != nil {
			return nil, status, convErr
		}
		if streamErr := relayStream(c, clientProto, resp, conv); streamErr != nil {
			// 流已提交，无法 failover；记为失败但不再报错
			return nil, status, nil
		}
		usage = conv.Usage()
	} else {
		upRespBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, status, readErr
		}
		usage = protocol.UsageFromResponse(t.Channel.Type, upRespBody)
		out, err = protocol.ConvertResponse(t.Channel.Type, clientProto, upRespBody)
		if err != nil {
			return nil, status, err
		}
	}

	success = true
	return out, status, nil
}

// rewriteModel 把请求体顶层 model 改写为上游模型名。
func rewriteModel(body []byte, modelName string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["model"] = modelName
	return json.Marshal(m)
}
