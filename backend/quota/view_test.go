package quota

import (
	"testing"
	"time"
)

func TestViewQueryAgeDoesNotExpireSuccessfulBalance(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		age      time.Duration
		interval int
		failure  *Failure
		usable   bool
		revision int64
		enabled  bool
		status   string
		show     bool
	}{
		{"recent", time.Minute, 0, nil, true, 1, true, "fresh", true},
		{"idle manual query", 11 * time.Minute, 0, nil, true, 1, true, "stale", true},
		{"yesterday successful query", 24 * time.Hour, 0, nil, true, 1, true, "stale", true},
		{"scheduled query delayed", 3 * time.Hour, 60, nil, true, 1, true, "stale", true},
		{"cancel is not a failed balance", 24 * time.Hour, 0, &Failure{Category: "cancelled"}, true, 1, true, "stale", true},
		{"temporary failure within window", 9 * time.Minute, 0, &Failure{Category: "temporary"}, true, 1, true, "stale", true},
		{"temporary failure exceeds window", 11 * time.Minute, 0, &Failure{Category: "temporary"}, true, 1, true, "expired", false},
		{"rate limit exceeds window", 11 * time.Minute, 0, &Failure{Category: "rate_limited"}, true, 1, true, "expired", false},
		{"auth no old identity", time.Minute, 0, &Failure{Category: "authentication"}, false, 1, true, "unavailable", false},
		{"auth followed by network failure", time.Minute, 0, &Failure{Category: "temporary"}, false, 1, true, "unavailable", false},
		{"changed identity", time.Minute, 0, nil, true, 2, true, "invalidated", false},
		{"disabled", time.Minute, 0, nil, true, 1, false, "disabled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(-tc.age)
			value := "0"
			snap := &Snapshot{Items: []Item{{ID: "wallet", Remaining: &value}}}
			view := makeView(Source{Enabled: tc.enabled, ConfigRevision: 1, AutoRefreshMinutes: tc.interval}, State{ConfigRevision: tc.revision, LastSuccessAt: &at, Snapshot: snap, SnapshotUsable: tc.usable, Failure: tc.failure}, now, false)
			if view.Status != tc.status || (view.Snapshot != nil) != tc.show {
				t.Fatalf("status=%s snapshot=%v", view.Status, view.Snapshot != nil)
			}
			if tc.show && (!view.LastSuccessAt.Equal(at) || *view.Snapshot.Items[0].Remaining != "0") {
				t.Fatal("display fabricated a new timestamp or lost zero")
			}
		})
	}
}
