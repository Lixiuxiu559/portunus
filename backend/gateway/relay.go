package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
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

// upstreamStatusError 表示上游返回非 2xx，携带状态码与响应体供上层透出。
type upstreamStatusError struct {
	status int
	body   []byte
}

func (e *upstreamStatusError) Error() string { return "上游返回非 2xx 状态码" }

// isRetryable 判断某次失败是否值得对同一 / 下一上游重试：
// 5xx 与 429 重试、网络 / 超时错误重试，其余（4xx、协议转换、配置）不重试。
func isRetryable(err error) bool {
	var se *upstreamStatusError
	if errors.As(err, &se) {
		return se.status >= 500 || se.status == 429
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// handleRelay 返回一个 relay handler，客户端协议由 clientProto 固定。
// 按分组策略遍历目标（外层），每个目标失败后按配置先重试 N 次（内层），
// 耗尽才换下一个目标；熔断开路的渠道会被跳过。
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
		anyAttempted := false
		for _, t := range targets {
			if !breakerAllow(t.Channel.ID) {
				continue // 熔断开路，跳过该渠道
			}
			for attempt := 0; attempt <= proxyCfg.RetryCount; attempt++ {
				anyAttempted = true
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
				if !isRetryable(err) {
					break // 不可重试，放弃该目标
				}
			}
		}

		if !anyAttempted {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "所有渠道暂不可用"})
			return
		}

		status := http.StatusBadGateway
		var se *upstreamStatusError
		if errors.As(lastErr, &se) {
			status = se.status
			// 上游 JSON 错误体原样透传，非 JSON 才用通用错误包装
			if json.Valid(se.body) {
				c.Writer.Header().Set("Content-Type", "application/json")
				c.Writer.WriteHeader(status)
				c.Writer.Write(se.body)
				return
			}
		}
		log.Printf("relay 失败: model=%s err=%v", meta.Model, lastErr)
		c.JSON(status, gin.H{"error": "上游调用失败"})
	}
}

// relayToTarget 对单个 target 完成一次 relay。
// 非流式：返回转换后的响应体（由调用方写入，以支持 failover 缓冲）；
// 流式：直接写客户端；首包前失败仍返回错误触发重试/换家，首包后（已提交）失败不再报错。
func relayToTarget(c *gin.Context, clientProto protocol.Provider, stream bool, g *group.Group, t router.Target, originalBody []byte, apiKeyID int64) (out []byte, status int, err error) {
	start := time.Now()
	success := false
	var usage *protocol.Usage
	var first time.Time // 流式首包写出时刻，非流式 / 首包前失败为零值
	defer func() {
		firstTokenMs := int64(0)
		if !first.IsZero() {
			firstTokenMs = first.Sub(start).Milliseconds()
		}
		logCall(apiKeyID, g, t, status, success, usage, time.Since(start).Milliseconds(), firstTokenMs)
		if success {
			breakerRecord(t.Channel.ID, false)
		} else if err != nil && isRetryable(err) {
			breakerRecord(t.Channel.ID, true)
		}
	}()

	reqBody, err := rewriteModel(originalBody, t.Model.Name)
	if err != nil {
		return nil, 0, err
	}

	upBody, err := protocol.ConvertRequest(clientProto, t.Channel.Type, reqBody)
	if err != nil {
		return nil, 0, err
	}

	upstream, err := protocol.NewUpstream(t.Channel.Type, t.Channel.BaseURL, t.Channel.Key)
	if err != nil {
		return nil, 0, err
	}

	reqCtx := c.Request.Context()
	if !stream {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, time.Duration(proxyCfg.NonStreamTimeoutSeconds)*time.Second)
		defer cancel()
	}

	resp, err := doRequest(reqCtx, upstream.ChatURL(t.Model.Name, stream), upstream.ChatHeaders(), upBody)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	status = resp.StatusCode

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		log.Printf("上游返回 %d: %s", resp.StatusCode, string(errBody))
		return nil, status, &upstreamStatusError{status: status, body: errBody}
	}

	if stream {
		conv, convErr := protocol.NewStreamConverter(t.Channel.Type, clientProto)
		if convErr != nil {
			return nil, status, convErr
		}
		// 客户端是 Anthropic 时，message_start 需要 input_tokens 才能显示上下文占用；
		// 上游流式可能不返回 usage，先按客户端原始请求体估一个值兜底。
		if clientProto == protocol.ProviderAnthropic {
			if es, ok := conv.(protocol.EstimateSetter); ok {
				es.SetEstimateInputTokens(protocol.EstimateRequestTokens(clientProto, reqBody))
			}
		}
		var streamErr error
		first, streamErr = relayStream(c, clientProto, resp, conv)
		if streamErr != nil {
			var committed *streamCommittedError
			if errors.As(streamErr, &committed) {
				// 已提交，无法 failover；记为失败但不再报错。
				// 已交付的部分流量仍可能累积了 usage，一并落库，避免费用漏记。
				usage = conv.Usage()
				return nil, status, nil
			}
			// 首包前失败，可重试 / 换家
			return nil, status, streamErr
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
