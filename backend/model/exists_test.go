package model

import (
	"path/filepath"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func setupExistsTest(t *testing.T) {
	t.Helper()
	if shared.DB != nil {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	}
	cfg := &shared.Config{}
	cfg.Database.Type = "sqlite"
	cfg.Database.Path = filepath.Join(t.TempDir(), "test.db")
	if _, err := shared.InitDB(cfg); err != nil {
		t.Fatalf("初始化 DB 失败: %v", err)
	}
	if err := shared.AutoMigrate(&Model{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := shared.DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

func TestExists(t *testing.T) {
	setupExistsTest(t)
	if ok, _ := Exists(1); ok {
		t.Error("不存在的模型应返回 false")
	}
	m := Model{ChannelID: 1, Name: "m1"}
	if err := shared.DB.Create(&m).Error; err != nil {
		t.Fatalf("建模型失败: %v", err)
	}
	if ok, _ := Exists(m.ID); !ok {
		t.Error("刚建的模型应返回 true")
	}
}
