package shared

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// APIKey 是命名的服务接入凭据；ID 是稳定的用量归属。
// Revoked 表示可恢复的禁用；DeletedAt 表示从管理列表移除且不可恢复的软删除。
type APIKey struct {
	ID        int64          `gorm:"primaryKey" json:"id"`
	Name      string         `gorm:"size:80;not null;default:''" json:"name"`
	Key       string         `gorm:"unique;not null" json:"key"`
	Revoked   bool           `gorm:"not null;default:false" json:"revoked"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at"`
}

func GenerateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + base64.RawURLEncoding.EncodeToString(b), nil
}

func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// GetAPIKey 保留旧管理接口语义，返回最早的有效 Key。
func GetAPIKey() (*APIKey, error) {
	var k APIKey
	if err := DB.Where("revoked = ?", false).First(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

func ListAPIKeys() ([]APIKey, error) {
	keys := make([]APIKey, 0)
	err := DB.Order("id").Find(&keys).Error
	return keys, err
}

// EnsureDefaultAPIKey 只在从未创建过 Key 的空库初始化；保留所有现有 Key。
// 旧行补上名称，保留原密钥和 ID。全部禁用或删除时，重启不会自动创建有效 Key。
func EnsureDefaultAPIKey() (*APIKey, error) {
	if err := DB.Model(&APIKey{}).Where("name = ?", "").Update("name", "默认 Key").Error; err != nil {
		return nil, err
	}
	var k APIKey
	err := DB.Unscoped().First(&k).Error
	if err == gorm.ErrRecordNotFound {
		return CreateAPIKey("默认 Key")
	}
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func apiKeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", &StatusError{Status: http.StatusBadRequest, Message: "API Key 名称须为 1–80 个字符"}
	}
	return name, nil
}

func CreateAPIKey(name string) (*APIKey, error) {
	name, err := apiKeyName(name)
	if err != nil {
		return nil, err
	}
	key, err := GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	k := APIKey{Name: name, Key: key}
	if err := DB.Create(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

// updateActiveAPIKey 的条件更新避免并发禁用后重新激活或更改凭据。
func updateActiveAPIKey(id int64, changes map[string]any) (*APIKey, error) {
	result := DB.Model(&APIKey{}).Where("id = ? AND revoked = ?", id, false).Updates(changes)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var k APIKey
	if err := DB.First(&k, id).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

func RenameAPIKey(id int64, name string) (*APIKey, error) {
	name, err := apiKeyName(name)
	if err != nil {
		return nil, err
	}
	return updateActiveAPIKey(id, map[string]any{"name": name})
}

func RegenerateAPIKeyByID(id int64) (*APIKey, error) {
	key, err := GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	return updateActiveAPIKey(id, map[string]any{"key": key})
}

// RegenerateAPIKey 兼容旧管理接口；只重置最早的有效 Key。
func RegenerateAPIKey() (*APIKey, error) {
	k, err := GetAPIKey()
	if err != nil {
		return nil, err
	}
	return RegenerateAPIKeyByID(k.ID)
}

// ListHistoricalAPIKeys 只返回日志筛选所需元数据，包含已删除身份，不返回密钥。
func ListHistoricalAPIKeys() ([]APIKey, error) {
	keys := make([]APIKey, 0)
	err := DB.Unscoped().Select("id", "name", "revoked", "deleted_at", "created_at").Order("id").Find(&keys).Error
	return keys, err
}

func SetAPIKeyRevoked(id int64, revoked bool) (*APIKey, error) {
	result := DB.Model(&APIKey{}).Where("id = ?", id).Update("revoked", revoked)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var k APIKey
	if err := DB.First(&k, id).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

func RevokeAPIKey(id int64) error {
	_, err := SetAPIKeyRevoked(id, true)
	return err
}

func DeleteAPIKey(id int64) error {
	result := DB.Where("id = ?", id).Delete(&APIKey{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// APIKeyIdentity 是鉴权时捕获的调用方快照，不含明文凭据。
type APIKeyIdentity struct {
	ID   int64
	Name string
}

func AuthenticateAPIKeyIdentity(key string) (APIKeyIdentity, bool) {
	var k APIKey
	if key == "" || DB.Where("key = ? AND revoked = ?", key, false).First(&k).Error != nil {
		return APIKeyIdentity{}, false
	}
	return APIKeyIdentity{ID: k.ID, Name: k.Name}, true
}

// AuthenticateAPIKey 用于网关鉴权，禁用、删除或重置后的旧凭据立即拒绝新请求。
func AuthenticateAPIKey(key string) (int64, bool) {
	identity, ok := AuthenticateAPIKeyIdentity(key)
	return identity.ID, ok
}
