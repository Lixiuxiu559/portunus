package shared

import (
	"time"
)

// 本文件是调用日志的实体与失败归因词表（写入方 gateway 经 Deps.LogWrite 注入，
// 读方是管理端 api/log.go 的查询路由）。查询 / 统计 / 清理在 log_query.go。

// Log 记录一次对外 LLM 调用的完整信息，供日志查询与费用统计。
type Log struct {
	ID              int64     `gorm:"primaryKey" json:"id"`
	APIKeyID        int64     `gorm:"index" json:"api_key_id"`
	GroupName       string    `gorm:"index" json:"group_name"`
	ChannelID       int64     `gorm:"index" json:"channel_id"`
	ModelName       string    `gorm:"index" json:"model_name"`
	Status          int       `json:"status"` // 上游返回的 HTTP 状态码
	Success         bool      `gorm:"index" json:"success"`
	Stream          bool      `gorm:"index" json:"stream"`                       // 是否流式请求（按客户端请求的 stream 参数记录，与首包是否到达无关）
	RequestID       string    `gorm:"size:32;index" json:"request_id,omitempty"` // 请求关联 ID：同一次客户端请求的所有上游尝试共享，并以 X-Request-Id 透传上游，跨网关对账时以此对齐
	InputToken      int64     `json:"input_token"`
	OutputToken     int64     `json:"output_token"`
	CacheReadToken  int64     `json:"cache_read_token"`
	CacheWriteToken int64     `json:"cache_write_token"`
	Cost            float64   `json:"cost"`
	DurationMs      int64     `json:"duration_ms"`
	FirstTokenMs    int64     `json:"first_token_ms"`     // 流式首包耗时（客户端 TTFT），非流式为 0
	ErrKind         string    `json:"err_kind,omitempty"` // 失败类别（取值见下方 ErrKind 词表；判定归 gateway.failSpec 的失败处置），成功为空；无查询路径暂不建索引
	ErrMsg          string    `json:"err_msg,omitempty"`  // 失败原文（截断），成功为空
	CreatedAt       time.Time `gorm:"index" json:"created_at"`
}

// ── 失败归因词表（Log.ErrKind 取值），前端与排障按此过滤 ──────────────────
// 只持有取值；错误 → 类别的判定归 gateway.failSpec（失败处置模块，唯一权威）。

const (
	ErrKindClientCancel    = "client_cancel"      // 客户端主动断开（Esc / 关窗口）
	ErrKindUpstream        = "upstream_error"     // 上游返回非 2xx
	ErrKindWatchdog        = "watchdog_timeout"   // 上游流静默超时被看门狗掐断
	ErrKindStreamInterrupt = "stream_interrupted" // 流已提交后中途断掉，无法 failover
	ErrKindConvert         = "convert_error"      // 协议转换 / 请求改写失败
	ErrKindNetwork         = "network"            // 连接 / 超时等网络层失败
	ErrKindInternal        = "internal"           // 其余未归类失败
	ErrKindCircuitOpen     = "circuit_open"       // 分组全部目标熔断开路，未打上游直接 503
)
