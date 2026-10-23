package gateway

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestChannelCapacityAtomicLimitAndIdempotentRelease(t *testing.T) {
	s := NewChannelConcurrencyStore()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	leases := make(chan *channelCapacityLease, 100)
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := s.Acquire(7, 2)
			if err == nil {
				admitted.Add(1)
				leases <- l
			} else {
				var full *channelCapacityError
				if !errors.As(err, &full) || full.InFlight != 2 || full.Limit != 2 {
					t.Errorf("admission=%v", err)
				}
			}
		}()
	}
	wg.Wait()
	close(leases)
	if admitted.Load() != 2 {
		t.Fatalf("admitted=%d", admitted.Load())
	}
	if full := s.Check(7, 2); full == nil {
		t.Fatal("full channel allowed")
	}
	for l := range leases {
		for n := 0; n < 10; n++ {
			wg.Add(1)
			go func() { defer wg.Done(); l.Release() }()
		}
	}
	wg.Wait()
	if len(s.active) != 0 {
		t.Fatalf("leaked=%v", s.active)
	}
	next, err := s.Acquire(7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := next.Release(); !ok {
		t.Fatal("lease not released")
	}
}

func TestChannelCapacityUnlimitedAndChangedLimitsShareLiveCounts(t *testing.T) {
	s := NewChannelConcurrencyStore()
	a, _ := s.Acquire(1, 0)
	b, _ := s.Acquire(1, 0)
	if full := s.Check(1, 1); full == nil || full.InFlight != 2 {
		t.Fatalf("unlimited live calls forgotten: %v", full)
	}
	if _, err := s.Acquire(1, 1); err == nil {
		t.Fatal("new limit ignored live calls")
	}
	// Raising the limit reuses the same bucket, rather than resetting occupied slots.
	c, err := s.Acquire(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Acquire(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	a.Release()
	a.Release()
	if _, err := s.Acquire(1, 2); err == nil {
		t.Fatal("repeat release decremented another live lease")
	}
	b.Release()
	c.Release()
	other.Release()
	if len(s.active) != 0 {
		t.Fatalf("inactive entries retained: %v", s.active)
	}
}

func TestChannelCapacityLeaseCannotReleaseNewOccupant(t *testing.T) {
	s := NewChannelConcurrencyStore()
	old, _ := s.Acquire(1, 1)
	old.Release()
	current, _ := s.Acquire(1, 1)
	old.Release()
	if _, err := s.Acquire(1, 1); err == nil {
		t.Fatal("old release stole current slot")
	}
	current.Release()
}
