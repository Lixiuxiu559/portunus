package shared

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestRoutingTracePersistenceAndExactIdentity(t *testing.T) {
	setupLogTest(t)
	start := time.Now().UTC().Truncate(time.Millisecond)
	want := RoutingTrace{RequestID: "request_%+'", SchemaVersion: 1, APIKeyID: 3, GroupID: 5,
		Strategy: "failover", ClientProtocol: "openai", Stream: true, CandidateCount: 2,
		Events: []RoutingEvent{
			{Sequence: 1, Candidate: 1, ChannelID: 11, ModelID: 22, Event: "candidate_skipped", Reason: "probe_busy", ErrKind: "circuit_open", RetryAfter: 1},
			{Sequence: 2, Candidate: 2, RelayAttempt: 1, ChannelID: 33, ModelID: 44, Event: "candidate_selected", Reason: "backup", Probe: true},
		}, StopReason: "succeeded", Status: 200, Outcome: "success", DeliveryStarted: true,
		HTTPAttempts: 1, StartedAt: start, FinishedAt: start.Add(time.Second)}
	if err := WriteRoutingTrace(&want); err != nil {
		t.Fatal(err)
	}
	if want.ID != 0 {
		t.Fatal("writer mutated input")
	}
	got, err := GetRoutingTrace(want.RequestID)
	if err != nil || got == nil {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	got.ID = 0
	if !reflect.DeepEqual(got, &want) {
		t.Fatalf("roundtrip=%+v want=%+v", got, want)
	}
	for _, id := range []string{"request_", "%", "request_XX+'", " " + want.RequestID} {
		row, err := GetRoutingTrace(id)
		if err != nil || row != nil {
			t.Fatalf("identity %q row=%+v err=%v", id, row, err)
		}
	}
	if err := WriteRoutingTrace(&want); err == nil {
		t.Fatal("duplicate request must not create second trace")
	}
	stats, err := LogStatsBy(LogFilter{})
	if err != nil || stats.TotalRequests != 0 {
		t.Fatalf("trace polluted usage statistics: %+v err=%v", stats, err)
	}
	_, attempts, err := ListUpstreamAttempts(want.RequestID, 1, 20)
	if err != nil || attempts != 0 {
		t.Fatalf("trace manufactured attempts=%d err=%v", attempts, err)
	}
}

func TestRoutingTraceBoundsAndEmptyEvents(t *testing.T) {
	setupLogTest(t)
	trace := RoutingTrace{RequestID: "bounded", Events: make([]RoutingEvent, MaxRoutingEvents+7), DroppedEvents: 3}
	for i := range trace.Events {
		trace.Events[i].Sequence = i + 1
	}
	if err := WriteRoutingTrace(&trace); err != nil {
		t.Fatal(err)
	}
	row, err := GetRoutingTrace(trace.RequestID)
	if err != nil || len(row.Events) != MaxRoutingEvents || row.DroppedEvents != 10 || row.Events[MaxRoutingEvents-1].Sequence != MaxRoutingEvents {
		t.Fatalf("bounded row=%+v err=%v", row, err)
	}
	if len(trace.Events) != MaxRoutingEvents+7 || trace.DroppedEvents != 3 {
		t.Fatal("writer changed source trace")
	}
	if err := WriteRoutingTrace(&RoutingTrace{RequestID: "empty"}); err != nil {
		t.Fatal(err)
	}
	empty, err := GetRoutingTrace("empty")
	if err != nil || empty.Events == nil || len(empty.Events) != 0 {
		t.Fatalf("empty events=%+v err=%v", empty, err)
	}
}

func TestDeleteOldLogsAlsoDeletesRoutingTraces(t *testing.T) {
	setupLogTest(t)
	cutoff := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		if err := WriteRoutingTrace(&RoutingTrace{RequestID: fmt.Sprint(i), StartedAt: cutoff.Add(-time.Second), FinishedAt: cutoff.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteRoutingTrace(&RoutingTrace{RequestID: "boundary", StartedAt: cutoff}); err != nil {
		t.Fatal(err)
	}
	if err := LogDB.Create(&Log{CreatedAt: cutoff.Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteOldLogs(cutoff, 2)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	var traces []RoutingTrace
	if err := LogDB.Find(&traces).Error; err != nil || len(traces) != 1 || traces[0].RequestID != "boundary" {
		t.Fatalf("remaining=%+v err=%v", traces, err)
	}
}

func TestDeleteOldLogsWithoutRoutingTable(t *testing.T) {
	setupLogTest(t)
	if err := LogDB.Migrator().DropTable(&RoutingTrace{}); err != nil {
		t.Fatal(err)
	}
	if _, err := DeleteOldLogs(time.Now(), 1); err != nil {
		t.Fatalf("legacy cleanup failed: %v", err)
	}
}
