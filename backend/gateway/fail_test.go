package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// failSpec 行为契约表：每行 = 一个错误值 → 处置决定五组字段的完整断言。
// 语义唯一实现在 fail.go 的 failSpec；这里的行取自既有集成测试隐含的全部
// 分支（含 committed+Canceled 等顺序敏感对），改动 failSpec 前先跑本表。

// fakeNetErr 满足 net.Error 的最小网络错误（dial 失败 / 连接重置的替身）。
type fakeNetErr struct{}

func (fakeNetErr) Error() string   { return "fake net error" }
func (fakeNetErr) Timeout() bool   { return false }
func (fakeNetErr) Temporary() bool { return false }

func TestFailSpec(t *testing.T) {
	rows := []struct {
		name string
		err  error
		want failDecision
	}{
		{"nil", nil, failDecision{}},
		{"客户端取消", context.Canceled,
			failDecision{ErrKind: shared.ErrKindClientCancel, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"url.Error 包装取消（net.Error 形态仍归取消）", &url.Error{Op: "Post", Err: context.Canceled},
			failDecision{ErrKind: shared.ErrKindClientCancel, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"已提交+取消（归用户行为不计熔断）", &streamCommittedError{err: context.Canceled},
			failDecision{ErrKind: shared.ErrKindClientCancel, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"已提交+断连", &streamCommittedError{err: io.ErrUnexpectedEOF},
			failDecision{BreakerFail: true, ErrKind: shared.ErrKindStreamInterrupt, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"已提交+流静默（committed 优先于 stall）", &streamCommittedError{err: &streamStallError{stage: "静默", wait: time.Minute}},
			failDecision{BreakerFail: true, ErrKind: shared.ErrKindStreamInterrupt, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"已提交+上游503（committed 优先于上游错误）", &streamCommittedError{err: &upstreamStatusError{status: 503}},
			failDecision{BreakerFail: true, ErrKind: shared.ErrKindStreamInterrupt, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"等头假死（快速开路+504）", &upstreamHeaderTimeoutError{seconds: 30},
			failDecision{Retryable: true, StallNoHeader: true, BreakerFail: true, ErrKind: shared.ErrKindNetwork, Status: http.StatusGatewayTimeout, RelayKind: relayErrAPI}},
		{"流静默假死（保留重试+504）", &streamStallError{stage: "首包", wait: 30 * time.Second},
			failDecision{Retryable: true, StallMidStream: true, BreakerFail: true, ErrKind: shared.ErrKindWatchdog, Status: http.StatusGatewayTimeout, RelayKind: relayErrAPI}},
		{"上游429+Retry-After透传", &upstreamStatusError{status: 429, retryAfter: "30"},
			failDecision{Retryable: true, BreakerFail: true, ErrKind: shared.ErrKindUpstream, Status: http.StatusTooManyRequests, RelayKind: relayErrRateLimit, RetryAfter: "30"}},
		{"上游500", &upstreamStatusError{status: 500},
			failDecision{Retryable: true, BreakerFail: true, ErrKind: shared.ErrKindUpstream, Status: 500, RelayKind: relayErrAPI}},
		{"上游503", &upstreamStatusError{status: 503},
			failDecision{Retryable: true, BreakerFail: true, ErrKind: shared.ErrKindUpstream, Status: 503, RelayKind: relayErrAPI}},
		{"上游400（请求问题不重试不计熔断）", &upstreamStatusError{status: 400},
			failDecision{ErrKind: shared.ErrKindUpstream, Status: 400, RelayKind: relayErrInvalidRequest}},
		{"上游401（渠道鉴权问题，引导换 key 而非改 payload）", &upstreamStatusError{status: 401},
			failDecision{ErrKind: shared.ErrKindUpstream, Status: 401, RelayKind: relayErrAuth}},
		{"上游403", &upstreamStatusError{status: 403},
			failDecision{ErrKind: shared.ErrKindUpstream, Status: 403, RelayKind: relayErrAuth}},
		{"上游404（目标不存在）", &upstreamStatusError{status: 404},
			failDecision{ErrKind: shared.ErrKindUpstream, Status: 404, RelayKind: relayErrNotFound}},
		{"协议转换失败", &convertError{err: errors.New("解析请求失败")},
			failDecision{ErrKind: shared.ErrKindConvert, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"网络错误", fakeNetErr{},
			failDecision{Retryable: true, BreakerFail: true, ErrKind: shared.ErrKindNetwork, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
		{"url.Error 包装非流式总超时（网络归因+504）", &url.Error{Op: "Post", Err: context.DeadlineExceeded},
			failDecision{Retryable: true, BreakerFail: true, ErrKind: shared.ErrKindNetwork, Status: http.StatusGatewayTimeout, RelayKind: relayErrAPI}},
		{"裸 DeadlineExceeded（自满足 net.Error，仍 504）", context.DeadlineExceeded,
			failDecision{Retryable: true, BreakerFail: true, ErrKind: shared.ErrKindNetwork, Status: http.StatusGatewayTimeout, RelayKind: relayErrAPI}},
		{"未知错误", errors.New("boom"),
			failDecision{ErrKind: shared.ErrKindInternal, Status: http.StatusBadGateway, RelayKind: relayErrAPI}},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			if got := failSpec(tc.err); got != tc.want {
				t.Fatalf("failSpec = %+v, want %+v", got, tc.want)
			}
			// 薄委托 shim 必须与查表一致——防两套语义漂移
			if got := isRetryable(tc.err); got != tc.want.Retryable {
				t.Errorf("isRetryable = %v, want %v", got, tc.want.Retryable)
			}
			if got := isStall(tc.err); got != tc.want.StallNoHeader {
				t.Errorf("isStall = %v, want %v", got, tc.want.StallNoHeader)
			}
			if got := classifyErr(tc.err); got != tc.want.ErrKind {
				t.Errorf("classifyErr = %q, want %q", got, tc.want.ErrKind)
			}
		})
	}
}
