package shared

import "time"

// Log 记录一次对外 LLM 调用的完整信息，供日志查询与费用统计。
type Log struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	APIKeyID    int64     `gorm:"index" json:"api_key_id"`
	GroupName   string    `gorm:"index" json:"group_name"`
	ChannelID   int64     `gorm:"index" json:"channel_id"`
	ModelName   string    `gorm:"index" json:"model_name"`
	Status      int       `json:"status"`           // 上游返回的 HTTP 状态码
	Success     bool      `gorm:"index" json:"success"`
	InputToken  int64     `json:"input_token"`
	OutputToken int64     `json:"output_token"`
	Cost        float64   `json:"cost"`
	DurationMs  int64     `json:"duration_ms"`
	CreatedAt   time.Time `json:"created_at"`
}
