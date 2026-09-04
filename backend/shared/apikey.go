package shared

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// APIKey 是对外 /v1 接口的鉴权凭据，也用于日志 / 费用的调用方归属。
// 个人单机使用场景：全局仅保留一行 key，不提供多 key 的增删。
type APIKey struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"unique;not null" json:"key"`
	CreatedAt time.Time `json:"created_at"`
}

// GenerateAPIKey 生成一个随机 API Key（sk- 前缀）。
func GenerateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// MaskAPIKey 对 key 脱敏，避免详情泄露明文凭据。
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// GetAPIKey 返回当前唯一的一把 API Key；不存在时返回 gorm.ErrRecordNotFound。
func GetAPIKey() (*APIKey, error) {
	var k APIKey
	if err := DB.First(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}

// EnsureDefaultAPIKey 保证库里恰好有一把 key：
// 0 把 → 自动生成；1 把 → 原样保留；多把 → 保留最早（id 最小），删除其余。
// 返回这把 key。
func EnsureDefaultAPIKey() (*APIKey, error) {
	var ks []APIKey
	if err := DB.Order("id").Find(&ks).Error; err != nil {
		return nil, err
	}
	switch len(ks) {
	case 0:
		return newAPIKey()
	case 1:
		return &ks[0], nil
	default:
		keep := ks[0]
		dropIDs := make([]int64, 0, len(ks)-1)
		for i := 1; i < len(ks); i++ {
			dropIDs = append(dropIDs, ks[i].ID)
		}
		if err := DB.Where("id IN ?", dropIDs).Delete(&APIKey{}).Error; err != nil {
			return nil, err
		}
		return &keep, nil
	}
}

// RegenerateAPIKey 原地重新生成 key：保持行 id 不变（日志归属不散），旧 key 立即失效。
func RegenerateAPIKey() (*APIKey, error) {
	k, err := EnsureDefaultAPIKey()
	if err != nil {
		return nil, err
	}
	key, err := GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	k.Key = key
	if err := DB.Save(k).Error; err != nil {
		return nil, err
	}
	return k, nil
}

// newAPIKey 生成随机 key 并落库。
func newAPIKey() (*APIKey, error) {
	key, err := GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	k := APIKey{Key: key}
	if err := DB.Create(&k).Error; err != nil {
		return nil, err
	}
	return &k, nil
}