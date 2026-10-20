package shared

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpstreamAttemptWriteIsolationAndUniqueness(t *testing.T) {
	setupLogTest(t)
	old := Log{RequestID: "legacy", Currency: "CNY", Cost: 3.25, InputToken: 19, OutputToken: 7, CacheReadToken: 4}
	if err := LogDB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []int{3, 1, 2} {
		row := UpstreamAttempt{RequestID: "r_%+'", Sequence: sequence, RelayAttempt: 1, Reason: "budget_compat", BudgetField: "max_tokens", ErrMsg: strings.Repeat("错误", 200), StartedAt: time.Now()}
		if err := WriteUpstreamAttempt(&row); err != nil {
			t.Fatal(err)
		}
		if len([]rune(row.ErrMsg)) != 400 {
			t.Fatal("writer 不应修改调用方对象")
		}
	}
	if err := WriteUpstreamAttempt(&UpstreamAttempt{RequestID: "r_%+'", Sequence: 1}); err == nil {
		t.Fatal("相同请求内的重复序号必须拒绝")
	}
	if err := WriteUpstreamAttempt(&UpstreamAttempt{RequestID: "another", Sequence: 1}); err != nil {
		t.Fatalf("不同请求应允许同一序号: %v", err)
	}
	rows, total, err := ListUpstreamAttempts("r_%+'", 2, 1)
	if err != nil || total != 3 || len(rows) != 1 || rows[0].Sequence != 2 || len([]rune(rows[0].ErrMsg)) != 256 {
		t.Fatalf("排序分页或错误截断错误 rows=%+v total=%d err=%v", rows, total, err)
	}
	rows, total, err = ListUpstreamAttempts("legacy", 1, 20)
	if err != nil || total != 0 || rows == nil || len(rows) != 0 {
		t.Fatalf("旧请求不可从 Log 推断尝试 rows=%+v total=%d err=%v", rows, total, err)
	}
	stats, err := LogStatsBy(LogFilter{})
	if err != nil || stats.TotalRequests != 1 || stats.InputTokens != 19 || stats.OutputTokens != 7 || stats.CacheReadTokens != 4 {
		t.Fatalf("独立尝试影响了旧统计: %+v err=%v", stats, err)
	}
	var preserved Log
	if err := LogDB.First(&preserved, old.ID).Error; err != nil || preserved.Currency != "CNY" || preserved.Cost != 3.25 {
		t.Fatalf("旧价格被改变: %+v err=%v", preserved, err)
	}
}

func TestUpstreamAttemptUsesSeparateLogDB(t *testing.T) {
	setupLogTest(t)
	cfg := &Config{}
	cfg.Database.LogPath = filepath.Join(t.TempDir(), "attempts.db")
	if err := InitLogDB(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := LogDB.DB(); err == nil {
			sqlDB.Close()
		}
	})
	row := UpstreamAttempt{RequestID: "isolated", Sequence: 1, StartedAt: time.Now()}
	if err := WriteUpstreamAttempt(&row); err != nil {
		t.Fatal(err)
	}
	var mainCount int64
	if err := DB.Model(&UpstreamAttempt{}).Count(&mainCount).Error; err != nil || mainCount != 0 {
		t.Fatalf("尝试被写入主库 count=%d err=%v", mainCount, err)
	}
	rows, total, err := ListUpstreamAttempts("isolated", 1, 20)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("独立日志库读写失败 rows=%+v total=%d err=%v", rows, total, err)
	}
}

func TestDeleteOldLogsAlsoCleansAttemptsByStartedAt(t *testing.T) {
	setupLogTest(t)
	boundary := time.Now().Add(-time.Hour)
	for i := 1; i <= 5; i++ {
		if err := WriteUpstreamAttempt(&UpstreamAttempt{RequestID: "old", Sequence: i, StartedAt: boundary.Add(-time.Minute), CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteUpstreamAttempt(&UpstreamAttempt{RequestID: "boundary", Sequence: 1, StartedAt: boundary, CreatedAt: boundary.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteOldLogs(boundary, 2)
	if err != nil || deleted != 0 {
		t.Fatalf("清理尝试不得增加旧返回计数 deleted=%d err=%v", deleted, err)
	}
	var remaining []UpstreamAttempt
	if err := LogDB.Find(&remaining).Error; err != nil || len(remaining) != 1 || remaining[0].RequestID != "boundary" {
		t.Fatalf("清理必须依据 started_at 且边界保留 rows=%+v err=%v", remaining, err)
	}
}
