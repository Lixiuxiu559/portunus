package shared

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// MaxRoutingEvents bounds per-request diagnostic storage independently of HTTP attempts.
const MaxRoutingEvents = 256

// RoutingEvent contains only routing metadata; never upstream payloads, URLs or credentials.
type RoutingEvent struct {
	Sequence      int    `json:"sequence"`
	Candidate     int    `json:"candidate"`
	RelayAttempt  int    `json:"relay_attempt"`
	ChannelID     int64  `json:"channel_id"`
	ModelID       int64  `json:"model_id"`
	Event         string `json:"event"`
	Reason        string `json:"reason"`
	ErrKind       string `json:"err_kind,omitempty"`
	Scope         string `json:"scope,omitempty"`
	RetryAfter    int    `json:"retry_after,omitempty"`
	Probe         bool   `json:"probe,omitempty"`
	CapacityLimit *int   `json:"capacity_limit,omitempty"`
	InFlight      *int   `json:"in_flight,omitempty"`
}

// RoutingTrace is one request's bounded routing history, separate from usage and Do counts.
type RoutingTrace struct {
	ID              int64          `gorm:"primaryKey" json:"id"`
	RequestID       string         `gorm:"uniqueIndex" json:"request_id"`
	SchemaVersion   int            `json:"schema_version"`
	APIKeyID        int64          `json:"api_key_id"`
	GroupID         int64          `json:"group_id"`
	Strategy        string         `json:"strategy"`
	ClientProtocol  string         `json:"client_protocol"`
	Stream          bool           `json:"stream"`
	CandidateCount  int            `json:"candidate_count"`
	FinalCandidate  int            `json:"final_candidate"`
	FinalChannelID  int64          `json:"final_channel_id"`
	FinalModelID    int64          `json:"final_model_id"`
	Events          []RoutingEvent `gorm:"serializer:json" json:"events"`
	DroppedEvents   int            `json:"dropped_events"`
	StopReason      string         `json:"stop_reason"`
	StopErrKind     string         `json:"stop_err_kind"`
	Status          int            `json:"status"`
	Outcome         string         `json:"outcome"`
	DeliveryStarted bool           `json:"delivery_started"`
	HTTPAttempts    int            `json:"http_attempts"`
	StartedAt       time.Time      `gorm:"index" json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
}

// WriteRoutingTrace inserts once without changing usage or HTTP-attempt records.
func WriteRoutingTrace(trace *RoutingTrace) error {
	if trace == nil || LogDB == nil {
		return fmt.Errorf("路由诊断或日志库未初始化")
	}
	row := *trace
	if len(row.Events) > MaxRoutingEvents {
		row.DroppedEvents += len(row.Events) - MaxRoutingEvents
		row.Events = row.Events[:MaxRoutingEvents]
	}
	if row.Events == nil {
		row.Events = []RoutingEvent{}
	}
	// The gateway reports only fixed safe metadata on failure; suppress SQL and its values here.
	return LogDB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Create(&row).Error
}

// GetRoutingTrace uses exact request identity; absent historical diagnostics stay absent.
func GetRoutingTrace(requestID string) (*RoutingTrace, error) {
	var row RoutingTrace
	err := LogDB.Where("request_id = ?", requestID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func deleteOldRoutingTraces(target time.Time, limit int) error {
	// Older embedders can migrate only Log and UpstreamAttempt. Do not break their cleanup.
	if !LogDB.Migrator().HasTable(&RoutingTrace{}) {
		return nil
	}
	for {
		var ids []int64
		if err := LogDB.Model(&RoutingTrace{}).Where("started_at < ?", target).
			Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := LogDB.Where("id IN ?", ids).Delete(&RoutingTrace{}).Error; err != nil {
			return err
		}
		if len(ids) < limit {
			return nil
		}
	}
}
