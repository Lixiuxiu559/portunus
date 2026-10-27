package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

func NewHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func Execute(ctx context.Context, doer HTTPDoer, adapter Adapter, source ResolvedSource) (*Snapshot, *Failure) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if adapter == nil {
		return nil, &Failure{Code: "unsupported_adapter", Category: "configuration", Message: "不支持此额度查询适配器"}
	}
	if a, ok := adapter.(newAPIAdapter); ok {
		return executeNewAPI(ctx, doer, a, source)
	}
	spec, err := adapter.BuildRequest(source)
	if err != nil {
		return nil, configurationFailure()
	}
	budget := maxResponseBytes
	body, headers, status, failure := executeRequest(ctx, doer, adapter.Describe(), spec, &budget)
	if failure != nil {
		return nil, failure
	}
	snapshot, failure := adapter.Parse(source, headers, body)
	if failure != nil {
		failure.UpstreamStatus = status
		return nil, failure
	}
	if !validateSnapshot(snapshot) {
		failure = invalidResponse()
		failure.UpstreamStatus = status
		return nil, failure
	}
	return snapshot, nil
}
func configurationFailure() *Failure {
	return &Failure{Code: "invalid_configuration", Category: "configuration", Message: "查询地址、凭据或参数无效"}
}

// All requests, including multipart account reads, share the same bounded transport.
func executeRequest(ctx context.Context, doer HTTPDoer, descriptor Descriptor, spec RequestSpec, budget *int) ([]byte, http.Header, int, *Failure) {
	if ctx.Err() != nil {
		return nil, nil, 0, requestFailure(ctx, ctx.Err(), "network_error", "额度查询连接失败")
	}
	if *budget <= 0 {
		return nil, nil, 0, &Failure{Code: "response_too_large", Category: "invalid_response", Message: "上游额度响应超过大小限制"}
	}
	if !validRequestMethod(descriptor, spec) {
		return nil, nil, 0, configurationFailure()
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, 15*time.Second)
	defer requestCancel()
	req, err := http.NewRequestWithContext(requestCtx, spec.Method, spec.URL, bytes.NewReader(spec.Body))
	if err != nil {
		return nil, nil, 0, &Failure{Code: "invalid_configuration", Category: "configuration", Message: "无法构造额度查询请求"}
	}
	req.Header = spec.Headers.Clone()
	if doer == nil {
		doer = NewHTTPClient()
	}
	// Keep injection useful for local transports while never permitting client redirects.
	if client, ok := doer.(*http.Client); ok {
		copyClient := *client
		copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if copyClient.Timeout <= 0 || copyClient.Timeout > 15*time.Second {
			copyClient.Timeout = 15 * time.Second
		}
		doer = &copyClient
	}
	resp, err := doer.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return nil, nil, 0, requestFailure(requestCtx, err, "network_error", "额度查询连接失败")
	}
	if resp == nil {
		return nil, nil, 0, &Failure{Code: "invalid_response", Category: "invalid_response", Message: "上游没有返回有效响应"}
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, resp.StatusCode, httpFailure(resp.StatusCode, resp.Header)
	}
	if resp.Body == nil {
		return nil, nil, resp.StatusCode, invalidResponse()
	}
	if resp.ContentLength > int64(*budget) {
		return nil, nil, 0, &Failure{Code: "response_too_large", Category: "invalid_response", Message: "上游额度响应超过大小限制", UpstreamStatus: resp.StatusCode}
	}
	limit := *budget
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	*budget = max(0, *budget-len(body))
	if err != nil {
		f := requestFailure(requestCtx, err, "response_read_error", "读取上游额度响应失败")
		f.UpstreamStatus = resp.StatusCode
		return nil, nil, resp.StatusCode, f
	}
	if len(body) > limit {
		return nil, nil, 0, &Failure{Code: "response_too_large", Category: "invalid_response", Message: "上游额度响应超过大小限制", UpstreamStatus: resp.StatusCode}
	}
	if requestCtx.Err() != nil {
		return nil, nil, 0, requestFailure(requestCtx, requestCtx.Err(), "network_error", "额度查询连接失败")
	}
	return body, resp.Header, resp.StatusCode, nil
}

func requestFailure(ctx context.Context, err error, code, message string) *Failure {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return &Failure{Code: "cancelled", Category: "cancelled", Message: "额度查询已取消"}
	}
	var netErr net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return &Failure{Code: "timeout", Category: "temporary", Message: "额度查询超时", Retryable: true}
	}
	return &Failure{Code: code, Category: "temporary", Message: message, Retryable: true}
}
func httpFailure(status int, headers http.Header) *Failure {
	f := &Failure{Code: "upstream_http_error", Category: "business", Message: "上游拒绝额度查询", UpstreamStatus: status}
	switch {
	case status == 401:
		f.Code = "authentication_failed"
		f.Category = "authentication"
		f.Message = "上游额度查询凭据无效"
	case status == 403:
		f.Code = "permission_denied"
		f.Category = "permission"
		f.Message = "上游额度查询权限不足"
	case status == 429:
		f.Code = "rate_limited"
		f.Category = "rate_limited"
		f.Message = "上游额度查询请求过于频繁"
		f.Retryable = true
		f.RetryAfterSeconds = retryAfter(headers.Get("Retry-After"), time.Now())
	case status >= 500:
		f.Code = "upstream_unavailable"
		f.Category = "temporary"
		f.Message = "上游额度查询服务暂时不可用"
		f.Retryable = true
	case status >= 300 && status < 400:
		f.Code = "redirect_rejected"
		f.Category = "configuration"
		f.Message = "额度查询不允许重定向"
	}
	return f
}
func retryAfter(value string, now time.Time) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return 0
		}
		return min(seconds, 3600)
	}
	if date, err := http.ParseTime(value); err == nil {
		wait := date.Sub(now)
		if wait > 0 {
			seconds := int64(wait / time.Second)
			if wait%time.Second != 0 {
				seconds++
			}
			return min(seconds, 3600)
		}
	}
	return 0
}

// POST requires explicit adapter capability and a bounded JSON body. No retries are issued.
func validRequestMethod(d Descriptor, spec RequestSpec) bool {
	if spec.Method == http.MethodGet {
		return len(spec.Body) == 0
	}
	if spec.Method != http.MethodPost || len(spec.Body) > maxCustomBodyBytes || spec.Headers.Get("Content-Type") != "application/json" {
		return false
	}
	if len(spec.Body) > 0 && !json.Valid(spec.Body) {
		return false
	}
	for _, method := range d.RequestMethods {
		if method == http.MethodPost {
			return true
		}
	}
	return false
}
