package shared

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// APIKey 是对外 /v1 接口的鉴权凭据，也用于日志 / 费用的调用方归属。
type APIKey struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"not null" json:"name"`
	Key       string    `gorm:"unique;not null" json:"key"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// ErrAPIKeyInvalid 表示 API Key 字段校验不通过。
var ErrAPIKeyInvalid = &StatusError{Status: 400, Message: "API Key 名称不能为空"}

// GenerateAPIKey 生成一个随机 API Key（sk- 前缀）。
func GenerateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// MaskAPIKey 对 key 脱敏，避免列表 / 详情泄露明文凭据。
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// ListAPIKeys 返回所有 API Key。
func ListAPIKeys() ([]APIKey, error) {
	var ks []APIKey
	err := DB.Order("id").Find(&ks).Error
	return ks, err
}

// GetAPIKey 返回单个 API Key。
func GetAPIKey(id int64) (*APIKey, error) {
	var k APIKey
	if err := DB.First(&k, id).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

// CreateAPIKey 创建 API Key，自动生成随机 key，默认启用。
func CreateAPIKey(name string) (*APIKey, error) {
	if name == "" {
		return nil, ErrAPIKeyInvalid
	}
	key, err := GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	k := APIKey{Name: name, Key: key, Enabled: true}
	if err := DB.Create(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

// UpdateAPIKey 更新 name / enabled，仅应用非 nil 字段。
func UpdateAPIKey(id int64, name *string, enabled *bool) (*APIKey, error) {
	var k APIKey
	if err := DB.First(&k, id).Error; err != nil {
		return nil, err
	}
	if name != nil {
		if *name == "" {
			return nil, ErrAPIKeyInvalid
		}
		k.Name = *name
	}
	if enabled != nil {
		k.Enabled = *enabled
	}
	if err := DB.Save(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

// DeleteAPIKey 删除 API Key。
func DeleteAPIKey(id int64) error {
	return DB.Delete(&APIKey{}, id).Error
}
