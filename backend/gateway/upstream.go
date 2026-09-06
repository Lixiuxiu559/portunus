package gateway

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

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
func (s *relayServer) doRequest(ctx context.Context, url string, headers http.Header, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = headers
	resp, err := s.deps.Client.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() && strings.Contains(err.Error(), "timeout awaiting response headers") {
			return nil, &upstreamHeaderTimeoutError{seconds: s.deps.Cfg.FirstByteTimeoutSeconds}
		}
		return nil, err
	}
	return resp, nil
}
