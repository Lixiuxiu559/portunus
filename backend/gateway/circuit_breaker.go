package gateway

import (
	"fmt"
	"sync"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

type circuitState int

const (
	stateClosed circuitState = iota
	stateOpen
	stateHalfOpen
)

// A breaker is model-scoped and shared by all groups. Every field is guarded by mu.
type circuitBreaker struct {
	mu                 sync.Mutex
	state              circuitState
	consecutiveFailure int
	consecutiveStall   int
	consecutiveSuccess int
	openedAt           time.Time
	generation         uint64
	active             *breakerLease
}

const stallOpenThreshold = 2

type BreakerStore struct {
	cfg shared.ProxyConfig
	m   sync.Map // int64(modelID) -> *circuitBreaker
	now func() time.Time
}

func NewBreakerStore(cfg shared.ProxyConfig) *BreakerStore {
	defaults := shared.DefaultProxyConfig()
	if cfg.CircuitFailureThreshold <= 0 {
		cfg.CircuitFailureThreshold = defaults.CircuitFailureThreshold
	}
	if cfg.CircuitSuccessThreshold <= 0 {
		cfg.CircuitSuccessThreshold = defaults.CircuitSuccessThreshold
	}
	if cfg.CircuitResetSeconds <= 0 {
		cfg.CircuitResetSeconds = defaults.CircuitResetSeconds
	}
	return &BreakerStore{cfg: cfg, now: time.Now}
}

func (bs *BreakerStore) breaker(modelID int64) *circuitBreaker {
	v, _ := bs.m.LoadOrStore(modelID, &circuitBreaker{})
	return v.(*circuitBreaker)
}

type circuitUnavailableError struct {
	Reason     string
	RetryAfter int
}

func (e *circuitUnavailableError) Error() string { return "熔断目标暂不可用：" + e.Reason }

// remainingLocked rounds up without overflowing a duration or adding a whole second.
func (bs *BreakerStore) remainingLocked(b *circuitBreaker) int {
	reset := boundedDuration(bs.cfg.CircuitResetSeconds, time.Second)
	elapsed := bs.now().Sub(b.openedAt)
	// A clock rollback must not overflow the remaining-duration subtraction.
	remain := reset
	if elapsed >= 0 {
		remain = reset - elapsed
	}
	if remain <= 0 {
		return 0
	}
	seconds := remain / time.Second
	if remain%time.Second != 0 {
		seconds++
	}
	return int(seconds)
}

func (bs *BreakerStore) unavailableLocked(b *circuitBreaker) *circuitUnavailableError {
	if b.state == stateOpen {
		if seconds := bs.remainingLocked(b); seconds > 0 {
			return &circuitUnavailableError{Reason: "circuit_open", RetryAfter: seconds}
		}
	}
	if b.state == stateHalfOpen && b.active != nil {
		return &circuitUnavailableError{Reason: "probe_busy", RetryAfter: 1}
	}
	return nil
}

// Allow is only a routing preview: it neither changes state nor reserves a probe.
func (bs *BreakerStore) unavailable(modelID int64) *circuitUnavailableError {
	b := bs.breaker(modelID)
	b.mu.Lock()
	defer b.mu.Unlock()
	return bs.unavailableLocked(b)
}

func (bs *BreakerStore) Allow(modelID int64) (bool, string) {
	if err := bs.unavailable(modelID); err != nil {
		return false, fmt.Sprintf("%s，约 %d 秒后可重试", err.Error(), err.RetryAfter)
	}
	return true, ""
}

func (bs *BreakerStore) CooldownSeconds(modelID int64) int {
	v, ok := bs.m.Load(modelID)
	if !ok {
		return 0
	}
	b := v.(*circuitBreaker)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := bs.unavailableLocked(b); err != nil {
		return err.RetryAfter
	}
	return 0
}

type breakerOutcome uint8

const (
	breakerIgnored breakerOutcome = iota
	breakerSucceeded
	breakerFailed
	breakerStalled
)

// A lease lives through one relay, including its sequential compatibility Do calls.
// It never expires by wall clock: an in-flight probe must finish before another starts.
type breakerLease struct {
	store      *BreakerStore
	breaker    *circuitBreaker
	generation uint64
	probe      bool
	finished   bool // guarded by breaker.mu
}

func (bs *BreakerStore) Acquire(modelID int64) (*breakerLease, error) {
	b := bs.breaker(modelID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := bs.unavailableLocked(b); err != nil {
		return nil, err
	}
	if b.state == stateOpen {
		b.state = stateHalfOpen
		b.generation++
		b.consecutiveSuccess = 0
	}
	lease := &breakerLease{store: bs, breaker: b, generation: b.generation, probe: b.state == stateHalfOpen}
	if lease.probe {
		b.active = lease
	}
	return lease, nil
}

// Check allows sequential reuse but rejects a lease invalidated by a newer state epoch.
func (l *breakerLease) Check() error {
	if l == nil {
		return nil
	}
	b := l.breaker
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.finished || l.generation != b.generation || (l.probe && b.active != l) {
		return &circuitUnavailableError{Reason: "stale_lease", RetryAfter: 1}
	}
	return nil
}

// Finish reports whether the outcome belonged to the active epoch; stale or repeated
// results return false without changing the breaker or releasing another probe.
func (l *breakerLease) Finish(outcome breakerOutcome) bool {
	if l == nil {
		return false
	}
	b := l.breaker
	b.mu.Lock()
	defer b.mu.Unlock()
	if l.finished {
		return false
	}
	l.finished = true
	if l.generation != b.generation || (l.probe && b.active != l) {
		return false
	}
	if l.probe {
		b.active = nil
	}
	if outcome == breakerIgnored {
		return true
	}
	l.store.recordLocked(b, outcome, l.probe)
	return true
}

func (bs *BreakerStore) openLocked(b *circuitBreaker) {
	b.state = stateOpen
	b.openedAt = bs.now()
	b.consecutiveSuccess = 0
	b.generation++
	b.active = nil
}

func (bs *BreakerStore) recordLocked(b *circuitBreaker, outcome breakerOutcome, probe bool) {
	if outcome == breakerSucceeded {
		b.consecutiveFailure = 0
		b.consecutiveStall = 0
		if probe {
			b.consecutiveSuccess++
			if b.consecutiveSuccess >= bs.cfg.CircuitSuccessThreshold {
				b.state = stateClosed
				b.generation++
				b.consecutiveSuccess = 0
			}
		}
		return
	}
	b.consecutiveSuccess = 0
	if b.consecutiveFailure < bs.cfg.CircuitFailureThreshold {
		b.consecutiveFailure++
	}
	if outcome == breakerStalled && b.consecutiveStall < stallOpenThreshold {
		b.consecutiveStall++
	}
	if probe || b.consecutiveFailure >= bs.cfg.CircuitFailureThreshold || b.consecutiveStall >= stallOpenThreshold {
		bs.openLocked(b)
	}
}

// Legacy fault injection/embedding API. Unleased successes can only update closed state;
// recovering an open breaker requires an acquired probe, never a late ordinary success.
func (bs *BreakerStore) Record(modelID int64, retryableFailure bool) {
	b := bs.breaker(modelID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if retryableFailure {
		if b.state != stateClosed {
			bs.openLocked(b)
			return
		}
		bs.recordLocked(b, breakerFailed, false)
	} else if b.state == stateClosed {
		bs.recordLocked(b, breakerSucceeded, false)
	}
}

func (bs *BreakerStore) RecordStall(modelID int64) {
	b := bs.breaker(modelID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != stateClosed {
		bs.openLocked(b)
		return
	}
	bs.recordLocked(b, breakerStalled, false)
}
