package model

import "time"

// Model 是渠道拉回的一个可用模型，价格是它的属性。
type Model struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	ChannelID       int64     `gorm:"not null;uniqueIndex:idx_channel_model" json:"channel_id"`
	Name            string    `gorm:"not null;uniqueIndex:idx_channel_model" json:"name"`
	InputPrice      float64   `json:"input_price"`       // 输入价（每 1M token）
	OutputPrice     float64   `json:"output_price"`      // 输出价（每 1M token）
	CacheReadPrice  float64   `json:"cache_read_price"`  // 缓存输入价（每 1M token）
	CacheWritePrice float64   `json:"cache_write_price"` // 缓存输出价（每 1M token）
	Enabled         bool      `gorm:"default:true" json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Price 是模型的四维价格，用于内置价格表。
// 单位：每 1M token；货币由全局设置 currency 决定（USD / CNY）。
type Price struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// defaultPrice 是内置价格表，拉回模型时用它填默认价，用户可覆盖。
// 此表仅作初始默认值，后续可替换为更完整的数据源。
var defaultPrice = map[string]Price{
	"gpt-4o":           {Input: 2.5, Output: 10, CacheRead: 1.25, CacheWrite: 2.5},
	"gpt-4o-mini":      {Input: 0.15, Output: 0.6, CacheRead: 0.075, CacheWrite: 0.15},
	"claude-sonnet":    {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-haiku":     {Input: 0.8, Output: 4, CacheRead: 0.08, CacheWrite: 1},
	"gemini-2.5-flash": {Input: 0.3, Output: 2.5, CacheRead: 0.03, CacheWrite: 0.3},
	"deepseek-v4-pro":  {Input: 1.0, Output: 4.0, CacheRead: 0.1, CacheWrite: 1.0},
	"deepseek-v4-flash": {Input: 0.3, Output: 1.2, CacheRead: 0.03, CacheWrite: 0.3},
}

// ApplyDefaultPrice 为拉回的模型套用内置默认价；未匹配到的模型价格保持 0，由用户手填。
func (m *Model) ApplyDefaultPrice() {
	p, ok := defaultPrice[m.Name]
	if !ok {
		return
	}
	if m.InputPrice == 0 {
		m.InputPrice = p.Input
	}
	if m.OutputPrice == 0 {
		m.OutputPrice = p.Output
	}
	if m.CacheReadPrice == 0 {
		m.CacheReadPrice = p.CacheRead
	}
	if m.CacheWritePrice == 0 {
		m.CacheWritePrice = p.CacheWrite
	}
}
