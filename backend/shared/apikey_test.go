package shared

import (
	"path/filepath"
	"testing"
)

func setupAPIKeyTest(t *testing.T) {
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
	if err := AutoMigrate(&APIKey{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := DB.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

func TestGenerateAPIKey(t *testing.T) {
	k1, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if len(k1) < 10 || k1[:3] != "sk-" {
		t.Errorf("key 格式异常: %s", k1)
	}
	k2, _ := GenerateAPIKey()
	if k1 == k2 {
		t.Errorf("两次生成的 key 不应相同")
	}
}

func TestMaskAPIKey(t *testing.T) {
	if got := MaskAPIKey("sk-abcdefgh12345678"); got != "sk-a...5678" {
		t.Errorf("MaskAPIKey = %q", got)
	}
	if got := MaskAPIKey("short"); got != "****" {
		t.Errorf("短 key 应脱敏为 ****, got %q", got)
	}
	if got := MaskAPIKey(""); got != "" {
		t.Errorf("空 key 应返回空, got %q", got)
	}
}

func TestEnsureDefaultAPIKey(t *testing.T) {
	setupAPIKeyTest(t)

	// 空库 → 自动生成一把
	k, err := EnsureDefaultAPIKey()
	if err != nil {
		t.Fatalf("EnsureDefaultAPIKey 失败: %v", err)
	}
	if k.Key == "" || k.Key[:3] != "sk-" {
		t.Errorf("自动生成的 key 异常: %q", k.Key)
	}

	// 再调一次 → 仍是同一把，不重复建
	k2, err := EnsureDefaultAPIKey()
	if err != nil {
		t.Fatalf("二次 Ensure 失败: %v", err)
	}
	if k2.ID != k.ID || k2.Key != k.Key {
		t.Errorf("已有一把时不应重建: id %d/%d key %q/%q", k2.ID, k.ID, k2.Key, k.Key)
	}

	// 手动多插两把 → 保留最早、删除其余
	if _, err := newAPIKey(); err != nil {
		t.Fatalf("建 extra1 失败: %v", err)
	}
	if _, err := newAPIKey(); err != nil {
		t.Fatalf("建 extra2 失败: %v", err)
	}
	k3, err := EnsureDefaultAPIKey()
	if err != nil {
		t.Fatalf("清理后 Ensure 失败: %v", err)
	}
	if k3.ID != k.ID {
		t.Errorf("应保留最早那把 id=%d, got %d", k.ID, k3.ID)
	}
	var count int64
	if err := DB.Model(&APIKey{}).Count(&count).Error; err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if count != 1 {
		t.Errorf("清理后应只剩一把, count=%d", count)
	}
}

func TestRegenerateAPIKey(t *testing.T) {
	setupAPIKeyTest(t)
	k, err := EnsureDefaultAPIKey()
	if err != nil {
		t.Fatalf("EnsureDefaultAPIKey 失败: %v", err)
	}
	oldID, oldKey := k.ID, k.Key

	k2, err := RegenerateAPIKey()
	if err != nil {
		t.Fatalf("RegenerateAPIKey 失败: %v", err)
	}
	if k2.ID != oldID {
		t.Errorf("重新生成应保持 id 不变: %d -> %d", oldID, k2.ID)
	}
	if k2.Key == oldKey {
		t.Errorf("重新生成应产出新 key")
	}
	if k2.Key[:3] != "sk-" {
		t.Errorf("新 key 格式异常: %q", k2.Key)
	}

	// 库里应仍只有一把
	var count int64
	if err := DB.Model(&APIKey{}).Count(&count).Error; err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if count != 1 {
		t.Errorf("重新生成后应仍只有一把, count=%d", count)
	}
}

func TestGetAPIKeyEmpty(t *testing.T) {
	setupAPIKeyTest(t)
	if _, err := GetAPIKey(); err == nil {
		t.Errorf("空库下 GetAPIKey 应报错")
	}
}