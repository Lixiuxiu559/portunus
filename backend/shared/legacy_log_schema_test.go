package shared

import (
	"path/filepath"
	"testing"
)

// TestLegacyLogSchemaPreservesRetiredColumns 验证手写旧日志 DDL fixture；
// 不将该场景推广为任意历史版本的数据库升级保证。
func TestLegacyLogSchemaPreservesRetiredColumns(t *testing.T) {
	if DB != nil {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	}
	cfg := &Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "legacy-log.db")
	if _, err := InitDB(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
	const ddl = `CREATE TABLE logs (
 id integer PRIMARY KEY AUTOINCREMENT,
 api_key_id integer,
 group_name text,
 channel_id integer,
 model_name text,
 currency text,
 status integer,
 success numeric,
 stream numeric,
 request_id text,
 input_token integer,
 output_token integer,
 cache_read_token integer,
 cache_write_token integer,
 cost real,
 duration_ms integer,
 first_token_ms integer,
 err_kind text,
 err_msg text,
 client_protocol text,
 created_at datetime
 )`
	if err := DB.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Exec(`INSERT INTO logs (id,api_key_id,group_name,channel_id,model_name,currency,status,success,stream,request_id,input_token,output_token,cache_read_token,cache_write_token,cost,duration_ms,first_token_ms,err_kind,err_msg,client_protocol,created_at) VALUES (51,3,'legacy-group',7,'legacy-model','CNY',429,0,1,'legacy-request',10,20,30,40,18.75,120,25,'upstream_error','historical failure','anthropic','2026-09-01 12:00:00')`).Error; err != nil {
		t.Fatal(err)
	}
	// 生产使用的日志库初始化入口将补齐后续诊断列与索引。
	if err := InitLogDB(cfg); err != nil {
		t.Fatal(err)
	}
	var row Log
	if err := LogDB.First(&row, 51).Error; err != nil {
		t.Fatal(err)
	}
	if row.Currency != "CNY" || row.Cost != 18.75 || row.InputToken != 10 || row.OutputToken != 20 || row.CacheReadToken != 30 || row.CacheWriteToken != 40 {
		t.Fatalf("旧DDL迁移改变历史费用/用量: %+v", row)
	}
	if row.APIKeyID != 3 || row.GroupName != "legacy-group" || row.ChannelID != 7 || row.ModelName != "legacy-model" || row.RequestID != "legacy-request" || row.Status != 429 || row.Success || !row.Stream || row.DurationMs != 120 || row.FirstTokenMs != 25 || row.ErrKind != "upstream_error" || row.ErrMsg != "historical failure" || row.ClientProtocol != "anthropic" {
		t.Fatalf("旧调用诊断改变: %+v", row)
	}
	if row.CreatedAt.Format("2006-01-02 15:04:05") != "2026-09-01 12:00:00" {
		t.Fatalf("旧时间戳改变: %v", row.CreatedAt)
	}
	for _, column := range []string{"currency", "cost", "bridge_decoded", "shim_rewrites", "hosted_tools_stripped"} {
		if !LogDB.Migrator().HasColumn(&Log{}, column) {
			t.Fatalf("旧列未保留或新增诊断列缺失: %s", column)
		}
	}
	var count int64
	if err := LogDB.Model(&Log{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("历史行数改变 count=%d err=%v", count, err)
	}
}
