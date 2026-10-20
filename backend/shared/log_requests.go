package shared

import (
	"time"

	"gorm.io/gorm"
)

// LogRequest 是按关联 ID 归并的持久化日志摘要。LatestLog 仅表示最近写入的
// 日志，不能据此推断客户端是否收到最终响应。无关联 ID 的历史日志各自独立。
type LogRequest struct {
	Key       string    `json:"key"`
	RequestID string    `json:"request_id"`
	LogCount  int64     `json:"log_count"`
	LatestLog Log       `json:"latest_log"`
	FirstAt   time.Time `json:"first_at"`
	LastAt    time.Time `json:"last_at"`
}

// requestLogKey 保证历史日志 ID 与真实 request_id 不会相互碰撞。
const requestLogKey = `CASE WHEN COALESCE(request_id, '') <> '' THEN 'request:' || request_id ELSE 'log:' || id END`

// ListLogRequests 先按筛选条件命中关联项，再从这些关联项的全部日志生成摘要。
// 归并、计数与分页均在数据库执行，最新日志按本页 ID 一次批量读取。
func ListLogRequests(f LogFilter) ([]LogRequest, int64, error) {
	var rows []LogRequest
	var total int64
	// 保持计数、摘要和日志明细处于同一读快照，避免并发写入或过期清理
	// 导致同一页的数量、边界时间与最新日志相互矛盾。
	err := LogDB.Transaction(func(tx *gorm.DB) error {
		var err error
		rows, total, err = listLogRequests(tx, f)
		return err
	})
	return rows, total, err
}

func listLogRequests(db *gorm.DB, f LogFilter) ([]LogRequest, int64, error) {
	page, size := normalizePage(f.Page, f.PageSize)
	matched := func() *gorm.DB {
		return applyLogFilter(db.Model(&Log{}), f).Select(requestLogKey).Group(requestLogKey)
	}
	var total int64
	if err := db.Table("(?) AS matching_requests", matched()).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]LogRequest, 0)
	if total == 0 {
		return rows, 0, nil
	}
	// 边界时间从 datetime 原列读取，避免聚合表达式丢失 SQLite 类型信息。
	var summaries []struct {
		Key      string
		LogCount int64
		LatestID int64
		FirstID  int64
		LastID   int64
	}
	// 回填日志的写入顺序未必等于时间顺序；分别求最早、最晚时间对应的行 ID。
	all := db.Model(&Log{}).
		Select(requestLogKey+` AS request_key, id,
			FIRST_VALUE(id) OVER (PARTITION BY `+requestLogKey+` ORDER BY created_at, id) AS first_id,
			FIRST_VALUE(id) OVER (PARTITION BY `+requestLogKey+` ORDER BY created_at DESC, id DESC) AS last_id`).
		Where(requestLogKey+" IN (?)", matched())
	if err := db.Table("(?) AS associated_logs", all).
		Select("request_key AS key, COUNT(*) AS log_count, MAX(id) AS latest_id, MIN(first_id) AS first_id, MIN(last_id) AS last_id").
		Group("request_key").Order("latest_id DESC").Offset((page - 1) * size).Limit(size).
		Scan(&summaries).Error; err != nil {
		return nil, total, err
	}
	if len(summaries) == 0 {
		return rows, total, nil
	}
	ids := make([]int64, 0, len(summaries)*3)
	for _, summary := range summaries {
		ids = append(ids, summary.LatestID, summary.FirstID, summary.LastID)
	}
	var logs []Log
	if err := db.Where("id IN ?", ids).Find(&logs).Error; err != nil {
		return nil, total, err
	}
	byID := make(map[int64]Log, len(logs))
	for _, log := range logs {
		byID[log.ID] = log
	}
	for _, summary := range summaries {
		latest := byID[summary.LatestID]
		rows = append(rows, LogRequest{Key: summary.Key, RequestID: latest.RequestID,
			LogCount: summary.LogCount, LatestLog: latest,
			FirstAt: byID[summary.FirstID].CreatedAt, LastAt: byID[summary.LastID].CreatedAt})
	}
	return rows, total, nil
}
