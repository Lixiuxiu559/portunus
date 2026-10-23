package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

const maxRateLimitEntries = 16384

type rateLimitCooldownError struct {
	Scope      string
	RetryAfter int
}

func (*rateLimitCooldownError) Error() string { return "上游限流冷却中" }

type rateLimitKey struct {
	channelID   int64
	fingerprint [sha256.Size]byte
	model       string
	scope       string
}

// RateLimitStore is a bounded, process-local cooldown cache, not a hard RPM quota.
// It retains no plaintext connection credentials and never resets cooldown on success.
// Old in-flight snapshots can only update their own connection fingerprint.
type RateLimitStore struct {
	mu      sync.Mutex
	entries map[rateLimitKey]time.Time
	base    time.Duration
	max     time.Duration
	now     func() time.Time
}

func NewRateLimitStore(cfg shared.ProxyConfig) *RateLimitStore {
	base, max := cfg.RateLimitCooldownSeconds, cfg.RateLimitMaxCooldownSeconds
	if base <= 0 {
		base = 30
	}
	if max <= 0 {
		max = 300
	}
	if max < base {
		max = base
	}
	return &RateLimitStore{entries: make(map[rateLimitKey]time.Time), base: boundedDuration(base, time.Second), max: boundedDuration(max, time.Second), now: time.Now}
}

func rateLimitTargetKey(t router.Target) rateLimitKey {
	scope := t.Channel.RateLimitScope
	if scope != "channel" {
		scope = "model"
	}
	cfg := t.Channel.ConnectionConfig()
	cfg.Options = cfg.Options.Effective()
	if cfg.AdapterID == "" {
		cfg.AdapterID = "generic"
	}
	// The typed snapshot consists only of JSON scalar fields; Marshal cannot fail.
	raw, _ := json.Marshal(struct {
		Connection any    `json:"connection"`
		Scope      string `json:"scope"`
		Thinking   bool   `json:"thinking"`
	}{cfg, scope, t.Channel.ThinkingCompat})
	key := rateLimitKey{channelID: t.Channel.ID, fingerprint: sha256.Sum256(raw), scope: scope}
	if scope == "model" {
		key.model = t.Model.Name
	}
	return key
}

func (s *RateLimitStore) Check(t router.Target) *rateLimitCooldownError {
	if s == nil {
		return nil
	}
	key := rateLimitTargetKey(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var latest time.Time
	var scope string
	// A channel cooldown dominates all its models; a model cooldown only its name.
	for _, candidate := range []rateLimitKey{
		{channelID: key.channelID, fingerprint: key.fingerprint, scope: "channel"},
		{channelID: key.channelID, fingerprint: key.fingerprint, scope: "model", model: t.Model.Name},
	} {
		until, ok := s.entries[candidate]
		if !ok {
			continue
		}
		if !until.After(now) {
			delete(s.entries, candidate)
			continue
		}
		if until.After(latest) {
			latest, scope = until, candidate.scope
		}
	}
	if latest.IsZero() {
		return nil
	}
	wait := latest.Sub(now)
	seconds := wait / time.Second
	if wait%time.Second != 0 {
		seconds++
	}
	return &rateLimitCooldownError{Scope: scope, RetryAfter: int(seconds)}
}

func (s *RateLimitStore) Record(t router.Target, retryAfter string) {
	if s == nil {
		return
	}
	key := rateLimitTargetKey(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	until := now.Add(s.retryAfter(retryAfter, now))
	if current, ok := s.entries[key]; ok && current.After(until) {
		return
	}
	if !until.After(now) {
		delete(s.entries, key)
		return
	}
	if _, exists := s.entries[key]; !exists && len(s.entries) >= maxRateLimitEntries {
		s.makeRoom(now)
	}
	s.entries[key] = until
}

func (s *RateLimitStore) makeRoom(now time.Time) {
	var oldestKey rateLimitKey
	var oldest time.Time
	for key, until := range s.entries {
		if !until.After(now) {
			delete(s.entries, key)
			continue
		}
		if oldest.IsZero() || until.Before(oldest) {
			oldestKey, oldest = key, until
		}
	}
	if len(s.entries) >= maxRateLimitEntries {
		delete(s.entries, oldestKey)
	}
}

func (s *RateLimitStore) retryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return s.base
	}
	// Saturating decimal parsing accepts arbitrarily large valid delta-seconds.
	// Validate every byte even after saturation: "999999x" remains invalid.
	capSeconds := uint64(s.max / time.Second)
	var seconds uint64
	digits := true
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			digits = false
			break
		}
		digit := uint64(raw[i] - '0')
		if seconds > capSeconds/10 || (seconds == capSeconds/10 && digit > capSeconds%10) {
			seconds = capSeconds
		} else {
			seconds = seconds*10 + digit
		}
	}
	if digits {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(raw); err == nil && date.After(now) {
		wait := date.Sub(now)
		if wait > s.max {
			return s.max
		}
		return wait
	}
	return s.base
}
