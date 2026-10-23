package gateway

import (
	"errors"
	"log"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
)

// routingDiagnostics is owned by the handler goroutine. It never stores payloads,
// upstream messages, URLs, credentials or client-supplied dynamic field names.
// A completed trace describes gateway routing, not receipt of a complete client response.
type routingDiagnostics struct {
	row         shared.RoutingTrace
	lastErr     error
	lastSuccess bool
}

type routingTargetTrace struct {
	diagnostics *routingDiagnostics
	template    shared.RoutingEvent
}

func newRoutingDiagnostics(c *gin.Context, requestID string, p protocol.Provider) *routingDiagnostics {
	return &routingDiagnostics{row: shared.RoutingTrace{RequestID: requestID, SchemaVersion: 1,
		APIKeyID: c.GetInt64("api_key_id"), ClientProtocol: string(p), StartedAt: time.Now(),
		Events: make([]shared.RoutingEvent, 0), StopReason: "handler_interrupted", Outcome: "interrupted"}}
}

func (d *routingDiagnostics) add(e shared.RoutingEvent) {
	e.Sequence = len(d.row.Events) + d.row.DroppedEvents + 1
	if len(d.row.Events) >= shared.MaxRoutingEvents {
		d.row.DroppedEvents++
		return
	}
	d.row.Events = append(d.row.Events, e)
}

func (d *routingDiagnostics) target(t router.Target, candidate, relay int) *routingTargetTrace {
	return &routingTargetTrace{diagnostics: d, template: shared.RoutingEvent{Candidate: candidate,
		RelayAttempt: relay, ChannelID: t.Channel.ID, ModelID: t.Model.ID}}
}

func (t *routingTargetTrace) event(kind, reason string, err error) {
	if t == nil {
		return
	}
	e := t.template
	e.Event, e.Reason = kind, reason
	if err != nil {
		e.ErrKind = failSpec(err).ErrKind
		var cooling *rateLimitCooldownError
		if errors.As(err, &cooling) {
			e.Scope, e.RetryAfter = cooling.Scope, cooling.RetryAfter
		}
		var capacity *channelCapacityError
		if errors.As(err, &capacity) {
			limit, count := capacity.Limit, capacity.InFlight
			e.CapacityLimit, e.InFlight = &limit, &count
		}
		var unavailable *circuitUnavailableError
		if errors.As(err, &unavailable) {
			e.RetryAfter = unavailable.RetryAfter
		}
	}
	e.Probe = kind == "probe_acquired" || kind == "probe_finished"
	t.diagnostics.add(e)
}

func routingAdmissionReason(err error) string {
	var unavailable *circuitUnavailableError
	if errors.As(err, &unavailable) {
		return unavailable.Reason
	}
	return failSpec(err).ErrKind
}

func (t *routingTargetTrace) blocked(err error) {
	if err != nil {
		t.event("admission_blocked", routingAdmissionReason(err), err)
	}
}

func (t *routingTargetTrace) finished(success bool, err error, started bool) {
	if t == nil {
		return
	}
	d := t.diagnostics
	d.lastErr, d.lastSuccess = err, success
	d.row.DeliveryStarted = d.row.DeliveryStarted || started
	reason := "failed"
	if success {
		reason = "succeeded"
	}
	t.event("relay_finished", reason, err)
}

func routingStopReason(err error) string {
	var committed *streamCommittedError
	if errors.As(err, &committed) {
		return "committed_failure"
	}
	// Hard stops remain the primary reason, even inside an unsafe wrapper.
	d := failSpec(err)
	if d.StopRequest && d.ErrKind != "" {
		switch d.ErrKind {
		case shared.ErrKindClientCancel, shared.ErrKindResourceLimit, shared.ErrKindAttemptBudget,
			shared.ErrKindNonStreamDeadline, shared.ErrKindFirstContentDeadline:
			return d.ErrKind
		}
	}
	var unsafe *streamReplayUnsafeError
	if errors.As(err, &unsafe) {
		return "replay_unsafe"
	}
	if d.StopRequest {
		if d.ErrKind == shared.ErrKindConvert {
			return "conversion_safety_stop"
		}
		return "request_stopped"
	}
	return "candidates_exhausted"
}

func (d *routingDiagnostics) stop(reason string, err error, outcome string) {
	d.row.StopReason, d.row.StopErrKind, d.row.Outcome = reason, failSpec(err).ErrKind, outcome
}

func (d *routingDiagnostics) flush(s *relayServer, c *gin.Context) {
	d.row.Status = c.Writer.Status()
	d.row.FinishedAt = time.Now()
	if s.deps.RoutingWrite != nil {
		if err := s.deps.RoutingWrite(&d.row); err != nil {
			// Do not log driver errors: SQL errors may include serialized parameters.
			log.Printf("切换诊断写入失败: request_id=%s", d.row.RequestID)
		}
	}
}

func (t *routingTargetTrace) capacity(kind string, limit, count int) {
	if t == nil {
		return
	}
	e := t.template
	e.Event, e.Reason = kind, kind
	e.CapacityLimit, e.InFlight = &limit, &count
	t.diagnostics.add(e)
}
