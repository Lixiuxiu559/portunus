package quota

import (
	"testing"
	"time"
)

func TestNewAPIPartialRateLimitPreservesSnapshotAndCooldown(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                         string
		wallet, subscriptions, plans *Failure
		seconds                      int64
	}{
		{"wallet", &Failure{Category: "rate_limited", RetryAfterSeconds: 120}, nil, nil, 120},
		{"subscriptions default", nil, &Failure{Category: "rate_limited"}, nil, 60},
		{"plans capped", nil, nil, &Failure{Category: "rate_limited", RetryAfterSeconds: 99999}, 3600},
		{"longest", &Failure{Category: "rate_limited", RetryAfterSeconds: 120}, nil, &Failure{Category: "rate_limited", RetryAfterSeconds: 300}, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{}
			plans := partStatus(tc.plans)
			snapshot := &Snapshot{NewAPI: &NewAPIStatus{Wallet: partStatus(tc.wallet), Subscriptions: partStatus(tc.subscriptions), Plans: &plans}}
			for _, automatic := range []int{0, 1} {
				src := Source{ID: 1, Enabled: true, ConfigRevision: 1, AutoRefreshMinutes: automatic}
				var st State
				s.applyResult(&st, src, snapshot, nil, now)
				if st.Snapshot != snapshot || !st.SnapshotUsable || st.Failure != nil || st.LastSuccessAt == nil || !st.LastSuccessAt.Equal(now) {
					t.Fatalf("lost partial snapshot: %+v", st)
				}
				want := now.Add(time.Duration(tc.seconds) * time.Second)
				if st.NextAllowedAt == nil || !st.NextAllowedAt.Equal(want) || due(src, st, now) {
					t.Fatalf("lost cooldown: %+v", st)
				}
				if automatic == 0 && st.NextScheduledAt != nil || automatic > 0 && (st.NextScheduledAt == nil || st.NextScheduledAt.Before(want) || !due(src, st, *st.NextScheduledAt)) {
					t.Fatalf("bad partial scheduling: %+v", st)
				}
				s.applyResult(&st, src, &Snapshot{NewAPI: &NewAPIStatus{Wallet: partStatus(nil), Subscriptions: partStatus(nil)}}, nil, want)
				if st.NextAllowedAt != nil || st.ConsecutiveFailures != 0 {
					t.Fatalf("recovery kept cooldown: %+v", st)
				}
			}
		})
	}
}
