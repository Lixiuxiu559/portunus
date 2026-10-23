package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// NewHTTPClient 按转发配置构建 http.Client：沿用默认 transport 的连接池，
// 仅覆盖连接超时与等响应头超时。不设总 Timeout，避免截断长流式响应；
// 流式靠 ResponseHeaderTimeout 兜底「首包前不回头」。导出供 main 装配与测试注入。
func NewHTTPClient(pc shared.ProxyConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: time.Duration(pc.ConnectTimeoutSeconds) * time.Second}).DialContext
	transport.ResponseHeaderTimeout = time.Duration(pc.FirstByteTimeoutSeconds) * time.Second
	return &http.Client{Transport: transport}
}

// doRequest 发送上游 POST 请求，返回响应（body 由调用方负责关闭与读取）。
// 端点与鉴权头由 protocol.Upstream 提供；HTTP 执行走注入的 Deps.Client。
func (s *relayServer) doRequest(ctx context.Context, url string, headers http.Header, body []byte, trace *relayAttemptTrace, reason string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = headers
	if err := requestControlError(ctx); err != nil {
		return nil, err
	}
	if control := controlFrom(ctx); control != nil {
		if err := control.waitForAttempt(ctx); err != nil {
			return nil, err
		}
		if err := control.reserve(); err != nil {
			return nil, err
		}
	}
	trace.begin(body, reason)
	resp, err := s.deps.Client.Do(req)
	if err != nil {
		if stopErr := requestControlError(ctx); stopErr != nil {
			return nil, stopErr
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() && strings.Contains(err.Error(), "timeout awaiting response headers") {
			return nil, &upstreamHeaderTimeoutError{seconds: s.deps.Cfg.FirstByteTimeoutSeconds}
		}
		return nil, err
	}
	return resp, nil
}

// chatBudgetRetry 生命周期为一次客户端请求的一个目标，避免外层普通重试反复切换字段。
type chatBudgetRetry struct{ body []byte }

// doRequestWithChatBudgetFallback 在响应尚未交付时处理一次预算字段不兼容。
// 未匹配的错误恢复 body 交给常规失败处理；200/流内错误不参与此兼容重试。
func (s *relayServer) doRequestWithChatBudgetFallback(ctx context.Context, url string, headers http.Header, body []byte, allow bool, state *chatBudgetRetry, trace *relayAttemptTrace) (*http.Response, error) {
	if state.body != nil {
		body = state.body
	}
	resp, err := s.doRequest(ctx, url, headers, body, trace, "")
	if err != nil || !allow || state.body != nil || (resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity) {
		return resp, err
	}
	errorBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(errorBody))
	if readErr != nil {
		if stopErr := requestControlError(ctx); stopErr != nil {
			return nil, stopErr
		}
		return resp, nil
	}
	retryBody, ok := protocol.ChatTokenBudgetFallback(body, resp.StatusCode, errorBody)
	if !ok {
		return resp, nil
	}
	s.finishAttempt(trace, resp.StatusCode, false, &upstreamStatusError{status: resp.StatusCode, body: errorBody})
	state.body = retryBody
	log.Printf("relay 预算字段兼容重试: request_id=%s status=%d max_tokens→max_completion_tokens", headers.Get("X-Request-Id"), resp.StatusCode)
	return s.doRequest(ctx, url, headers, retryBody, trace, "budget_compat")
}
