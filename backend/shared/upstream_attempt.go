package shared

import (
	"time"
)

// UpstreamAttempt 记录一次应用显式发起的 http.Client.Do 尝试，不参与 Log 的用量与请求计数。
// 包括连接失败和取消；不等同于上游实际收到的请求数或 Transport 的物理发送次数。
// 同一客户端请求按 Sequence 排序；没有记录的历史请求表示未采集，不能推断尝试次数。
type UpstreamAttempt struct {
	ID               int64     `gorm:"primaryKey" json:"id"`
	RequestID        string    `gorm:"index;uniqueIndex:idx_request_sequence,priority:1" json:"request_id"`
	Sequence         int       `gorm:"uniqueIndex:idx_request_sequence,priority:2" json:"sequence"`
	RelayAttempt     int       `json:"relay_attempt"`
	Reason           string    `json:"reason"`
	APIKeyID         int64     `json:"api_key_id"`
	GroupName        string    `json:"group_name"`
	ChannelID        int64     `json:"channel_id"`
	ModelID          int64     `json:"model_id"`
	ModelName        string    `json:"model_name"`
	ClientProtocol   string    `json:"client_protocol"`
	UpstreamProtocol string    `json:"upstream_protocol"`
	Stream           bool      `json:"stream"`
	BudgetField      string    `json:"budget_field"`
	Status           int       `json:"status"`
	Success          bool      `json:"success"`
	ErrKind          string    `json:"err_kind,omitempty"`
	ErrMsg           string    `json:"err_msg,omitempty"`
	DurationMs       int64     `json:"duration_ms"`
	StartedAt        time.Time `gorm:"index" json:"started_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// WriteUpstreamAttempt 写入独立日志库，错误原文最多保留 256 个字符。
func WriteUpstreamAttempt(attempt *UpstreamAttempt) error {
	row := *attempt
	if message := []rune(row.ErrMsg); len(message) > 256 {
		row.ErrMsg = string(message[:256])
	}
	return LogDB.Create(&row).Error
}

// ListUpstreamAttempts 精确查询请求的已采集尝试，不从旧日志回填或推断。
func ListUpstreamAttempts(requestID string, page, pageSize int) ([]UpstreamAttempt, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	query := LogDB.Model(&UpstreamAttempt{}).Where("request_id = ?", requestID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]UpstreamAttempt, 0)
	err := query.Order("sequence asc, id asc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

func deleteOldUpstreamAttempts(target time.Time, limit int) error {
	for {
		var ids []int64
		if err := LogDB.Model(&UpstreamAttempt{}).Where("started_at < ?", target).
			Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := LogDB.Where("id IN ?", ids).Delete(&UpstreamAttempt{}).Error; err != nil {
			return err
		}
		if len(ids) < limit {
			return nil
		}
	}
}
