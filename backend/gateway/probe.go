package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
	"github.com/gin-gonic/gin"
)

const probeResponseLimit = 1 << 20

// ProbeOptions 仅允许用户主动验证一个模型和一种响应模式。
type ProbeOptions struct {
	Model           string `json:"model"`
	Stream          bool   `json:"stream"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

type ProbeResult struct {
	Success         bool   `json:"success"`
	Stage           string `json:"stage"`
	Message         string `json:"message"`
	Status          int    `json:"status"`
	DurationMs      int64  `json:"duration_ms"`
	Model           string `json:"model"`
	Stream          bool   `json:"stream"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

func probeConfig() shared.ProxyConfig {
	cfg := shared.DefaultProxyConfig()
	cfg.RetryCount = 0
	cfg.ConnectTimeoutSeconds = 5
	cfg.FirstByteTimeoutSeconds = 15
	cfg.StreamIdleTimeoutSeconds = 10
	cfg.NonStreamTimeoutSeconds = 25
	return cfg
}

// 一个专属连接池供所有验证复用；不共享正式调用的熔断状态或日志依赖。
var probeHTTPClient = func() *http.Client {
	client := NewHTTPClient(probeConfig())
	client.Transport = probeTransport{base: client.Transport}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}()

// ProbeConnection 复用生产 relay 的装配与发送，只调用单个目标一次。
// 它绕过路由编排，使用私有熔断器和内存日志，不访问数据库。
func ProbeConnection(ctx context.Context, ch channel.Channel, opt ProbeOptions) (result ProbeResult) {
	start := time.Now()
	result = ProbeResult{Stage: "input", Message: "请指定模型，输出上限必须为 1–128 token", Model: strings.TrimSpace(opt.Model), Stream: opt.Stream, MaxOutputTokens: opt.MaxOutputTokens}
	defer func() { result.DurationMs = time.Since(start).Milliseconds() }()
	if result.Model == "" || len(result.Model) > 512 || opt.MaxOutputTokens < 1 || opt.MaxOutputTokens > 128 {
		return
	}
	conn, err := upstream.Open(ch.ConnectionConfig())
	if err != nil {
		result.Stage, result.Message = "request", "连接配置无效"
		return
	}
	if _, err = http.NewRequest(http.MethodPost, conn.ChatURL(result.Model, opt.Stream), nil); err != nil {
		result.Stage, result.Message = "request", "无法构建上游请求"
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"model": result.Model, "stream": opt.Stream, "max_tokens": opt.MaxOutputTokens, "messages": []map[string]string{{"role": "user", "content": "只回复 OK"}}})
	writer := &probeWriter{header: make(http.Header)}
	c, _ := gin.CreateTestContext(writer)
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(body))
	var entry *shared.Log
	cfg := probeConfig()
	server := NewRelayServer(Deps{Cfg: cfg, Client: probeHTTPClient, Breakers: NewBreakerStore(cfg), LogWrite: func(e *shared.Log) { entry = e }})
	server.quiet = true
	target := router.Target{Channel: ch, Model: model.Model{Name: result.Model}}
	sequence := 0
	trace := &relayAttemptTrace{sequence: &sequence}
	out, status, callErr := server.relayToTarget(c, protocol.ProviderOpenAI, opt.Stream, &group.Group{Name: "渠道调用验证"}, target, body, 0, newRequestID(), nil, &chatBudgetRetry{}, trace)
	result.Status = status
	result.Stage, result.Message = probeFailure(ctx, entry, callErr, status)
	if result.Stage != "" {
		return
	}
	if opt.Stream {
		out = writer.body.Bytes()
	}
	if writer.exceeded || !validProbeResponse(out, opt.Stream) {
		result.Stage, result.Message = "response", "上游响应缺少有效的模型输出或结束标记"
		return
	}
	result.Success = true
	result.Stage, result.Message = "complete", "指定模型的本次调用验证通过"
	return
}

func probeFailure(ctx context.Context, entry *shared.Log, err error, status int) (string, string) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled", "调用验证已取消"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout", "调用验证超时"
	}
	var ne net.Error
	if (errors.As(err, &ne) && ne.Timeout()) || (err != nil && failSpec(err).Status == http.StatusGatewayTimeout) || (entry != nil && entry.ErrKind == shared.ErrKindWatchdog) {
		return "timeout", "调用验证超时"
	}
	if status == 0 {
		return "request", "无法完成上游连接或请求发送"
	}
	if status < 200 || status >= 300 {
		return "upstream", "上游请求失败，请检查连接、凭据和模型权限"
	}
	if err != nil || entry == nil || !entry.Success {
		return "response", "上游响应读取、转换或流处理失败"
	}
	return "", ""
}

func validProbeResponse(body []byte, stream bool) bool {
	type choice struct {
		Message      map[string]json.RawMessage `json:"message"`
		Delta        map[string]json.RawMessage `json:"delta"`
		FinishReason *string                    `json:"finish_reason"`
	}
	type response struct {
		Choices []choice        `json:"choices"`
		Error   json.RawMessage `json:"error"`
	}
	if !stream {
		var r response
		if json.Unmarshal(body, &r) != nil || len(r.Error) > 0 || len(r.Choices) == 0 {
			return false
		}
		for _, item := range r.Choices {
			if probeHasText(item.Message) && item.FinishReason != nil && *item.FinishReason != "" {
				return true
			}
		}
		return false
	}
	content, finished := false, false
	for _, line := range bytes.Split(body, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var r response
		if json.Unmarshal(data, &r) != nil || len(r.Error) > 0 {
			return false
		}
		for _, item := range r.Choices {
			if probeHasText(item.Delta) {
				content = true
			}
			if item.FinishReason != nil && *item.FinishReason != "" {
				finished = true
			}
		}
	}
	return content && finished
}

func probeHasText(fields map[string]json.RawMessage) bool {
	for _, key := range []string{"content", "reasoning_content"} {
		var text string
		if json.Unmarshal(fields[key], &text) == nil && strings.TrimSpace(text) != "" {
			return true
		}
	}
	return false
}

// 同时限制上游原始响应与转换后输出，避免验证接口积累任意大小正文。
type probeTransport struct{ base http.RoundTripper }

func (t probeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		resp.Body = &probeBody{ReadCloser: resp.Body, remaining: probeResponseLimit}
	}
	return resp, err
}

type probeBody struct {
	io.ReadCloser
	remaining int
}

func (b *probeBody) Read(p []byte) (int, error) {
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= n
	if b.remaining < 0 {
		return 0, errors.New("调用验证响应超过大小上限")
	}
	return n, err
}

type probeWriter struct {
	header   http.Header
	body     bytes.Buffer
	exceeded bool
}

func (w *probeWriter) Header() http.Header { return w.header }
func (*probeWriter) WriteHeader(int)       {}
func (*probeWriter) Flush()                {}
func (w *probeWriter) Write(p []byte) (int, error) {
	if w.body.Len()+len(p) > probeResponseLimit {
		w.exceeded = true
		return 0, errors.New("调用验证响应超过大小上限")
	}
	return w.body.Write(p)
}
