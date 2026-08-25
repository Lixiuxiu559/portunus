package shared

import (
	"time"

	"gorm.io/gorm"
)

// Log 记录一次对外 LLM 调用的完整信息，供日志查询与费用统计。
type Log struct {
	ID          int64     `gorm:"primaryKey" json:"id"`
	APIKeyID    int64     `gorm:"index" json:"api_key_id"`
	GroupName   string    `gorm:"index" json:"group_name"`
	ChannelID   int64     `gorm:"index" json:"channel_id"`
	ModelName   string    `gorm:"index" json:"model_name"`
	Status      int       `json:"status"` // 上游返回的 HTTP 状态码
	Success     bool      `gorm:"index" json:"success"`
	InputToken  int64     `json:"input_token"`
	OutputToken int64     `json:"output_token"`
	Cost        float64   `json:"cost"`
	DurationMs  int64     `json:"duration_ms"`
	CreatedAt   time.Time `json:"created_at"`
}

// LogFilter 是日志查询的筛选条件，零值字段不参与过滤。
type LogFilter struct {
	APIKeyID  int64
	ChannelID int64
	GroupName string
	ModelName string
	Success   *bool
	StartTime *time.Time
	EndTime   *time.Time
	Page      int
	PageSize  int
}

// LogStats 是日志汇总统计。
type LogStats struct {
	TotalCost      float64 `json:"total_cost"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	TotalRequests  int64   `json:"total_requests"`
	RecentRequests int64   `json:"recent_requests"` // 近 60 秒请求数
}

// ListLogs 按筛选条件分页查询日志，返回日志与总数。
func ListLogs(f LogFilter) ([]Log, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)

	var total int64
	if err := applyLogFilter(DB.Model(&Log{}), f).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []Log
	err := applyLogFilter(DB.Model(&Log{}), f).
		Order("id desc").
		Offset((page - 1) * size).
		Limit(size).
		Find(&logs).Error
	return logs, total, err
}

// LogStatsBy 按筛选条件返回汇总统计（忽略分页）。
func LogStatsBy(f LogFilter) (LogStats, error) {
	var s LogStats
	err := applyLogFilter(DB.Model(&Log{}), f).
		Select("COALESCE(SUM(cost),0) AS total_cost, COALESCE(SUM(input_token),0) AS input_tokens, COALESCE(SUM(output_token),0) AS output_tokens, COUNT(*) AS total_requests").
		Scan(&s).Error
	if err != nil {
		return s, err
	}

	err = applyLogFilter(DB.Model(&Log{}), f).
		Where("created_at >= ?", time.Now().Add(-time.Minute)).
		Count(&s.RecentRequests).Error
	return s, err
}

// applyLogFilter 应用筛选条件（不含分页）。
func applyLogFilter(db *gorm.DB, f LogFilter) *gorm.DB {
	if f.APIKeyID > 0 {
		db = db.Where("api_key_id = ?", f.APIKeyID)
	}
	if f.ChannelID > 0 {
		db = db.Where("channel_id = ?", f.ChannelID)
	}
	if f.GroupName != "" {
		db = db.Where("group_name = ?", f.GroupName)
	}
	if f.ModelName != "" {
		db = db.Where("model_name = ?", f.ModelName)
	}
	if f.Success != nil {
		db = db.Where("success = ?", *f.Success)
	}
	if f.StartTime != nil {
		db = db.Where("created_at >= ?", *f.StartTime)
	}
	if f.EndTime != nil {
		db = db.Where("created_at <= ?", *f.EndTime)
	}
	return db
}

// normalizePage 归一化分页参数。
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}
