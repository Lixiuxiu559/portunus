package shared

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLogRequestsCompleteSummaryAndExactFilters(t *testing.T) {
	setupLogTest(t)
	first := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	last := first.Add(time.Minute)
	logs := []Log{
		{RequestID: "exact_%+'", APIKeyID: 7, GroupName: "g", ChannelID: 1, ModelName: "same", ClientProtocol: "openai", Status: 503, Success: false, CreatedAt: first},
		{RequestID: "exact_%+'", ChannelID: 2, Status: 200, Success: true, InputToken: 10, CreatedAt: last},
		// A backfill is the latest persisted log but not the latest timestamp.
		{RequestID: "exact_%+'", ChannelID: 3, Status: 200, Success: true, CreatedAt: first.Add(30 * time.Second)},
		{RequestID: "exact_XX+'", ChannelID: 1},
	}
	if err := LogDB.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	failed := false
	f := LogFilter{APIKeyID: 7, GroupName: "g", ChannelID: 1, ModelName: "same", ClientProtocol: "openai", RequestID: "exact_%+'", Success: &failed, StartTime: &first, EndTime: &first}
	rows, total, err := ListLogRequests(f)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("summary rows=%+v total=%d err=%v", rows, total, err)
	}
	got := rows[0]
	if got.Key != "request:exact_%+'" || got.RequestID != "exact_%+'" || got.LogCount != 3 || got.LatestLog.ID != logs[2].ID || !got.LatestLog.Success || got.LatestLog.ChannelID != 3 || !got.FirstAt.Equal(first) || !got.LastAt.Equal(last) {
		t.Fatalf("过滤只能命中关联项，摘要必须包含全部关联日志: %+v", got)
	}
	for _, requestID := range []string{"%", "exact_", "' OR 1=1 --"} {
		rows, total, err := ListLogRequests(LogFilter{RequestID: requestID})
		if err != nil || total != 0 || len(rows) != 0 || rows == nil {
			t.Fatalf("精确请求筛选 %q: rows=%+v total=%d err=%v", requestID, rows, total, err)
		}
	}
	// Old rows/stats keep their physical-log semantics and never count attempts.
	physical, count, err := ListLogs(LogFilter{RequestID: "exact_%+'"})
	stats, statsErr := LogStatsBy(LogFilter{RequestID: "exact_%+'"})
	if err != nil || statsErr != nil || count != 3 || len(physical) != 3 || stats.TotalRequests != 3 || stats.InputTokens != 10 {
		t.Fatalf("旧接口口径变化 count=%d stats=%+v err=%v/%v", count, stats, err, statsErr)
	}
}

func TestLogRequestsLegacyRowsAndGroupedPagination(t *testing.T) {
	setupLogTest(t)
	logs := make([]Log, 25)
	for i := range logs {
		logs[i] = Log{RequestID: "one-request", ModelName: fmt.Sprintf("model-%d", i)}
	}
	logs = append(logs, Log{}, Log{}, Log{RequestID: "log:27"}, Log{RequestID: "another"})
	if err := LogDB.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if err := LogDB.Model(&Log{}).Where("id = ?", logs[26].ID).UpdateColumn("request_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	var seen []LogRequest
	for page := 1; page <= 4; page++ {
		rows, total, err := ListLogRequests(LogFilter{Page: page, PageSize: 2})
		if err != nil || total != 5 {
			t.Fatalf("page=%d total=%d err=%v", page, total, err)
		}
		want := 2
		if page == 3 {
			want = 1
		}
		if page == 4 {
			want = 0
		}
		if len(rows) != want || rows == nil {
			t.Fatalf("page=%d rows=%+v", page, rows)
		}
		seen = append(seen, rows...)
	}
	for i, row := range seen {
		if i > 0 && seen[i-1].LatestLog.ID <= row.LatestLog.ID {
			t.Fatal("未按最新日志 ID 倒序")
		}
	}
	if seen[1].Key != "request:log:27" || seen[2].Key != "log:27" || seen[3].Key != "log:26" || seen[4].LogCount != 25 || seen[4].LatestLog.ID != logs[24].ID {
		t.Fatalf("历史空 ID、前缀碰撞或跨页拆分错误: %+v", seen)
	}
	for _, f := range []LogFilter{{Page: -1, PageSize: -1}, {PageSize: 999}} {
		rows, total, err := ListLogRequests(f)
		if err != nil || total != 5 || len(rows) != 5 {
			t.Fatalf("分页归一化错误 rows=%v total=%d err=%v", rows, total, err)
		}
	}
}

func TestLogRequestsBoundedQueries(t *testing.T) {
	setupLogTest(t)
	var logs []Log
	for i := 0; i < 100; i++ {
		logs = append(logs, Log{RequestID: fmt.Sprintf("request-%d", i)})
	}
	if err := LogDB.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	original := LogDB
	LogDB = LogDB.Session(&gorm.Session{Logger: requestQueryLogger{Interface: logger.Discard, count: &queries}})
	t.Cleanup(func() { LogDB = original })
	rows, total, err := ListLogRequests(LogFilter{PageSize: 100})
	if err != nil || total != 100 || len(rows) != 100 {
		t.Fatalf("rows=%d total=%d err=%v", len(rows), total, err)
	}
	if queries != 3 {
		t.Fatalf("应固定 3 次查询，无 N+1，得到 %d", queries)
	}
}

type requestQueryLogger struct {
	logger.Interface
	count *int
}

func (l requestQueryLogger) Trace(_ context.Context, _ time.Time, _ func() (string, int64), _ error) {
	*l.count++
}
