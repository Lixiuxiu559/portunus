package shared

import (
	"path/filepath"
	"testing"
	"time"
)

func setupSettingTest(t *testing.T) {
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
	if err := AutoMigrate(&Setting{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

func TestSyncIntervalSetting(t *testing.T) {
	setupSettingTest(t)

	// 未写入时回退默认值
	if got := GetSyncInterval(); got != SyncIntervalDefault {
		t.Fatalf("默认间隔 = %d, want %d", got, SyncIntervalDefault)
	}

	if err := SetSyncInterval(120); err != nil {
		t.Fatalf("设置间隔失败: %v", err)
	}
	if got := GetSyncInterval(); got != 120 {
		t.Fatalf("间隔 = %d, want 120", got)
	}

	if err := SetSyncInterval(0); err != nil {
		t.Fatalf("设置 0（关闭）失败: %v", err)
	}
	if got := GetSyncInterval(); got != 0 {
		t.Fatalf("间隔 = %d, want 0", got)
	}

	if err := SetSyncInterval(-1); err == nil {
		t.Fatal("负间隔应返回错误")
	}
}

func TestLastSyncAtSetting(t *testing.T) {
	setupSettingTest(t)

	if got := GetLastSyncAt(); got != 0 {
		t.Fatalf("初始 last_sync_at = %d, want 0", got)
	}

	now := time.Unix(1700000000, 0)
	if err := SetLastSyncAt(now); err != nil {
		t.Fatalf("写 last_sync_at 失败: %v", err)
	}
	if got := GetLastSyncAt(); got != 1700000000 {
		t.Fatalf("last_sync_at = %d, want 1700000000", got)
	}
}

func TestEnsureDefaultSettingsIncludesSync(t *testing.T) {
	setupSettingTest(t)

	if err := EnsureDefaultSettings(); err != nil {
		t.Fatalf("EnsureDefaultSettings 失败: %v", err)
	}
	if got := GetSyncInterval(); got != SyncIntervalDefault {
		t.Fatalf("默认间隔 = %d, want %d", got, SyncIntervalDefault)
	}
}