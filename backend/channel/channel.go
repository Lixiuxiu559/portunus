package channel

import (
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// Channel 是一个上游供应商的连接配置。
type Channel struct {
	ID        int64             `gorm:"primaryKey" json:"id"`
	Name      string            `gorm:"unique;not null" json:"name"`
	Type      protocol.Provider `json:"type"`
	BaseURL   string            `json:"base_url"` // 仅基础地址，具体路径由协议层补全
	Key       string            `json:"key"`      // 上游访问凭据
	Enabled   bool              `gorm:"default:true" json:"enabled"`
	AutoSync  bool              `gorm:"default:true" json:"auto_sync"` // 是否参与自动模型同步
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Valid 校验渠道字段，供创建 / 更新时调用。
func (c *Channel) Valid() bool {
	return c.Name != "" && c.BaseURL != "" && c.Type.Valid()
}
