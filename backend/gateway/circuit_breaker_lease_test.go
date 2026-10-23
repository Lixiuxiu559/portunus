package gateway

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func leaseTestStore() (*BreakerStore, *time.Time) {
	cfg := shared.DefaultProxyConfig()
	cfg.CircuitFailureThreshold = 2
	cfg.CircuitResetSeconds = 5
	bs := NewBreakerStore(cfg)
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	bs.now = func() time.Time { return now }
	return bs, &now
}
func leaseTestOpen(bs *BreakerStore, id int64) { bs.Record(id, true); bs.Record(id, true) }
func mustLease(t *testing.T, bs *BreakerStore, id int64) *breakerLease {
	t.Helper()
	l, err := bs.Acquire(id)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func requireLeaseReason(t *testing.T, err error, reason string) {
	t.Helper()
	var e *circuitUnavailableError
	if !errors.As(err, &e) || e.Reason != reason || e.RetryAfter < 1 {
		t.Fatalf("error=%v, want %s with positive retry", err, reason)
	}
}
func TestBreakerLeaseSingleConcurrentProbe(t *testing.T) {
	bs, now := leaseTestStore()
	leaseTestOpen(bs, 1)
	*now = now.Add(5 * time.Second)
	for i := 0; i < 10; i++ {
		if ok, _ := bs.Allow(1); !ok {
			t.Fatal("preview reserved probe")
		}
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	leases := make(chan *breakerLease, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := bs.Acquire(1)
			if err == nil {
				count.Add(1)
				leases <- l
			} else {
				var e *circuitUnavailableError
				if !errors.As(err, &e) || e.Reason != "probe_busy" {
					t.Errorf("acquire: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	close(leases)
	if count.Load() != 1 {
		t.Fatalf("admitted %d", count.Load())
	}
	if ok, _ := bs.Allow(1); ok {
		t.Fatal("busy probe preview allowed")
	}
	if bs.CooldownSeconds(1) != 1 {
		t.Fatal("probe busy retry-after")
	}
	*now = now.Add(24 * time.Hour)
	_, err := bs.Acquire(1)
	requireLeaseReason(t, err, "probe_busy")
	for l := range leases {
		l.Finish(breakerIgnored)
	}
	l := mustLease(t, bs, 1)
	l.Finish(breakerIgnored)
}
func TestBreakerLeaseNeutralAndIdempotentRelease(t *testing.T) {
	bs, now := leaseTestStore()
	leaseTestOpen(bs, 1)
	*now = now.Add(5 * time.Second)
	first := mustLease(t, bs, 1)
	for i := 0; i < 3; i++ {
		if err := first.Check(); err != nil {
			t.Fatal(err)
		}
	}
	first.Finish(breakerIgnored)
	requireLeaseReason(t, first.Check(), "stale_lease")
	second := mustLease(t, bs, 1)
	first.Finish(breakerFailed)
	_, err := bs.Acquire(1)
	requireLeaseReason(t, err, "probe_busy")
	if err := second.Check(); err != nil {
		t.Fatal(err)
	}
	second.Finish(breakerSucceeded)
	third := mustLease(t, bs, 1)
	third.Finish(breakerSucceeded)
	// Closing permits concurrent ordinary requests.
	a := mustLease(t, bs, 1)
	b := mustLease(t, bs, 1)
	a.Finish(breakerIgnored)
	b.Finish(breakerIgnored)
}
func TestBreakerLeaseStaleClosedResultsCannotChangeRecovery(t *testing.T) {
	for _, outcome := range []breakerOutcome{breakerSucceeded, breakerFailed, breakerStalled} {
		t.Run(string(rune('a'+outcome)), func(t *testing.T) {
			bs, now := leaseTestStore()
			old := mustLease(t, bs, 1)
			leaseTestOpen(bs, 1)
			*now = now.Add(5 * time.Second)
			p := mustLease(t, bs, 1)
			requireLeaseReason(t, old.Check(), "stale_lease")
			old.Finish(outcome)
			if err := p.Check(); err != nil {
				t.Fatal(err)
			}
			p.Finish(breakerSucceeded)
			q := mustLease(t, bs, 1)
			q.Finish(breakerSucceeded)
			if state := bs.breaker(1).state; state != stateClosed {
				t.Fatalf("state=%v", state)
			}
		})
	}
}
func TestBreakerLeaseProbeFailureImmediatelyReopens(t *testing.T) {
	for _, outcome := range []breakerOutcome{breakerFailed, breakerStalled} {
		bs, now := leaseTestStore()
		leaseTestOpen(bs, 1)
		*now = now.Add(5 * time.Second)
		l := mustLease(t, bs, 1)
		*now = now.Add(2 * time.Second)
		l.Finish(outcome)
		_, err := bs.Acquire(1)
		requireLeaseReason(t, err, "circuit_open")
		if bs.CooldownSeconds(1) != 5 {
			t.Fatal("failure did not reset cooldown")
		}
		*now = now.Add(5 * time.Second)
		next := mustLease(t, bs, 1)
		l.Finish(breakerSucceeded)
		_, err = bs.Acquire(1)
		requireLeaseReason(t, err, "probe_busy")
		if err := next.Check(); err != nil {
			t.Fatal(err)
		}
		next.Finish(breakerIgnored)
	}
}
func TestBreakerLeaseLegacySuccessCannotRecoverAndModelIsolation(t *testing.T) {
	bs, now := leaseTestStore()
	leaseTestOpen(bs, 1)
	bs.Record(1, false)
	_, err := bs.Acquire(1)
	requireLeaseReason(t, err, "circuit_open")
	other := mustLease(t, bs, 2)
	other.Finish(breakerSucceeded)
	*now = now.Add(5 * time.Second)
	probe := mustLease(t, bs, 1)
	bs.Record(1, false)
	_, err = bs.Acquire(1)
	requireLeaseReason(t, err, "probe_busy")
	bs.Record(1, true)
	requireLeaseReason(t, probe.Check(), "stale_lease")
	probe.Finish(breakerSucceeded)
	_, err = bs.Acquire(1)
	requireLeaseReason(t, err, "circuit_open")
}
func TestBreakerLeaseCooldownRoundingAndDefaults(t *testing.T) {
	bs, now := leaseTestStore()
	leaseTestOpen(bs, 1)
	*now = now.Add(time.Nanosecond)
	if got := bs.CooldownSeconds(1); got != 5 {
		t.Fatalf("cooldown=%d", got)
	}
	*now = now.Add(4 * time.Second)
	if got := bs.CooldownSeconds(1); got != 1 {
		t.Fatalf("cooldown=%d", got)
	}
	defaults := shared.DefaultProxyConfig()
	fallback := NewBreakerStore(shared.ProxyConfig{})
	if fallback.cfg.CircuitResetSeconds != defaults.CircuitResetSeconds || fallback.cfg.CircuitFailureThreshold != defaults.CircuitFailureThreshold || fallback.cfg.CircuitSuccessThreshold != defaults.CircuitSuccessThreshold {
		t.Fatal("invalid default thresholds")
	}
	huge := NewBreakerStore(shared.ProxyConfig{CircuitResetSeconds: int(^uint(0) >> 1), CircuitFailureThreshold: 1})
	huge.Record(1, true)
	if huge.CooldownSeconds(1) <= 0 {
		t.Fatal("duration overflow bypassed cooldown")
	}
}

func TestBreakerLeaseOldEpochCannotPolluteRecoveredClosedState(t *testing.T) {
	for _, outcome := range []breakerOutcome{breakerSucceeded, breakerFailed, breakerStalled} {
		bs, now := leaseTestStore()
		oldClosed := mustLease(t, bs, 1)
		leaseTestOpen(bs, 1)
		*now = now.Add(5 * time.Second)
		oldProbe := mustLease(t, bs, 1)
		bs.Record(1, true) // An independent fault revokes the old probe epoch.
		*now = now.Add(5 * time.Second)
		mustLease(t, bs, 1).Finish(breakerSucceeded)
		mustLease(t, bs, 1).Finish(breakerSucceeded)
		oldClosed.Finish(outcome)
		oldProbe.Finish(outcome)
		if state := bs.breaker(1).state; state != stateClosed {
			t.Fatalf("stale outcome %v changed recovered state to %v", outcome, state)
		}
		a := mustLease(t, bs, 1)
		b := mustLease(t, bs, 1)
		a.Finish(breakerIgnored)
		b.Finish(breakerIgnored)
	}
}

func TestBreakerLeaseConcurrentFinishCountsOnce(t *testing.T) {
	bs, now := leaseTestStore()
	leaseTestOpen(bs, 1)
	*now = now.Add(5 * time.Second)
	lease := mustLease(t, bs, 1)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); lease.Finish(breakerSucceeded) }()
	}
	wg.Wait()
	if b := bs.breaker(1); b.state != stateHalfOpen || b.consecutiveSuccess != 1 {
		t.Fatalf("finish was counted more than once: state=%v success=%d", b.state, b.consecutiveSuccess)
	}
	next := mustLease(t, bs, 1)
	next.Finish(breakerSucceeded)
	if bs.breaker(1).state != stateClosed {
		t.Fatal("second distinct successful probe did not close")
	}
}
