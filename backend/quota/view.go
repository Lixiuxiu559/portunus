package quota

import "time"

func makeView(s Source, st State, now time.Time, busy bool) View {
	v := View{Status: "never", InProgress: busy, LastAttemptAt: st.LastAttemptAt, LastSuccessAt: st.LastSuccessAt, Failure: st.Failure, NextAllowedAt: st.NextAllowedAt, LastSnapshot: st.Snapshot}
	if !s.Enabled {
		v.Status = "disabled"
		return v
	}
	if st.ConfigRevision != s.ConfigRevision {
		return View{Status: "invalidated", InProgress: busy}
	}
	if st.Failure != nil {
		v.Status = "unavailable"
	}
	if st.Snapshot != nil && st.SnapshotUsable && st.LastSuccessAt != nil {
		fresh := 5 * time.Minute
		if s.AutoRefreshMinutes > 0 {
			fresh = time.Duration(s.AutoRefreshMinutes) * time.Minute
		}
		maxAge := 2 * fresh
		if maxAge < 10*time.Minute {
			maxAge = 10 * time.Minute
		}
		age := now.Sub(*st.LastSuccessAt)
		// Freshness describes when we queried, not whether the upstream balance expired.
		// Bound fallback values after a failed query; an idle successful query remains
		// visible with its original timestamp until configuration/identity invalidates it.
		failed := st.Failure != nil && st.Failure.Category != "cancelled"
		if !failed || age <= maxAge {
			v.Snapshot = st.Snapshot
			if failed || age > fresh {
				v.Status = "stale"
			} else {
				v.Status = "fresh"
			}
		} else {
			v.Status = "expired"
		}
	}
	return v
}
func sourceView(s Source, st State, key string, now time.Time, busy bool) SourceView {
	return SourceView{ID: s.ID, ChannelID: s.ChannelID, Name: s.Name, AdapterID: s.AdapterID, SchemaVersion: s.SchemaVersion, ConfigRevision: s.ConfigRevision, Enabled: s.Enabled, QueryBaseURL: s.QueryBaseURL, CredentialMode: s.CredentialMode, HasToken: resolvedSource(s, key).Token != "", Options: s.Options, AutoRefreshMinutes: s.AutoRefreshMinutes, State: makeView(s, st, now, busy)}
}
