package shared

import (
	"time"

	"gorm.io/gorm"
)

// 本文件承载调用日志的管理端读侧：筛选、分页查询、汇总统计与过期清理。
// 实体与失败归因词表在 log.go。

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

// LogStats 是日志汇总统计。费用按日志快照的货币分桶累计（无汇率换算）；
// 存量日志与空 currency 记入 USD 桶。
type LogStats struct {
	TotalCostUSD   float64 `json:"total_cost_usd"`
	TotalCostCNY   float64 `json:"total_cost_cny"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	TotalRequests  int64   `json:"total_requests"`
	RecentRequests int64   `json:"recent_requests"` // 近 60 秒请求数
}

// ListLogs 按筛选条件分页查询日志，返回日志与总数。
func ListLogs(f LogFilter) ([]Log, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)

	var total int64
	if err := applyLogFilter(LogDB.Model(&Log{}), f).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []Log
	err := applyLogFilter(LogDB.Model(&Log{}), f).
		Order("id desc").
		Offset((page - 1) * size).
		Limit(size).
		Find(&logs).Error
	return logs, total, err
}

// LogStatsBy 按筛选条件返回汇总统计（忽略分页）。
func LogStatsBy(f LogFilter) (LogStats, error) {
	var s LogStats
	err := applyLogFilter(LogDB.Model(&Log{}), f).
		Select(`COALESCE(SUM(CASE WHEN currency = 'CNY' THEN cost ELSE 0 END),0) AS total_cost_cny,
			COALESCE(SUM(CASE WHEN currency = 'CNY' THEN 0 ELSE cost END),0) AS total_cost_usd,
			COALESCE(SUM(input_token),0) AS input_tokens,
			COALESCE(SUM(output_token),0) AS output_tokens,
			COUNT(*) AS total_requests`).
		Scan(&s).Error
	if err != nil {
		return s, err
	}

	err = applyLogFilter(LogDB.Model(&Log{}), f).
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

// DeleteOldLogs 分批删除 created_at 早于 target 的日志，避免一次性删除锁住大表。
// limit 为每批删除条数，返回删除总数。
// GORM 的 Delete 会忽略 Limit，故先分页查出待删 id，再按 id 删除。
func DeleteOldLogs(target time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	var total int64
	for {
		var ids []int64
		if err := LogDB.Model(&Log{}).
			Where("created_at < ?", target).
			Order("id").
			Limit(limit).
			Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			break
		}
		res := LogDB.Where("id IN ?", ids).Delete(&Log{})
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if int64(len(ids)) < int64(limit) {
			break
		}
	}
	return total, nil
}
