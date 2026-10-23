package gateway

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func cooldownTarget() router.Target {
	return router.Target{Channel: channel.Channel{ID: 1, Type: protocol.ProviderOpenAI, BaseURL: "https://example.invalid", Key: "secret"}, Model: model.Model{ID: 1, Name: "upstream-a", ChannelID: 1}}
}

func cooldownClock() (*RateLimitStore, *time.Time) {
	s := NewRateLimitStore(shared.ProxyConfig{})
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestRateLimitStoreScopesAndAliases(t *testing.T) {
	for _, scope := range []string{"", "model", "channel"} {
		t.Run(scope, func(t *testing.T) {
			s, _ := cooldownClock()
			a := cooldownTarget()
			a.Channel.RateLimitScope = scope
			s.Record(a, "45")
			alias := a
			alias.Model.ID = 99 // Groups and local aliases are absent from the key.
			if got := s.Check(alias); got == nil || got.RetryAfter != 45 {
				t.Fatalf("alias lost cooldown: %+v", got)
			}
			b := a
			b.Model.Name = "upstream-b"
			if got := s.Check(b); (got != nil) != (scope == "channel") {
				t.Fatalf("scope %q propagated incorrectly: %+v", scope, got)
			}
			b = a
			b.Channel.ID++
			if s.Check(b) != nil {
				t.Fatal("cooldown leaked across channels")
			}
		})
	}
}

func TestRateLimitStoreConnectionFingerprint(t *testing.T) {
	s, _ := cooldownClock()
	a := cooldownTarget()
	s.Record(a, "60")
	for name, change := range map[string]func(*router.Target){
		"key":      func(v *router.Target) { v.Channel.Key = "new-secret" },
		"endpoint": func(v *router.Target) { v.Channel.BaseURL += "/v2" },
		"protocol": func(v *router.Target) { v.Channel.Type = protocol.ProviderAnthropic },
		"provider": func(v *router.Target) { v.Channel.ProviderID = "other" },
		"profile":  func(v *router.Target) { v.Channel.ProfileID = "other" },
		"adapter":  func(v *router.Target) { v.Channel.AdapterID = "other" },
		"options":  func(v *router.Target) { v.Channel.ConnectionOptions.ModelDiscovery = "manual" },
		"scope":    func(v *router.Target) { v.Channel.RateLimitScope = "channel" },
		"thinking": func(v *router.Target) { v.Channel.ThinkingCompat = true },
	} {
		t.Run(name, func(t *testing.T) {
			b := a
			change(&b)
			if s.Check(b) != nil {
				t.Fatal("new configuration inherited old cooldown")
			}
			s.Record(b, "90")
			s.Record(a, "300") // A late response from the old revision cannot overwrite b.
			if got := s.Check(b); got == nil || got.RetryAfter != 90 {
				t.Fatalf("old revision affected new revision: %+v", got)
			}
		})
	}
	b := a
	b.Channel.Name, b.Channel.Icon = "renamed", "icon"
	b.Channel.UpdatedAt = time.Now()
	b.Channel.AutoSync = true
	b.Channel.AdapterID = "generic"
	b.Channel.ConnectionOptions = b.Channel.ConnectionOptions.Effective()
	b.Channel.RateLimitScope = "model"
	if s.Check(b) == nil {
		t.Fatal("display/equivalent default changes reset cooldown")
	}
}

func TestRateLimitStoreRetryAfter(t *testing.T) {
	s, now := cooldownClock()
	cases := []struct {
		name, header string
		want         time.Duration
	}{
		{"missing", "", 30 * time.Second},
		{"zero", "0", 0},
		{"decimal", "005", 5 * time.Second},
		{"trim", " 12 ", 12 * time.Second},
		{"negative", "-1", 30 * time.Second},
		{"plus", "+1", 30 * time.Second},
		{"fraction", "0.5", 30 * time.Second},
		{"overflow", strings.Repeat("9", 100), 300 * time.Second},
		{"overflow malformed", strings.Repeat("9", 100) + "x", 30 * time.Second},
		{"past date", now.Add(-time.Second).Format(http.TimeFormat), 30 * time.Second},
		{"now date", now.Format(http.TimeFormat), 30 * time.Second},
		{"future date", now.Add(45 * time.Second).Format(http.TimeFormat), 45 * time.Second},
		{"capped date", now.Add(time.Hour).Format(http.TimeFormat), 300 * time.Second},
		{"invalid", "tomorrow", 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.retryAfter(tc.header, *now); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	// HTTP dates have second precision; fractional time must not round down waiting.
	*now = now.Add(250 * time.Millisecond)
	s.Record(cooldownTarget(), now.Add(time.Second).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"))
	if got := s.Check(cooldownTarget()); got == nil || got.RetryAfter != 1 {
		t.Fatalf("fractional retry wait: %+v", got)
	}
}

func TestRateLimitStoreExtendsExpiresAndSelectsLatest(t *testing.T) {
	s, now := cooldownClock()
	a := cooldownTarget()
	s.Record(a, "60")
	*now = now.Add(time.Second)
	s.Record(a, "1")
	s.Record(a, "0")
	if got := s.Check(a); got == nil || got.RetryAfter != 59 {
		t.Fatalf("cooldown shortened: %+v", got)
	}
	key := rateLimitTargetKey(a)
	channelKey := key
	channelKey.model, channelKey.scope = "", "channel"
	s.entries[channelKey] = now.Add(70 * time.Second)
	if got := s.Check(a); got == nil || got.Scope != "channel" || got.RetryAfter != 70 {
		t.Fatalf("did not select later channel cooldown: %+v", got)
	}
	*now = now.Add(70 * time.Second)
	if s.Check(a) != nil || len(s.entries) != 0 {
		t.Fatal("expired entries retained or checks extended cooldown")
	}
	s.Record(a, "0")
	if s.Check(a) != nil || len(s.entries) != 0 {
		t.Fatal("zero seconds created cooldown")
	}
	s.Record(a, "10")
	*now = now.Add(time.Second)
	s.Record(a, "20")
	if got := s.Check(a); got == nil || got.RetryAfter != 20 {
		t.Fatalf("later 429 did not extend cooldown: %+v", got)
	}
}

func TestRateLimitStoreBoundedAndConcurrent(t *testing.T) {
	s, now := cooldownClock()
	a := cooldownTarget()
	for i := 0; i < maxRateLimitEntries; i++ {
		v := a
		v.Model.Name = fmt.Sprint(i)
		s.Record(v, "60")
	}
	s.Record(a, "120")
	if len(s.entries) != maxRateLimitEntries || s.Check(a) == nil {
		t.Fatalf("cache bound/replacement failed: %d", len(s.entries))
	}
	*now = now.Add(61 * time.Second)
	v := a
	v.Model.Name = "after-expiry"
	s.Record(v, "120") // Full-cache sweep deletes expired entries.
	if len(s.entries) != 2 {
		t.Fatalf("expired entries were not swept: %d", len(s.entries))
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Record(a, "10")
				if s.Check(a) == nil {
					t.Error("concurrent check lost cooldown")
				}
			}
		}()
	}
	wg.Wait()
}

func TestRateLimitStoreConfigFallbackAndOverflow(t *testing.T) {
	for _, cfg := range []shared.ProxyConfig{{}, {RateLimitCooldownSeconds: -1, RateLimitMaxCooldownSeconds: -1}} {
		s := NewRateLimitStore(cfg)
		if s.base != 30*time.Second || s.max != 300*time.Second {
			t.Fatalf("bad default: %v %v", s.base, s.max)
		}
	}
	s := NewRateLimitStore(shared.ProxyConfig{RateLimitCooldownSeconds: 40, RateLimitMaxCooldownSeconds: 5})
	if s.base != s.max || s.max != 40*time.Second {
		t.Fatalf("max below base: %v %v", s.base, s.max)
	}
	s = NewRateLimitStore(shared.ProxyConfig{RateLimitCooldownSeconds: math.MaxInt, RateLimitMaxCooldownSeconds: math.MaxInt})
	if s.base <= 0 || s.max <= 0 || s.retryAfter(strings.Repeat("9", 50), time.Now()) <= 0 {
		t.Fatal("duration overflow")
	}
	if strings.Contains((&rateLimitCooldownError{Scope: "secret", RetryAfter: 5}).Error(), "secret") {
		t.Fatal("error exposes data")
	}
}
