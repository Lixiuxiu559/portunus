package model

import (
	"path/filepath"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// TestLegacyModelSchemaPreservesRetiredColumns 使用手写旧表，而非当前实体建表，
// 验证此旧 DDL fixture 经正常迁移后历史计价列和非零值仍保留。
func TestLegacyModelSchemaPreservesRetiredColumns(t *testing.T) {
	if shared.DB != nil {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	}
	cfg := &shared.Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "legacy-model.db")
	if _, err := shared.InitDB(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
	const ddl = `CREATE TABLE models (
 id integer PRIMARY KEY AUTOINCREMENT,
 channel_id integer NOT NULL,
 name text NOT NULL,
 currency text NOT NULL DEFAULT "USD",
 input_price real,
 output_price real,
 cache_read_price real,
 cache_write_price real,
 created_at datetime,
 updated_at datetime
 )`
	if err := shared.DB.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Exec(`CREATE UNIQUE INDEX idx_channel_model ON models(channel_id, name)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Exec(`INSERT INTO models (id,channel_id,name,currency,input_price,output_price,cache_read_price,cache_write_price,created_at,updated_at) VALUES (41,7,'legacy-model','CNY',1.25,9.5,0.4,2.3,'2026-09-01 12:00:00','2026-09-01 13:00:00')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.AutoMigrate(&Model{}); err != nil {
		t.Fatal(err)
	}
	var row Model
	if err := shared.DB.First(&row, 41).Error; err != nil {
		t.Fatal(err)
	}
	if row.ChannelID != 7 || row.Name != "legacy-model" || row.Currency != CurrencyCNY || row.InputPrice != 1.25 || row.OutputPrice != 9.5 || row.CacheReadPrice != .4 || row.CacheWritePrice != 2.3 {
		t.Fatalf("旧DDL迁移改写了数据: %+v", row)
	}
	if row.CreatedAt.Format("2006-01-02 15:04:05") != "2026-09-01 12:00:00" || row.UpdatedAt.Format("2006-01-02 15:04:05") != "2026-09-01 13:00:00" {
		t.Fatalf("旧时间戳被改写: %+v", row)
	}
	for _, column := range []string{"currency", "input_price", "output_price", "cache_read_price", "cache_write_price"} {
		if !shared.DB.Migrator().HasColumn(&Model{}, column) {
			t.Fatalf("旧列被删除: %s", column)
		}
	}
	if !shared.DB.Migrator().HasIndex(&Model{}, "idx_channel_model") {
		t.Fatal("模型唯一索引丢失")
	}
	var count int64
	if err := shared.DB.Model(&Model{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("旧行数改变 count=%d err=%v", count, err)
	}
}
