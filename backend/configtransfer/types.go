// Package configtransfer implements the version 1, offline configuration transfer boundary.
package configtransfer

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

const (
	FormatVersion           = 1
	MaxPlaintextBytes       = 32 * 1024 * 1024
	MaxFileBytes            = MaxPlaintextBytes + 64
	MaxMultipartBytes       = MaxPlaintextBytes + 16*1024
	MaxSmallBodyBytes       = 8 * 1024
	MaxPasswordBytes        = 1024
	MaxChannels             = 1000
	MaxModels               = 50000
	MaxGroups               = 5000
	MaxItems                = 100000
	MaxPreviewBytes         = 8 * 1024 * 1024
	MaxResultBytes          = 12 * 1024 * 1024
	MaxNameBytes            = 16 * 1024
	MaxSafeID         int64 = 9007199254740991
	PreviewTTL              = 15 * time.Minute
)

type Counts struct {
	Channels int `json:"channels"`
	Models   int `json:"models"`
	Groups   int `json:"groups"`
}

func (c Counts) Empty() bool { return c.Channels == 0 && c.Models == 0 && c.Groups == 0 }

type Payload struct {
	FormatVersion int       `json:"format_version"`
	AppVersion    string    `json:"app_version"`
	Channels      []Channel `json:"channels"`
	Models        []Model   `json:"models"`
	Groups        []Group   `json:"groups"`
}
type Channel struct {
	Ref                   string           `json:"ref"`
	Name                  string           `json:"name"`
	Icon                  string           `json:"icon"`
	ProviderID            string           `json:"provider_id"`
	ProfileID             string           `json:"profile_id"`
	AdapterID             string           `json:"adapter_id"`
	Type                  string           `json:"type"`
	BaseURL               string           `json:"base_url"`
	Key                   string           `json:"key"`
	ConnectionOptions     upstream.Options `json:"connection_options"`
	AutoSync              bool             `json:"auto_sync"`
	ThinkingCompat        bool             `json:"thinking_compat"`
	MaxConcurrentRequests int64            `json:"max_concurrent_requests"`
	RateLimitScope        string           `json:"rate_limit_scope"`
}
type Model struct {
	Ref        string `json:"ref"`
	ChannelRef string `json:"channel_ref"`
	Name       string `json:"name"`
}
type Group struct {
	Ref           string  `json:"ref"`
	Name          string  `json:"name"`
	Strategy      string  `json:"strategy"`
	ActiveItemRef *string `json:"active_item_ref"`
	Items         []Item  `json:"items"`
}
type Item struct {
	Ref      string `json:"ref"`
	ModelRef string `json:"model_ref"`
	Priority string `json:"priority"`
}

func (p *Payload) Counts() Counts { return Counts{len(p.Channels), len(p.Models), len(p.Groups)} }

type FileInfo struct {
	FormatVersion int    `json:"format_version"`
	AppVersion    string `json:"app_version"`
}
type PreviewChannel struct {
	Ref          string `json:"ref"`
	OriginalName string `json:"original_name"`
	Name         string `json:"name"`
	ModelCount   int    `json:"model_count"`
	HasKey       bool   `json:"has_key"`
	Type         string `json:"type"`
}
type PreviewItem struct {
	Ref      string `json:"ref"`
	ModelRef string `json:"model_ref"`
	Priority string `json:"priority"`
	Position int    `json:"position"`
}
type PreviewGroup struct {
	Ref           string        `json:"ref"`
	OriginalName  string        `json:"original_name"`
	Name          string        `json:"name"`
	Strategy      string        `json:"strategy"`
	ActiveItemRef *string       `json:"active_item_ref"`
	Items         []PreviewItem `json:"items"`
}
type PreviewDTO struct {
	PreviewID   string           `json:"preview_id"`
	OperationID string           `json:"operation_id"`
	FileSHA256  string           `json:"file_sha256"`
	PlanSHA256  string           `json:"plan_sha256"`
	ExpiresAt   string           `json:"expires_at"`
	FileInfo    FileInfo         `json:"file_info"`
	Counts      Counts           `json:"counts"`
	RenameCount int              `json:"rename_count"`
	Channels    []PreviewChannel `json:"channels"`
	Models      []Model          `json:"models"`
	Groups      []PreviewGroup   `json:"groups"`
}
type ResultName struct {
	Ref          string `json:"ref"`
	ID           string `json:"id"`
	OriginalName string `json:"original_name"`
	Name         string `json:"name"`
}
type ResultDTO struct {
	OperationID   string       `json:"operation_id"`
	State         string       `json:"state"`
	CommittedAt   string       `json:"committed_at"`
	ResultVersion int          `json:"result_version"`
	Counts        Counts       `json:"counts"`
	RenameCount   int          `json:"rename_count"`
	Channels      []ResultName `json:"channels"`
	Groups        []ResultName `json:"groups"`
	Replayed      bool         `json:"replayed"`
}
type StateDTO struct {
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	Counts      Counts `json:"counts"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	FailureCode string `json:"failure_code,omitempty"`
	Stage       string `json:"stage,omitempty"`
}
type RecoverySummary struct {
	OperationID string `json:"operation_id"`
	State       string `json:"state"`
	CreatedAt   string `json:"created_at"`
	ConfirmedAt string `json:"confirmed_at"`
	Counts      Counts `json:"counts"`
	RenameCount int    `json:"rename_count"`
	FailureCode string `json:"failure_code,omitempty"`
}
type RecoveryList struct {
	Data       []RecoverySummary `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
}
type CommitRequest struct {
	PreviewID  string `json:"preview_id"`
	FileSHA256 string `json:"file_sha256"`
	PlanSHA256 string `json:"plan_sha256"`
}
type Summary struct {
	Counts        Counts         `json:"counts"`
	CanExport     bool           `json:"can_export"`
	FormatVersion int            `json:"format_version"`
	Limits        map[string]int `json:"limits"`
	InstanceID    string         `json:"instance_id"`
	Ready         bool           `json:"ready"`
}

// Error deliberately contains no underlying parser, SQL, URL or credential text.
type Error struct {
	Message            string      `json:"error"`
	Code               string      `json:"code"`
	Stage              string      `json:"stage"`
	Retryable          bool        `json:"retryable"`
	Field              string      `json:"field,omitempty"`
	OperationID        string      `json:"operation_id,omitempty"`
	ReplacementPreview *PreviewDTO `json:"replacement_preview,omitempty"`
	Status             int         `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Stage }
func NewError(code, stage string) *Error {
	status, message, retry := http.StatusInternalServerError, "配置迁移失败", false
	switch code {
	case "INVALID_INPUT":
		status, message = 400, "输入字段或编码无效"
	case "LIMIT_EXCEEDED":
		status, message = 413, "配置或文件超过受理上限"
	case "EMPTY_CONFIGURATION":
		status, message = 409, "没有可迁移的配置"
	case "FORMAT_UNSUPPORTED":
		status, message = 422, "文件格式不受支持，本应用仅支持文件格式 1"
	case "CRYPTO_PROFILE_UNSUPPORTED":
		status, message = 422, "文件加密方式不受支持"
	case "DECRYPT_FAILED":
		status, message = 422, "无法解密，请检查密码或文件"
	case "FILE_CORRUPT":
		status, message = 422, "文件不可读或不完整"
	case "CONFIG_INVALID":
		status, message = 422, "配置字段、能力或关联无效"
	case "SOURCE_INTEGRITY_ERROR":
		status, message = 409, "源配置不完整，无法导出"
	case "NAME_SPACE_EXHAUSTED":
		status, message = 409, "无法生成可用名称，请调整同名配置后重试"
	case "UNSUPPORTED_DATABASE_SCHEMA":
		status, message = 503, "数据库结构不支持配置迁移"
	case "PREVIEW_CHANGED":
		status, message = 409, "预览名称已被占用，请核对新预览后再次确认"
	case "PREVIEW_EXPIRED":
		status, message = 410, "预览已失效，请重新预览"
	case "OPERATION_RUNNING":
		status, message, retry = 202, "正在处理本次导入，请查询同一操作", true
	case "TRANSFER_BUSY":
		status, message, retry = 423, "正在处理另一项配置迁移，请稍后重试", true
	case "OPERATION_MISMATCH":
		status, message = 409, "操作与预览不匹配"
	case "OPERATION_ABORTED":
		status, message = 409, "本次操作已终止，请核实原结果"
	case "DB_BUSY":
		status, message, retry = 503, "数据库正忙，请稍后核实同一操作", true
	case "TRANSFER_UNAVAILABLE":
		status, message, retry = 503, "配置迁移尚未就绪，请稍后重试", true
	case "BACKEND_CHANGED":
		status, message = 409, "后端实例已变化，请重新连接"
	case "RESULT_UNCONFIRMED":
		status, message, retry = 503, "结果待核实，请继续查询同一操作", true
	case "NOT_FOUND":
		status, message = 404, "未找到该操作，不能据此判断导入结果"
	case "IMPORT_FAILED":
		status, message = 409, "导入失败，已全部回滚"
	}
	return &Error{Message: message, Code: code, Stage: stage, Retryable: retry, Status: status}
}
func encodedSize(v any) (int, error) { b, err := json.Marshal(v); return len(b), err }
func timestamp(ms int64) string      { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
