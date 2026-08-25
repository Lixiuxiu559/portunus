package shared

import (
	"math"
	"path/filepath"
	"testing"
)

func setupLogTest(t *testing.T) {
	t.Helper()
	if DB != nil {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	}
	cfg := &Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if _, err := InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	if err := AutoMigrate(&Log{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

func TestListLogsFilterAndPagination(t *testing.T) {
	setupLogTest(t)

	for i := 0; i < 3; i++ {
		DB.Create(&Log{GroupName: "g1", ChannelID: 1, ModelName: "a", Success: true, InputToken: 10, OutputToken: 5, Cost: 0.1, Status: 200})
	}
	DB.Create(&Log{GroupName: "g1", ChannelID: 1, ModelName: "b", Success: true, InputToken: 20, OutputToken: 10, Cost: 0.2, Status: 200})
	DB.Create(&Log{GroupName: "g1", ChannelID: 1, ModelName: "b", Success: false, InputToken: 0, OutputToken: 0, Cost: 0, Status: 500})

	if logs, total, err := ListLogs(LogFilter{ModelName: "b", Page: 1, PageSize: 20}); err != nil || total != 2 || len(logs) != 2 {
		t.Errorf("model=b 应返回 2 条, got total=%d len=%d err=%v", total, len(logs), err)
	}

	successFalse := false
	if logs, total, _ := ListLogs(LogFilter{Success: &successFalse, Page: 1, PageSize: 20}); total != 1 || len(logs) != 1 {
		t.Errorf("success=false 应返回 1 条, got total=%d len=%d", total, len(logs))
	}

	if logs, total, _ := ListLogs(LogFilter{Page: 1, PageSize: 2}); total != 5 || len(logs) != 2 {
		t.Errorf("page1 size2 应 total=5 len=2, got total=%d len=%d", total, len(logs))
	}
	if logs, _, _ := ListLogs(LogFilter{Page: 3, PageSize: 2}); len(logs) != 1 {
		t.Errorf("page3 size2 应 len=1, got len=%d", len(logs))
	}
}

func TestLogStatsBy(t *testing.T) {
	setupLogTest(t)
	DB.Create(&Log{GroupName: "g1", ModelName: "a", Success: true, InputToken: 10, OutputToken: 5, Cost: 0.1, Status: 200})
	DB.Create(&Log{GroupName: "g1", ModelName: "a", Success: true, InputToken: 20, OutputToken: 10, Cost: 0.2, Status: 200})

	s, err := LogStatsBy(LogFilter{})
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if s.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", s.TotalRequests)
	}
	if s.InputTokens != 30 || s.OutputTokens != 15 {
		t.Errorf("token 统计不对: in=%d out=%d", s.InputTokens, s.OutputTokens)
	}
	if math.Abs(s.TotalCost-0.3) > 1e-9 {
		t.Errorf("TotalCost = %f, want 0.3", s.TotalCost)
	}
	if s.RecentRequests != 2 {
		t.Errorf("RecentRequests = %d, want 2", s.RecentRequests)
	}
}
