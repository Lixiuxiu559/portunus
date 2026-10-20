package shared

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
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
	if err := InitLogDB(cfg); err != nil {
		t.Fatalf("初始化日志库失败: %v", err)
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
		LogDB.Create(&Log{GroupName: "g1", ChannelID: 1, ModelName: "a", Success: true, InputToken: 10, OutputToken: 5, Cost: 0.1, Status: 200})
	}
	LogDB.Create(&Log{GroupName: "g1", ChannelID: 1, ModelName: "b", Success: true, InputToken: 20, OutputToken: 10, Cost: 0.2, Status: 200})
	LogDB.Create(&Log{GroupName: "g1", ChannelID: 1, ModelName: "b", Success: false, InputToken: 0, OutputToken: 0, Cost: 0, Status: 500})

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
	LogDB.Create(&Log{GroupName: "g1", ModelName: "a", Success: true, InputToken: 10, OutputToken: 5, Cost: 0.1, Status: 200})
	LogDB.Create(&Log{GroupName: "g1", ModelName: "a", Success: true, InputToken: 20, OutputToken: 10, Cost: 0.2, Status: 200})

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
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"total_cost_usd", "total_cost_cny", "cost", "currency"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("统计响应不应包含退役字段 %s", key)
		}
	}
	if s.RecentRequests != 2 {
		t.Errorf("RecentRequests = %d, want 2", s.RecentRequests)
	}
}

// TestLogAutoMigrateAddsDiagnosticColumns 验证**存量库升级路径**：改动前建的 logs
// 表只有旧列，重启时 InitLogDB 的 AutoMigrate 必须补上新列且不丢存量行。
// 这是发布风险点——迁移失败会让已有用户的日志页直接不可用。
func TestLogAutoMigrateAddsDiagnosticColumns(t *testing.T) {
	cfg := &Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "old.db")
	if _, err := InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	})

	// 模拟改动前的存量表：仅旧列，且已有一行历史数据
	if err := DB.Exec(`CREATE TABLE logs (
		id integer primary key autoincrement,
		api_key_id integer, group_name text, channel_id integer, model_name text,
		currency text, status integer, success numeric, stream numeric,
		request_id text, input_token integer, output_token integer,
		cache_read_token integer, cache_write_token integer, cost real,
		duration_ms integer, first_token_ms integer, err_kind text, err_msg text,
		created_at datetime)`).Error; err != nil {
		t.Fatalf("建存量表失败: %v", err)
	}
	if err := DB.Exec(`INSERT INTO logs (group_name, model_name, status, success, created_at) VALUES ('g','m',200,1,?)`,
		time.Now()).Error; err != nil {
		t.Fatalf("插存量行失败: %v", err)
	}

	// 重启路径：InitLogDB 应补列
	if err := InitLogDB(cfg); err != nil {
		t.Fatalf("存量库迁移失败: %v", err)
	}

	var n int64
	if err := LogDB.Model(&Log{}).Count(&n).Error; err != nil {
		t.Fatalf("迁移后查询失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("迁移后存量行数 = %d, want 1（存量数据丢了）", n)
	}

	// 新列可写可筛
	if err := LogDB.Create(&Log{GroupName: "g", ModelName: "m", Success: true, Status: 200,
		ClientProtocol: "anthropic", BridgeDecoded: 5, ShimRewrites: 1}).Error; err != nil {
		t.Fatalf("写入遥测失败: %v", err)
	}
	logs, total, err := ListLogs(LogFilter{ClientProtocol: "anthropic", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("按 client_protocol 查询失败: %v", err)
	}
	if total != 1 || len(logs) != 1 || logs[0].BridgeDecoded != 5 || logs[0].ShimRewrites != 1 {
		t.Errorf("迁移后遥测查询不对: total=%d len=%d", total, len(logs))
	}
}

// TestLogDiagnosticsFilter 断言诊断遥测的筛选语义：client_protocol 能把两个客户端
// 分开（这是比较缓存命中率的前提），空筛选不过滤，统计接口同样支持该筛选。
func TestLogDiagnosticsFilter(t *testing.T) {
	setupLogTest(t)

	LogDB.Create(&Log{GroupName: "g1", ModelName: "deepseek-v4-pro", Success: true, Status: 200,
		ClientProtocol: "anthropic", BridgeDecoded: 3, ShimRewrites: 2})
	LogDB.Create(&Log{GroupName: "g1", ModelName: "deepseek-v4-pro", Success: true, Status: 200,
		ClientProtocol: "openai"})
	// 存量日志：client_protocol 为空，不落入任何协议桶
	LogDB.Create(&Log{GroupName: "g1", ModelName: "deepseek-v4-pro", Success: true, Status: 200})

	logs, total, err := ListLogs(LogFilter{ClientProtocol: "anthropic", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("client_protocol=anthropic 应返回 1 条, got total=%d len=%d", total, len(logs))
	}
	if logs[0].BridgeDecoded != 3 || logs[0].ShimRewrites != 2 {
		t.Errorf("遥测未原样读回: bridge=%d shim=%d", logs[0].BridgeDecoded, logs[0].ShimRewrites)
	}

	if _, total, _ := ListLogs(LogFilter{ClientProtocol: "openai", Page: 1, PageSize: 20}); total != 1 {
		t.Errorf("client_protocol=openai 应返回 1 条, got %d", total)
	}
	if _, total, _ := ListLogs(LogFilter{Page: 1, PageSize: 20}); total != 3 {
		t.Errorf("空筛选不应过滤, got %d", total)
	}

	// 统计也支持该筛选，否则按客户端比命中率只能靠翻页数，不可用
	s, err := LogStatsBy(LogFilter{ClientProtocol: "anthropic"})
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if s.TotalRequests != 1 {
		t.Errorf("按 client_protocol 统计 TotalRequests = %d, want 1", s.TotalRequests)
	}
}

func TestDeleteOldLogs(t *testing.T) {
	setupLogTest(t)

	old := time.Now().Add(-24 * time.Hour)
	for i := 0; i < 5; i++ {
		l := &Log{GroupName: "g", ModelName: "m", Success: true, Status: 200}
		if err := LogDB.Create(l).Error; err != nil {
			t.Fatalf("建旧日志失败: %v", err)
		}
		if err := LogDB.Model(l).UpdateColumn("created_at", old).Error; err != nil {
			t.Fatalf("改旧日志时间失败: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := LogDB.Create(&Log{GroupName: "g", ModelName: "m", Success: true, Status: 200}).Error; err != nil {
			t.Fatalf("建新日志失败: %v", err)
		}
	}

	// limit=2，5 条旧日志应分多批删完
	deleted, err := DeleteOldLogs(time.Now().Add(-time.Hour), 2)
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if deleted != 5 {
		t.Errorf("deleted = %d, want 5", deleted)
	}

	var count int64
	if err := LogDB.Model(&Log{}).Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 2 {
		t.Errorf("剩余日志 = %d, want 2", count)
	}
}

func TestLogRetirementPreservesHistoricalDataAndCacheStats(t *testing.T) {
	setupLogTest(t)
	legacy := Log{GroupName: "selected", ModelName: "old", Currency: "CNY", Cost: 9.875, InputToken: 10, OutputToken: 20, CacheReadToken: 30, CacheWriteToken: 40, Status: 200, Success: true, RequestID: "legacy-request"}
	if err := LogDB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []Log{
		{GroupName: "selected", ModelName: "new", InputToken: 1, OutputToken: 2, CacheReadToken: 3, CacheWriteToken: 4, Success: false, Status: 500},
		{GroupName: "other", InputToken: 100, OutputToken: 200, CacheReadToken: 300, CacheWriteToken: 400, Success: true},
	} {
		if err := LogDB.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 运行正常启动的迁移后，旧列和数值必须仍完整存在。
	if err := LogDB.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	rows, total, err := ListLogs(LogFilter{GroupName: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("分组过滤失效: %d", total)
	}
	stats, err := LogStatsBy(LogFilter{GroupName: "selected"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.InputTokens != 11 || stats.OutputTokens != 22 || stats.CacheReadTokens != 33 || stats.CacheWriteTokens != 44 || stats.TotalRequests != 2 {
		t.Fatalf("过滤用量聚合错误: %+v", stats)
	}
	success := true
	filtered, err := LogStatsBy(LogFilter{GroupName: "selected", Success: &success})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.InputTokens != 10 || filtered.OutputTokens != 20 || filtered.CacheReadTokens != 30 || filtered.CacheWriteTokens != 40 || filtered.TotalRequests != 1 {
		t.Fatalf("成功过滤错误: %+v", filtered)
	}
	var preserved Log
	if err := LogDB.First(&preserved, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.Currency != legacy.Currency || preserved.Cost != legacy.Cost || preserved.RequestID != legacy.RequestID {
		t.Fatalf("历史数据被改写: %+v", preserved)
	}
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var payload []map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, row := range payload {
		for _, field := range []string{"currency", "cost"} {
			if _, ok := row[field]; ok {
				t.Fatalf("日志响应不应暴露历史费用字段: %s", field)
			}
		}
	}
	for _, column := range []string{"cost", "currency"} {
		if !LogDB.Migrator().HasColumn(&Log{}, column) {
			t.Fatalf("历史列被移除: %s", column)
		}
	}
}
