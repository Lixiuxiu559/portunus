package shared

import (
	"path/filepath"
	"strings"
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

	// 多把 Key → 全部保留，重启初始化不收敛
	if _, err := CreateAPIKey("extra"); err != nil {
		t.Fatalf("建 extra1 失败: %v", err)
	}
	if _, err := CreateAPIKey("extra"); err != nil {
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
	if count != 3 {
		t.Errorf("初始化应保留所有 Key, count=%d", count)
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

func TestNamedAPIKeysLifecycle(t *testing.T) {
	setupAPIKeyTest(t)
	claude, err := CreateAPIKey("  Claude Code  ")
	if err != nil {
		t.Fatal(err)
	}
	codex, err := CreateAPIKey("Codex")
	if err != nil {
		t.Fatal(err)
	}
	if claude.Name != "Claude Code" || claude.ID == codex.ID || claude.Key == codex.Key {
		t.Fatal("命名和独立凭据错误")
	}
	for _, key := range []*APIKey{claude, codex} {
		if id, ok := AuthenticateAPIKey(key.Key); !ok || id != key.ID {
			t.Fatal("有效 Key 鉴权归属错误")
		}
	}
	renamed, err := RenameAPIKey(claude.ID, "工作 Claude")
	if err != nil || renamed.Key != claude.Key {
		t.Fatalf("重命名改变凭据: %v", err)
	}
	rotated, err := RegenerateAPIKeyByID(claude.ID)
	if err != nil || rotated.ID != claude.ID || rotated.Name != "工作 Claude" {
		t.Fatalf("重置改变身份: %v", err)
	}
	if _, ok := AuthenticateAPIKey(claude.Key); ok {
		t.Fatal("重置后的旧凭据仍有效")
	}
	if id, ok := AuthenticateAPIKey(rotated.Key); !ok || id != claude.ID {
		t.Fatal("新凭据无效")
	}
	if id, ok := AuthenticateAPIKey(codex.Key); !ok || id != codex.ID {
		t.Fatal("重置影响其他 Key")
	}
	if err := RevokeAPIKey(claude.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := AuthenticateAPIKey(rotated.Key); ok {
		t.Fatal("停用凭据仍有效")
	}
	if _, err := RegenerateAPIKeyByID(claude.ID); err == nil {
		t.Fatal("已停用 Key 不应重置")
	}
	if err := RevokeAPIKey(codex.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureDefaultAPIKey(); err != nil {
		t.Fatal(err)
	}
	keys, err := ListAPIKeys()
	if err != nil || len(keys) != 2 || !keys[0].Revoked || !keys[1].Revoked {
		t.Fatal("重启初始化恢复了已停用 Key 或丢失历史身份")
	}
	if _, err := GetAPIKey(); err == nil {
		t.Fatal("全部停用后不应存在有效默认 Key")
	}
}

func TestAPIKeyNamesValidation(t *testing.T) {
	setupAPIKeyTest(t)
	for _, name := range []string{"", " \t", strings.Repeat("中", 81)} {
		if _, err := CreateAPIKey(name); err == nil {
			t.Fatalf("应拒绝非法名称 %q", name)
		}
	}
	k, err := CreateAPIKey(strings.Repeat("中", 80))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenameAPIKey(k.ID, " "); err == nil {
		t.Fatal("应拒绝空名称")
	}
	if _, err := RenameAPIKey(999999, "不存在"); err == nil {
		t.Fatal("应拒绝不存在 ID")
	}
	if err := RevokeAPIKey(999999); err == nil {
		t.Fatal("应拒绝不存在 ID")
	}
}

func TestLegacyAPIKeyMigration(t *testing.T) {
	setupAPIKeyTest(t)
	if err := DB.Migrator().DropTable(&APIKey{}); err != nil {
		t.Fatal(err)
	}
	if err := DB.Exec(`CREATE TABLE api_keys (id integer PRIMARY KEY, key text NOT NULL UNIQUE, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Exec(`INSERT INTO api_keys (id, key, created_at) VALUES (42, 'sk-legacy', '2026-01-01 00:00:00')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(&APIKey{}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		k, err := EnsureDefaultAPIKey()
		if err != nil || k.ID != 42 || k.Key != "sk-legacy" || k.Name != "默认 Key" || k.Revoked {
			t.Fatalf("旧 Key 迁移不兼容: %+v %v", k, err)
		}
	}
	if id, ok := AuthenticateAPIKey("sk-legacy"); !ok || id != 42 {
		t.Fatal("迁移后的旧 Key 无法鉴权")
	}
}

func TestAPIKeyDisableRestoreAndDelete(t *testing.T) {
	setupAPIKeyTest(t)
	key, err := CreateAPIKey("same-name")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetAPIKeyRevoked(key.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := AuthenticateAPIKey(key.Key); ok {
		t.Fatal("禁用后仍通过鉴权")
	}
	restored, err := SetAPIKeyRevoked(key.ID, false)
	if err != nil || restored.ID != key.ID || restored.Key != key.Key {
		t.Fatalf("恢复改变身份: %v", err)
	}
	if id, ok := AuthenticateAPIKey(key.Key); !ok || id != key.ID {
		t.Fatal("启用后无法鉴权")
	}
	if err := DeleteAPIKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := AuthenticateAPIKey(key.Key); ok {
		t.Fatal("删除后仍通过鉴权")
	}
	if _, err := SetAPIKeyRevoked(key.ID, false); err == nil {
		t.Fatal("已删除Key不能恢复")
	}
	if _, err := RenameAPIKey(key.ID, "revive"); err == nil {
		t.Fatal("已删除Key不能重命名")
	}
	if _, err := RegenerateAPIKeyByID(key.ID); err == nil {
		t.Fatal("已删除Key不能重置")
	}
	if _, err := EnsureDefaultAPIKey(); err != nil {
		t.Fatal(err)
	}
	keys, err := ListAPIKeys()
	if err != nil || len(keys) != 0 {
		t.Fatal("删除所有Key后重启不应重新创建或显示已删除Key")
	}
	history, err := ListHistoricalAPIKeys()
	if err != nil || len(history) != 1 || history[0].ID != key.ID || history[0].Name != "same-name" || !history[0].DeletedAt.Valid || history[0].Key != "" {
		t.Fatal("历史元数据未保留或包含密钥")
	}
	replacement, err := CreateAPIKey("same-name")
	if err != nil || replacement.ID == key.ID {
		t.Fatal("同名新Key复用了旧ID")
	}
}
