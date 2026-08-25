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

func TestAPIKeyCRUD(t *testing.T) {
	setupAPIKeyTest(t)

	k, err := CreateAPIKey("my-key")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if k.Key == "" || k.Key[:3] != "sk-" {
		t.Errorf("创建后应有完整 key: %q", k.Key)
	}
	if !k.Enabled {
		t.Errorf("新 key 应默认启用")
	}

	ks, err := ListAPIKeys()
	if err != nil || len(ks) != 1 {
		t.Fatalf("list 失败: %v, len=%d", err, len(ks))
	}

	newName := "renamed"
	disabled := false
	k2, err := UpdateAPIKey(k.ID, &newName, &disabled)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if k2.Name != "renamed" || k2.Enabled {
		t.Errorf("更新结果不对: %+v", k2)
	}

	if _, err := CreateAPIKey(""); err != ErrAPIKeyInvalid {
		t.Errorf("空名称应返回 ErrAPIKeyInvalid, got %v", err)
	}

	if err := DeleteAPIKey(k.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := GetAPIKey(k.ID); err == nil {
		t.Errorf("删除后 Get 应报错")
	}
}
