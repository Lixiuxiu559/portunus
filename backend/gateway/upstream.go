package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// httpClient 持有连接池；连接 / 响应头超时由 Transport 提供。
// 不设总 Timeout，避免截断长流式响应；流式靠 ResponseHeaderTimeout 兜底「首包前不回头」。
var httpClient = newHTTPClient(shared.DefaultProxyConfig())

// newHTTPClient 按转发配置构建 http.Client：沿用默认 transport 的连接池，
// 仅覆盖连接超时与等响应头超时。
func newHTTPClient(pc shared.ProxyConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: time.Duration(pc.ConnectTimeoutSeconds) * time.Second}).DialContext
	transport.ResponseHeaderTimeout = time.Duration(pc.FirstByteTimeoutSeconds) * time.Second
	return &http.Client{Transport: transport}
}

// upstreamHeaderTimeoutError 上游在 FirstByteTimeoutSeconds 内未返回响应头。
// 实现 net.Error（Timeout / Temporary）以保持 isRetryable 的可重试分类不变，
// 只把 Transport 原生晦涩文案（net/http: timeout awaiting response headers）换成可读错误。
type upstreamHeaderTimeoutError struct {
	seconds int
}

func (e *upstreamHeaderTimeoutError) Error() string {
	return fmt.Sprintf("上游 %ds 未返回响应头", e.seconds)
}
func (*upstreamHeaderTimeoutError) Timeout() bool   { return true }
func (*upstreamHeaderTimeoutError) Temporary() bool { return true }

// doRequest 发送上游 POST 请求，返回响应（body 由调用方负责关闭与读取）。
// 端点与鉴权头由 protocol.Upstream 提供。
func doRequest(ctx context.Context, url string, headers http.Header, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = headers
	resp, err := httpClient.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() && strings.Contains(err.Error(), "timeout awaiting response headers") {
			return nil, &upstreamHeaderTimeoutError{seconds: proxyCfg.FirstByteTimeoutSeconds}
		}
		return nil, err
	}
	return resp, nil
}
