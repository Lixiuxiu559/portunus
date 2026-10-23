package gateway

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// requestControl belongs to one client request, including all targets and compatibility Do calls.
// Its deadline timer is removable: delivered streams may outlive the first-content deadline.
// Non-streaming requests keep the deadline through response reading and conversion.
type requestControl struct {
	mu                      sync.Mutex
	ctx                     context.Context
	cancel                  context.CancelCauseFunc
	parent                  context.Context
	max, used               int
	deadline                time.Time
	deadlineCause           error
	backoffBase, backoffMax time.Duration
	timer                   *time.Timer
	delivered               bool
}
type requestControlKey struct{}
type attemptBudgetError struct{}

func (*attemptBudgetError) Error() string { return "请求级 HTTP 尝试预算已耗尽" }

type firstContentDeadlineError struct{}

func (*firstContentDeadlineError) Error() string { return "请求累计首内容等待期限已到" }

type nonStreamDeadlineError struct{}

func (*nonStreamDeadlineError) Error() string { return "请求累计非流式处理期限已到" }

// clientDeadlineError distinguishes a caller deadline from an upstream timeout.
type clientDeadlineError struct{}

func (*clientDeadlineError) Error() string { return "客户端请求期限已到" }
func (*clientDeadlineError) Unwrap() error { return context.DeadlineExceeded }

func newRelayRequestControl(parent context.Context, cfg shared.ProxyConfig, stream bool) *requestControl {
	defaults := shared.DefaultProxyConfig()
	seconds := cfg.NonStreamTimeoutSeconds
	defaultSeconds := defaults.NonStreamTimeoutSeconds
	var cause error = &nonStreamDeadlineError{}
	if stream {
		seconds, defaultSeconds = cfg.FirstContentTimeoutSeconds, defaults.FirstContentTimeoutSeconds
		cause = &firstContentDeadlineError{}
	}
	if seconds <= 0 {
		seconds = defaultSeconds
	}
	r := newRequestControlWithCause(parent, cfg.MaxHTTPAttempts, boundedDuration(seconds, time.Second), cause)
	base, max := cfg.RetryBackoffBaseMs, cfg.RetryBackoffMaxMs
	if base <= 0 {
		base = defaults.RetryBackoffBaseMs
	}
	if max <= 0 {
		max = defaults.RetryBackoffMaxMs
	}
	r.backoffBase, r.backoffMax = boundedDuration(base, time.Millisecond), boundedDuration(max, time.Millisecond)
	if r.backoffMax < r.backoffBase {
		r.backoffMax = r.backoffBase
	}
	return r
}

// boundedDuration prevents valid positive integer configuration from overflowing a duration.
func boundedDuration(n int, unit time.Duration) time.Duration {
	const largest = time.Duration(1<<63 - 1)
	if uint64(n) > uint64(largest/unit) {
		return largest
	}
	return time.Duration(n) * unit
}

func newRequestControl(parent context.Context, max int, wait time.Duration) *requestControl {
	return newRequestControlWithCause(parent, max, wait, &firstContentDeadlineError{})
}

func newRequestControlWithCause(parent context.Context, max int, wait time.Duration, cause error) *requestControl {
	if max <= 0 {
		max = shared.DefaultProxyConfig().MaxHTTPAttempts
	}
	ctx, cancel := context.WithCancelCause(parent)
	defaults := shared.DefaultProxyConfig()
	r := &requestControl{ctx: ctx, cancel: cancel, parent: parent, max: max, deadlineCause: cause,
		backoffBase: boundedDuration(defaults.RetryBackoffBaseMs, time.Millisecond),
		backoffMax:  boundedDuration(defaults.RetryBackoffMaxMs, time.Millisecond)}
	r.ctx = context.WithValue(ctx, requestControlKey{}, r)
	if wait > 0 {
		r.deadline = time.Now().Add(wait)
		r.timer = time.AfterFunc(wait, func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if !r.delivered {
				r.cancel(r.deadlineCause)
			}
		})
	}
	return r
}
func controlFrom(ctx context.Context) *requestControl {
	r, _ := ctx.Value(requestControlKey{}).(*requestControl)
	return r
}
func (r *requestControl) checkLocked() error {
	if err := r.parent.Err(); err != nil {
		if err == context.DeadlineExceeded {
			return &clientDeadlineError{}
		}
		return err
	}
	if !r.delivered && !r.deadline.IsZero() && !time.Now().Before(r.deadline) {
		r.cancel(r.deadlineCause)
	}
	return context.Cause(r.ctx)
}
func (r *requestControl) check() error { r.mu.Lock(); defer r.mu.Unlock(); return r.checkLocked() }

// reserve is called immediately before trace.begin and the explicit Client.Do.
func (r *requestControl) reserve() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return err
	}
	if r.delivered || r.used >= r.max {
		return &attemptBudgetError{}
	}
	r.used++
	return nil
}
func (r *requestControl) available() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return err
	}
	if r.used >= r.max || r.delivered {
		return &attemptBudgetError{}
	}
	return nil
}

// retryDelay uses the previous HTTP attempt count; no attempt is reserved while waiting.
// Equal jitter prevents synchronised retries while retaining a lower bound on spacing.
func (r *requestControl) retryDelay(used int) time.Duration {
	if used <= 0 {
		return 0
	}
	cap := r.backoffBase
	for i := 1; i < used && cap < r.backoffMax; i++ {
		if cap > r.backoffMax/2 {
			cap = r.backoffMax
			break
		}
		cap *= 2
	}
	if cap > r.backoffMax {
		cap = r.backoffMax
	}
	if cap <= 0 {
		return 0
	}
	lower := cap / 2
	return lower + time.Duration(rand.Int64N(int64(cap-lower)+1))
}

// waitForAttempt is shared by all later Do calls, including compatibility retries.
// Waiting holds no lock, reserves no budget, and is interruptible by either context.
func (r *requestControl) waitForAttempt(ctx context.Context) error {
	r.mu.Lock()
	err := r.checkLocked()
	if err == nil && (r.delivered || r.used >= r.max) {
		err = &attemptBudgetError{}
	}
	delay := r.retryDelay(r.used)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if err = context.Cause(ctx); err != nil {
		if requestErr := r.check(); requestErr != nil {
			return requestErr
		}
		return err
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.ctx.Done():
		case <-ctx.Done():
		}
	}
	if err := r.available(); err != nil {
		return err
	}
	return context.Cause(ctx)
}

// commit arbitrates delivery (or non-streaming response readiness) against expiry under one lock.
func (r *requestControl) commit() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkLocked(); err != nil {
		return err
	}
	r.delivered = true
	if r.timer != nil {
		r.timer.Stop()
	}
	return nil
}
func (r *requestControl) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
	}
	r.cancel(context.Canceled)
}
func requestControlError(ctx context.Context) error {
	if r := controlFrom(ctx); r != nil {
		if err := r.check(); err != nil {
			return err
		}
	}
	return ctx.Err()
}
