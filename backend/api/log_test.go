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

func TestLogRoutesRequestIDFilter(t *testing.T) {
	setupCascadeTest(t)
	if err := shared.InitLogDB(&shared.Config{}); err != nil {
		t.Fatal(err)
	}
	rows := []shared.Log{
		{RequestID: "same_%+request", ChannelID: 1, GroupName: "g", ModelName: "same", InputToken: 2},
		{RequestID: "same_%+request", ChannelID: 1, GroupName: "g", ModelName: "same", InputToken: 3},
		{RequestID: "same_%+request", ChannelID: 2, GroupName: "g", ModelName: "same", InputToken: 5},
		{RequestID: "same_%+request", ChannelID: 1, GroupName: "other", ModelName: "same", InputToken: 7},
		{RequestID: "same_XX+request", ChannelID: 1, GroupName: "g", ModelName: "same", InputToken: 11},
		{ChannelID: 1, GroupName: "g", ModelName: "same", InputToken: 13},
	}
	if err := shared.LogDB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerLogRoutes(r.Group("/api"))
	get := func(path string, query url.Values, target any) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?"+query.Encode(), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, requestID, channel, group string
		total, input                    int64
		wantID                          int64
	}{
		{"combined", "same_%+request", "1", "g", 2, 5, rows[0].ID},
		{"cross-channel", "same_%+request", "", "", 4, 17, rows[2].ID},
		{"prefix", "same_", "", "", 0, 0, 0},
		{"percent", "%", "", "", 0, 0, 0},
		{"unmatched", "missing", "", "", 0, 0, 0},
		{"empty-keeps-history", "", "", "", 6, 41, rows[4].ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"request_id": {tc.requestID}, "channel_id": {tc.channel}, "group_name": {tc.group}, "model_name": {"same"}, "page": {"2"}, "page_size": {"1"}}
			var list struct {
				Total int64        `json:"total"`
				Data  []shared.Log `json:"data"`
			}
			get("/api/logs", query, &list)
			if list.Total != tc.total || (tc.total == 0 && len(list.Data) != 0) || (tc.total > 0 && (len(list.Data) != 1 || list.Data[0].ID != tc.wantID)) {
				t.Fatalf("API 筛选分页错误: %+v", list)
			}
			var stats shared.LogStats
			get("/api/logs/stat", query, &stats)
			if stats.TotalRequests != tc.total || stats.InputTokens != tc.input || stats.RecentRequests != tc.total {
				t.Fatalf("API 统计筛选错误: %+v", stats)
			}
		})
	}
}

func TestUpstreamAttemptRoutes(t *testing.T) {
	setupCascadeTest(t)
	if err := shared.InitLogDB(&shared.Config{}); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []int{3, 1, 2} {
		if err := shared.WriteUpstreamAttempt(&shared.UpstreamAttempt{RequestID: "exact_%+'", Sequence: sequence}); err != nil {
			t.Fatal(err)
		}
	}
	if err := shared.WriteUpstreamAttempt(&shared.UpstreamAttempt{RequestID: "exact_XX+'", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	registerLogRoutes(r.Group("/api"))
	for _, tc := range []struct {
		name, requestID, page, size string
		status                      int
		total                       int64
		sequence                    int
	}{
		{"second-page", "exact_%+'", "2", "1", 200, 3, 2},
		{"first-page", "exact_%+'", "1", "1", 200, 3, 1},
		{"last-page", "exact_%+'", "3", "1", 200, 3, 3},
		{"out-of-range", "exact_%+'", "4", "1", 200, 3, 0},
		{"literal-percent", "%", "1", "20", 200, 0, 0},
		{"prefix", "exact_", "1", "20", 200, 0, 0},
		{"historical", "old-request", "1", "20", 200, 0, 0},
		{"empty", "", "1", "20", 400, 0, 0},
		{"blank", "  ", "1", "20", 400, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"request_id": {tc.requestID}, "page": {tc.page}, "page_size": {tc.size}}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/logs/attempts?"+query.Encode(), nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			var response struct {
				Total int64                    `json:"total"`
				Data  []shared.UpstreamAttempt `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Total != tc.total || response.Data == nil || (tc.sequence == 0 && len(response.Data) != 0) || (tc.sequence > 0 && (len(response.Data) != 1 || response.Data[0].Sequence != tc.sequence)) {
				t.Fatalf("精确查询/分页/数组格式错误: %+v", response)
			}
		})
	}
}
