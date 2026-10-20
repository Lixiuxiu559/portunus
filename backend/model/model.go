package model

import "time"

// Currency 仅用于保留历史数据库字段，不参与当前模型配置或调用处理。
type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyCNY Currency = "CNY"
)

// Model 是渠道拉回的一个可用模型；旧计价列只保留历史数据，不对外展示。
type Model struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	ChannelID       int64     `gorm:"not null;uniqueIndex:idx_channel_model" json:"channel_id"`
	Name            string    `gorm:"not null;uniqueIndex:idx_channel_model" json:"name"`
	Currency        Currency  `gorm:"not null;default:USD" json:"-"` // 历史列；保持原有数据库定义
	InputPrice      float64   `json:"-"`                             // 历史输入价
	OutputPrice     float64   `json:"-"`                             // 历史输出价
	CacheReadPrice  float64   `json:"-"`                             // 历史缓存读取价
	CacheWritePrice float64   `json:"-"`                             // 历史缓存写入价
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
