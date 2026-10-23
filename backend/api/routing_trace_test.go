package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/gin-gonic/gin"
)

func TestRoutingTraceRoute(t *testing.T) {
	setupCascadeTest(t)
	if err := shared.InitLogDB(&shared.Config{}); err != nil {
		t.Fatal(err)
	}
	const identity = "route_%+'"
	if err := shared.WriteRoutingTrace(&shared.RoutingTrace{RequestID: identity, SchemaVersion: 1,
		Strategy: "manual", CandidateCount: 1, StopReason: "all_candidates_unavailable", Status: 503,
		Events: []shared.RoutingEvent{{Sequence: 1, Candidate: 1, Event: "candidate_skipped", Reason: "probe_busy"}},
	}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerLogRoutes(r.Group("/api"))
	for _, tc := range []struct {
		identity string
		status   int
		found    bool
	}{
		{"", 400, false}, {" \t", 400, false}, {identity, 200, true},
		{"route_", 200, false}, {"%", 200, false}, {"route_XX+'", 200, false}, {" " + identity, 200, false},
	} {
		t.Run(tc.identity, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/logs/routing?"+url.Values{"request_id": {tc.identity}}.Encode(), nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status != 200 {
				return
			}
			var body struct {
				Data *shared.RoutingTrace `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if (body.Data != nil) != tc.found {
				t.Fatalf("found=%v body=%s", tc.found, w.Body.String())
			}
			if !tc.found && w.Body.String() != `{"data":null}` {
				t.Fatalf("missing must explicitly return null: %s", w.Body.String())
			}
			if tc.found && (body.Data.RequestID != identity || body.Data.HTTPAttempts != 0 || len(body.Data.Events) != 1 || body.Data.Events[0].Reason != "probe_busy") {
				t.Fatalf("unexpected trace: %s", w.Body.String())
			}
		})
	}
}
