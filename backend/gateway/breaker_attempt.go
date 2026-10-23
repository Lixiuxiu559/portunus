package gateway

// breakerAttempt owns one relay's health permit, including sequential compatibility
// requests. No permit is acquired until an actual HTTP send is ready to begin.
type breakerAttempt struct {
	store   *BreakerStore
	modelID int64
	lease   *breakerLease
	routing *routingTargetTrace
}

type breakerAttemptContextKey struct{}

func (a *breakerAttempt) check() error {
	if a != nil && a.lease != nil {
		return a.lease.Check()
	}
	return nil
}

func (a *breakerAttempt) acquire() error {
	if a == nil || a.store == nil {
		return nil
	}
	if a.lease != nil {
		return a.lease.Check()
	}
	lease, err := a.store.Acquire(a.modelID)
	if err != nil {
		return err
	}
	a.lease = lease
	if lease.probe {
		a.routing.event("probe_acquired", "half_open", nil)
	}
	return nil
}

func (a *breakerAttempt) finish(success bool, d failDecision) {
	if a == nil || a.lease == nil {
		return
	}
	outcome := breakerIgnored
	if success {
		outcome = breakerSucceeded
	} else if d.BreakerFail {
		outcome = breakerFailed
		if d.StallNoHeader {
			outcome = breakerStalled
		}
	}
	applied := a.lease.Finish(outcome)
	if a.lease.probe {
		reason := "ignored"
		switch outcome {
		case breakerSucceeded:
			reason = "succeeded"
		case breakerFailed:
			reason = "failed"
		case breakerStalled:
			reason = "stalled"
		}
		if !applied {
			reason = "stale_lease"
		}
		a.routing.event("probe_finished", reason, nil)
	}
}
