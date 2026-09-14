package channel

import (
	"time"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

// Channel 是一个上游供应商的连接配置。
type Channel struct {
	ID       int64             `gorm:"primaryKey" json:"id"`
	Name     string            `gorm:"unique;not null" json:"name"`
	Type     protocol.Provider `json:"type"`
	BaseURL  string            `json:"base_url"`                      // 仅基础地址，具体路径由协议层补全
	Key      string            `json:"key"`                           // 上游访问凭据
	AutoSync bool              `gorm:"default:true" json:"auto_sync"` // 是否参与自动模型同步
	// ThinkingCompat thinking 兼容垫片：上游是严格 thinking 模型（DeepSeek V4 等）
	// 时开启，assistant 历史缺 reasoning_content 会注入占位，避免多轮 400。
	// 默认开：宽容上游忽略占位字段，垫片只对缺 reasoning_content 的历史 assistant
	// 动手（已有内容不覆盖），误开代价仅历史消息多十余字符；漏开则多轮必 400。
	ThinkingCompat bool      `gorm:"default:true" json:"thinking_compat"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Valid 校验渠道字段，供创建 / 更新时调用。
func (c *Channel) Valid() bool {
	return c.Name != "" && c.BaseURL != "" && c.Type.Valid()
}
