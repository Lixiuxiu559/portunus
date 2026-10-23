package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestRetryBackoffJitterBoundsAndSaturation(t *testing.T) {
	r := newRequestControl(context.Background(), 10, 0)
	defer r.close()
	r.backoffBase, r.backoffMax = 8*time.Millisecond, 30*time.Millisecond
	for _, tc := range []struct {
		used int
		cap  time.Duration
	}{
		{0, 0}, {1, 8 * time.Millisecond}, {2, 16 * time.Millisecond}, {3, 30 * time.Millisecond}, {1000000000, 30 * time.Millisecond},
	} {
		for i := 0; i < 100; i++ {
			d := r.retryDelay(tc.used)
			if d < tc.cap/2 || d > tc.cap {
				t.Fatalf("used=%d delay=%v cap=%v", tc.used, d, tc.cap)
			}
		}
	}
	r.backoffBase, r.backoffMax = time.Duration(1<<62), time.Duration(1<<63-1)
	for i := 0; i < 100; i++ {
		if d := r.retryDelay(100); d < r.backoffMax/2 || d > r.backoffMax {
			t.Fatalf("overflow: %v", d)
		}
	}
}

func TestRelayRequestControlModeAndRuntimeDefaults(t *testing.T) {
	for _, stream := range []bool{false, true} {
		r := newRelayRequestControl(context.Background(), shared.ProxyConfig{}, stream)
		defaults := shared.DefaultProxyConfig()
		want := time.Duration(defaults.NonStreamTimeoutSeconds) * time.Second
		if stream {
			want = time.Duration(defaults.FirstContentTimeoutSeconds) * time.Second
		}
		if d := time.Until(r.deadline); d <= want-time.Second || d > want {
			t.Fatalf("stream=%v deadline=%v want=%v", stream, d, want)
		}
		if r.max != defaults.MaxHTTPAttempts || r.backoffBase != time.Duration(defaults.RetryBackoffBaseMs)*time.Millisecond || r.backoffMax != time.Duration(defaults.RetryBackoffMaxMs)*time.Millisecond {
			t.Fatalf("defaults: %+v", r)
		}
		if stream {
			var cause *firstContentDeadlineError
			if !errors.As(r.deadlineCause, &cause) {
				t.Fatalf("stream cause %T", r.deadlineCause)
			}
		} else {
			var cause *nonStreamDeadlineError
			if !errors.As(r.deadlineCause, &cause) {
				t.Fatalf("nonstream cause %T", r.deadlineCause)
			}
		}
		r.close()
	}
	cfg := shared.ProxyConfig{RetryBackoffBaseMs: 100, RetryBackoffMaxMs: 1}
	r := newRelayRequestControl(context.Background(), cfg, false)
	defer r.close()
	if r.backoffMax != r.backoffBase {
		t.Fatalf("invalid direct config not clamped: %v %v", r.backoffBase, r.backoffMax)
	}
}

func TestRetryBackoffUnavailableDoesNotWaitOrReserve(t *testing.T) {
	for _, state := range []string{"first", "budget", "committed", "attempt-canceled", "parent-canceled"} {
		t.Run(state, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := newRequestControl(parent, 1, 0)
			defer r.close()
			r.backoffBase, r.backoffMax = time.Hour, time.Hour
			ctx, cancelAttempt := context.WithCancel(r.ctx)
			defer cancelAttempt()
			switch state {
			case "budget":
				_ = r.reserve()
			case "committed":
				_ = r.commit()
			case "attempt-canceled":
				cancelAttempt()
			case "parent-canceled":
				cancel()
			}
			used := r.used
			started := time.Now()
			err := r.waitForAttempt(ctx)
			if time.Since(started) > time.Second {
				t.Fatal("unexpected wait")
			}
			if state == "first" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("state %s allowed", state)
			}
			if r.used != used {
				t.Fatalf("wait reserved attempt: %d != %d", r.used, used)
			}
		})
	}
}

func TestRetryBackoffCancellationInterruptsWait(t *testing.T) {
	for _, which := range []string{"attempt", "parent", "control"} {
		t.Run(which, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			r := newRequestControl(parent, 4, 0)
			defer r.close()
			r.backoffBase, r.backoffMax = time.Hour, time.Hour
			if err := r.reserve(); err != nil {
				t.Fatal(err)
			}
			ctx, cancelAttempt := context.WithCancel(r.ctx)
			defer cancelAttempt()
			result := make(chan error, 1)
			go func() { result <- r.waitForAttempt(ctx) }()
			switch which {
			case "attempt":
				cancelAttempt()
			case "parent":
				cancelParent()
			case "control":
				r.close()
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel cause %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("backoff not canceled")
			}
			if r.used != 1 {
				t.Fatalf("canceled wait consumed budget %d", r.used)
			}
		})
	}
}

func TestRetryBackoffRequestDeadlineCause(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			var cause error = &nonStreamDeadlineError{}
			if stream {
				cause = &firstContentDeadlineError{}
			}
			r := newRequestControlWithCause(context.Background(), 4, 10*time.Millisecond, cause)
			defer r.close()
			r.backoffBase, r.backoffMax = time.Hour, time.Hour
			if err := r.reserve(); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() { result <- r.waitForAttempt(r.ctx) }()
			select {
			case err := <-result:
				if err != cause || context.Cause(r.ctx) != cause {
					t.Fatalf("deadline lost: %v / %v", err, context.Cause(r.ctx))
				}
			case <-time.After(time.Second):
				t.Fatal("deadline did not cancel backoff")
			}
			if r.used != 1 {
				t.Fatalf("deadline consumed budget %d", r.used)
			}
		})
	}
}

func TestRetryBackoffCompletedWaitStillRequiresReserve(t *testing.T) {
	r := newRequestControl(context.Background(), 3, 0)
	defer r.close()
	r.backoffBase, r.backoffMax = 2*time.Millisecond, 2*time.Millisecond
	_ = r.reserve()
	started := time.Now()
	if err := r.waitForAttempt(r.ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < time.Millisecond {
		t.Fatal("missing lower jitter bound")
	}
	if r.used != 1 {
		t.Fatalf("wait reserved %d", r.used)
	}
	if err := r.reserve(); err != nil {
		t.Fatal(err)
	}
	if r.used != 2 {
		t.Fatal("reserve missing")
	}
}

func TestRequestControlDeadlineCoversProcessingUntilCommit(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			var cause error = &nonStreamDeadlineError{}
			if stream {
				cause = &firstContentDeadlineError{}
			}
			t.Run("pending-body-or-conversion", func(t *testing.T) {
				r := newRequestControlWithCause(context.Background(), 4, 5*time.Millisecond, cause)
				defer r.close()
				_ = r.reserve()
				select {
				case <-r.ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("processing deadline missing")
				}
				if err := r.commit(); err != cause {
					t.Fatalf("late commit: %v", err)
				}
			})
			t.Run("commit-disarms-deadline", func(t *testing.T) {
				r := newRequestControlWithCause(context.Background(), 4, 10*time.Millisecond, cause)
				defer r.close()
				if err := r.commit(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond)
				if err := r.check(); err != nil {
					t.Fatalf("committed request canceled: %v", err)
				}
			})
		})
	}
}

func TestRetryBackoffParentDeadlineIsLocal(t *testing.T) {
	for _, expiredBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "already_expired", false: "expires_during_wait"}[expiredBefore], func(t *testing.T) {
			wait := 20 * time.Millisecond
			if expiredBefore {
				wait = -time.Second
			}
			parent, cancel := context.WithTimeout(context.Background(), wait)
			defer cancel()
			r := newRequestControl(parent, 4, 0)
			defer r.close()
			r.backoffBase, r.backoffMax = time.Hour, time.Hour
			if !expiredBefore {
				if err := r.reserve(); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			err := r.waitForAttempt(r.ctx)
			var clientDeadline *clientDeadlineError
			if !errors.As(err, &clientDeadline) || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
				t.Fatalf("parent deadline lost: %v", err)
			}
			d := failSpec(err)
			if !d.StopRequest || d.Retryable || d.BreakerFail || d.ErrKind != shared.ErrKindClientCancel || d.Status != 504 {
				t.Fatalf("client deadline blamed upstream: %+v", d)
			}
			d = failSpec(&streamCommittedError{err: err})
			if d.Retryable || d.BreakerFail || d.ErrKind != shared.ErrKindClientCancel || d.Status != 504 {
				t.Fatalf("delivered client deadline blamed upstream: %+v", d)
			}
		})
	}
}
