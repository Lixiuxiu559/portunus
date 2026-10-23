package gateway

import "sync"

// channelCapacityError is a local admission rejection, never an upstream fault.
type channelCapacityError struct{ Limit, InFlight int }

func (*channelCapacityError) Error() string { return "渠道并发已满" }

// ChannelConcurrencyStore counts live relays by channel ID across models and groups.
// Counts are independent of connection fingerprints and configured limits. Unlimited
// relays are counted too, so enabling a limit cannot forget already running calls.
// Only active entries are retained; there is no queue and no expiring permit.
type ChannelConcurrencyStore struct {
	mu     sync.Mutex
	active map[int64]int
}

func NewChannelConcurrencyStore() *ChannelConcurrencyStore {
	return &ChannelConcurrencyStore{active: make(map[int64]int)}
}
func (s *ChannelConcurrencyStore) Check(channelID int64, limit int) *channelCapacityError {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit > 0 && s.active[channelID] >= limit {
		return &channelCapacityError{Limit: limit, InFlight: s.active[channelID]}
	}
	return nil
}

type channelCapacityLease struct {
	store         *ChannelConcurrencyStore
	channelID     int64
	limit         int
	acquiredCount int
	released      bool // protected by store.mu
}

func (s *ChannelConcurrencyStore) Acquire(channelID int64, limit int) (*channelCapacityLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.active[channelID]
	if limit > 0 && count >= limit {
		return nil, &channelCapacityError{Limit: limit, InFlight: count}
	}
	if s.active == nil {
		s.active = make(map[int64]int)
	}
	count++
	s.active[channelID] = count
	return &channelCapacityLease{store: s, channelID: channelID, limit: limit, acquiredCount: count}, nil
}
func (l *channelCapacityLease) Release() (int, bool) {
	if l == nil {
		return 0, false
	}
	s := l.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.released {
		return s.active[l.channelID], false
	}
	l.released = true
	count := s.active[l.channelID] - 1
	if count <= 0 {
		delete(s.active, l.channelID)
		return 0, true
	}
	s.active[l.channelID] = count
	return count, true
}

// One relay keeps its slot through body/stream processing and sequential compatibility
// Do calls. Retry backoff before the first Do does not occupy capacity.
type channelCapacityAttempt struct {
	store     *ChannelConcurrencyStore
	channelID int64
	limit     int
	lease     *channelCapacityLease
	routing   *routingTargetTrace
}
type channelCapacityContextKey struct{}

func (a *channelCapacityAttempt) acquire() error {
	if a == nil || a.store == nil || a.lease != nil {
		return nil
	}
	lease, err := a.store.Acquire(a.channelID, a.limit)
	if err != nil {
		return err
	}
	a.lease = lease
	a.routing.capacity("capacity_acquired", a.limit, lease.acquiredCount)
	return nil
}
func (a *channelCapacityAttempt) release() {
	if a == nil || a.lease == nil {
		return
	}
	if count, released := a.lease.Release(); released {
		a.routing.capacity("capacity_released", a.limit, count)
	}
}
