package channel

import (
	"path/filepath"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
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
	if err := shared.AutoMigrate(&Channel{}); err != nil {
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
		t.Error("不存在的渠道应返回 false")
	}
	c := Channel{Name: "c1", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&c).Error; err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	if ok, _ := Exists(c.ID); !ok {
		t.Error("刚建的渠道应返回 true")
	}
}
