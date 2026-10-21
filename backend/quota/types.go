// Package quota 查询渠道的上游余额与额度，不参与生成协议、路由或计费。
package quota

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"
)

type QueryOptions struct {
	UserID       string        `json:"user_id,omitempty"`
	QuotaPerUnit string        `json:"quota_per_unit,omitempty"`
	DisplayUnit  string        `json:"display_unit,omitempty"`
	Custom       *CustomConfig `json:"custom,omitempty"`
}

// CustomConfig 声明一次余额读取和有限字段映射，不包含可执行脚本。
type CustomConfig struct {
	Request CustomRequest    `json:"request"`
	Success *CustomCondition `json:"success,omitempty"`
	Items   []CustomItem     `json:"items"`
}
type CustomRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// EqualsJSON 保留 JSON 标量原文，避免前端 JSON Number 往返丢失精度。
type CustomCondition struct {
	Path       string `json:"path"`
	EqualsJSON string `json:"equals_json"`
}
type CustomItem struct {
	ID                     string           `json:"id"`
	Label                  string           `json:"label"`
	Kind                   string           `json:"kind"`
	Scope                  string           `json:"scope"`
	Unit                   Unit             `json:"unit"`
	RemainingPath          string           `json:"remaining_path,omitempty"`
	UsedPath               string           `json:"used_path,omitempty"`
	TotalPath              string           `json:"total_path,omitempty"`
	RemainingFromTotalUsed bool             `json:"remaining_from_total_used,omitempty"`
	Divisor                string           `json:"divisor,omitempty"`
	OriginalUnit           *Unit            `json:"original_unit,omitempty"`
	Unlimited              *CustomCondition `json:"unlimited,omitempty"`
}

type Secrets struct {
	Token string `json:"token,omitempty"`
}
type CredentialPatch struct {
	Token *string `json:"token,omitempty"`
	Clear bool    `json:"clear,omitempty"`
}

type Source struct {
	ID                 int64 `gorm:"primaryKey"`
	ChannelID          int64 `gorm:"not null;index"`
	Name               string
	AdapterID          string
	SchemaVersion      int
	ConfigRevision     int64
	Enabled            bool
	QueryBaseURL       string
	CredentialMode     string
	Credentials        Secrets      `gorm:"serializer:json" json:"-"`
	Options            QueryOptions `gorm:"serializer:json"`
	AutoRefreshMinutes int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (Source) TableName() string { return "channel_quota_sources" }

// SourceInput 用于创建和完整配置更新；秘密字段缺失保留，clear 显式清除。
type SourceInput struct {
	Name               string           `json:"name"`
	AdapterID          string           `json:"adapter_id"`
	SchemaVersion      int              `json:"schema_version"`
	Enabled            bool             `json:"enabled"`
	QueryBaseURL       string           `json:"query_base_url"`
	CredentialMode     string           `json:"credential_mode"`
	Credentials        *CredentialPatch `json:"credentials,omitempty"`
	Options            QueryOptions     `json:"options"`
	AutoRefreshMinutes int              `json:"auto_refresh_minutes"`
	ExpectedRevision   int64            `json:"expected_revision"`
}

type Unit struct {
	Kind string `json:"kind"`
	Code string `json:"code"`
}

// OriginalAmounts 保留换算前的上游额度，仅用于详情核对，不作为第二个账户汇总。
type OriginalAmounts struct {
	Unit      Unit    `json:"unit"`
	Remaining *string `json:"remaining,omitempty"`
	Used      *string `json:"used,omitempty"`
	Total     *string `json:"total,omitempty"`
}
type Item struct {
	ID              string           `json:"id"`
	Label           string           `json:"label"`
	Kind            string           `json:"kind"`
	Scope           string           `json:"scope"`
	Unit            Unit             `json:"unit"`
	Remaining       *string          `json:"remaining,omitempty"`
	Used            *string          `json:"used,omitempty"`
	Total           *string          `json:"total,omitempty"`
	UsedPercent     *float64         `json:"used_percent,omitempty"`
	LimitState      string           `json:"limit_state"`
	ResetsAt        *time.Time       `json:"resets_at,omitempty"`
	SourceUpdatedAt *time.Time       `json:"source_updated_at,omitempty"`
	AccountStatus   string           `json:"account_status,omitempty"`
	Original        *OriginalAmounts `json:"original,omitempty"`
}
type Snapshot struct {
	Items []Item `json:"items"`
}
type Failure struct {
	Code              string `json:"code"`
	Category          string `json:"category"`
	Message           string `json:"message"`
	Retryable         bool   `json:"retryable"`
	UpstreamStatus    int    `json:"upstream_status,omitempty"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
}

type State struct {
	SourceID              int64 `gorm:"primaryKey"`
	ConfigRevision        int64
	LastAttemptAt         *time.Time
	LastCompletedAt       *time.Time
	LastSuccessAt         *time.Time
	LastOutcome           string
	SnapshotSchemaVersion int
	Snapshot              *Snapshot `gorm:"serializer:json"`
	SnapshotUsable        bool
	Failure               *Failure `gorm:"serializer:json"`
	NextAllowedAt         *time.Time
	NextScheduledAt       *time.Time
	ConsecutiveFailures   int
}

func (State) TableName() string { return "channel_quota_states" }

type View struct {
	Status        string     `json:"status"`
	InProgress    bool       `json:"in_progress"`
	Snapshot      *Snapshot  `json:"snapshot,omitempty"`
	LastSnapshot  *Snapshot  `json:"last_snapshot,omitempty"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	Failure       *Failure   `json:"failure,omitempty"`
	NextAllowedAt *time.Time `json:"next_allowed_at,omitempty"`
}
type SourceView struct {
	ID                 int64        `json:"id"`
	ChannelID          int64        `json:"channel_id"`
	Name               string       `json:"name"`
	AdapterID          string       `json:"adapter_id"`
	SchemaVersion      int          `json:"schema_version"`
	ConfigRevision     int64        `json:"config_revision"`
	Enabled            bool         `json:"enabled"`
	QueryBaseURL       string       `json:"query_base_url"`
	CredentialMode     string       `json:"credential_mode"`
	HasToken           bool         `json:"has_token"`
	Options            QueryOptions `json:"options"`
	AutoRefreshMinutes int          `json:"auto_refresh_minutes"`
	State              View         `json:"state"`
}
type ChannelSummary struct {
	ChannelID int64        `json:"channel_id"`
	Sources   []SourceView `json:"sources"`
}
type PreviewInput struct {
	SourceInput
	ChannelID int64 `json:"channel_id"`
	SourceID  int64 `json:"source_id,omitempty"`
}

type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
}
type Descriptor struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	ProviderIDs     []string `json:"provider_ids"`
	DefaultBaseURL  string   `json:"default_base_url"`
	CredentialModes []string `json:"credential_modes"`
	Fields          []Field  `json:"fields"`
	SourceURL       string   `json:"source_url,omitempty"`
	RequestMethods  []string `json:"request_methods,omitempty"`
}
type ResolvedSource struct {
	AdapterID    string
	QueryBaseURL string
	Options      QueryOptions
	Token        string
}
type RequestSpec struct {
	URL     string
	Method  string
	Headers http.Header
	Body    []byte
}
type Adapter interface {
	Describe() Descriptor
	Validate(Source) error
	BuildRequest(ResolvedSource) (RequestSpec, error)
	Parse(ResolvedSource, http.Header, []byte) (*Snapshot, *Failure)
}

// ChannelLoader 必须在提供的事务中读取；不存在时返回 gorm.ErrRecordNotFound。
type ChannelLoader func(tx *gorm.DB, id int64) (key string, err error)
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Executor 可注入本地测试；生产使用共用执行器，不在适配器中请求网络。
type Executor func(context.Context, HTTPDoer, Adapter, ResolvedSource) (*Snapshot, *Failure)
