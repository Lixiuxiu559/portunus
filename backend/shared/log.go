package shared

import (
	"time"
)

// 本文件是调用日志的实体与失败归因词表（写入方 gateway 经 Deps.LogWrite 注入，
// 读方是管理端 api/log.go 的查询路由）。查询 / 统计 / 清理在 log_query.go。

// Log 记录一次对外 LLM 调用的完整信息，供调用诊断与用量统计。
type Log struct {
	ID              int64   `gorm:"primaryKey" json:"id"`
	APIKeyID        int64   `gorm:"index" json:"api_key_id"`
	GroupName       string  `gorm:"index" json:"group_name"`
	ChannelID       int64   `gorm:"index" json:"channel_id"`
	ModelName       string  `gorm:"index" json:"model_name"`
	Currency        string  `gorm:"size:8" json:"-"` // 历史费用币种，只保留存量数据
	Status          int     `json:"status"`          // 上游返回的 HTTP 状态码
	Success         bool    `gorm:"index" json:"success"`
	Stream          bool    `gorm:"index" json:"stream"`                       // 是否流式请求（按客户端请求的 stream 参数记录，与首包是否到达无关）
	RequestID       string  `gorm:"size:32;index" json:"request_id,omitempty"` // 请求关联 ID：同一次客户端请求的所有上游尝试共享，并以 X-Request-Id 透传上游，跨网关对账时以此对齐
	InputToken      int64   `json:"input_token"`
	OutputToken     int64   `json:"output_token"`
	CacheReadToken  int64   `json:"cache_read_token"`
	CacheWriteToken int64   `json:"cache_write_token"`
	Cost            float64 `json:"-"`
	DurationMs      int64   `json:"duration_ms"`
	FirstTokenMs    int64   `json:"first_token_ms"`     // 流式首包耗时（客户端 TTFT），非流式为 0
	ErrKind         string  `json:"err_kind,omitempty"` // 失败类别（取值见下方 ErrKind 词表；判定归 gateway.failSpec 的失败处置），成功为空；无查询路径暂不建索引
	ErrMsg          string  `json:"err_msg,omitempty"`  // 失败原文（截断），成功为空
	// ── 诊断遥测：把「缓存为什么没命中」从猜测变成可测量 ──────────────────
	// 写入方是 gateway（值来自 protocol.ComposeStats 与客户端入口协议）。
	ClientProtocol string `gorm:"size:16;index" json:"client_protocol,omitempty"` // 客户端入口协议（openai / anthropic / openai_responses / gemini）；空 = 本次改动前的存量日志，未记录
	BridgeDecoded  int    `json:"bridge_decoded"`                                 // reasoning 桥从 thinking 块签名还原原文的次数。anthropic 入向恒为 0 而请求里确有思考块 → 客户端没回传本网关的签名，桥接未生效（见 protocol/reasoning_bridge.go）
	ShimRewrites   int    `json:"shim_rewrites"`                                  // thinking 兼容垫片改写的 assistant 消息条数，即被改写的前缀面积（见 protocol/thinking_compat.go）
	// HostedToolsStripped 是被剥离的托管工具声明计数：发往不支持 hosted 工具的
	// openai 上游时剔除的声明数，加上 canonical 无法承载、入站即丢弃的未知
	// hosted 类型数（见 protocol.ComposeStats）。>0 说明该请求的搜索类能力在
	// 上游侧静默降级，客户端收到的 server_tool_use 将无对应执行。
	HostedToolsStripped int `json:"hosted_tools_stripped"`
	// 仅保存请求侧协议兼容检查摘要，不保存原生扩展内容。Checked=false 表示未采集，非兼容失败。
	ConversionPolicy     string `gorm:"size:16" json:"-"` // 退役策略列，仅保留历史值；新日志不写入、不输出
	ConversionScope      string `gorm:"size:32" json:"conversion_scope,omitempty"`
	ConversionChecked    bool   `json:"conversion_checked"`
	ConversionCompatible bool   `json:"conversion_compatible"`
	ConversionIssueCount int    `json:"conversion_issue_count"`
	// 响应侧摘要独立于请求侧检查，不保存原生字段、响应引用或正文。
	ResponseConversionChecked    bool      `json:"response_conversion_checked"`
	ResponseConversionScope      string    `gorm:"size:32" json:"response_conversion_scope,omitempty"`
	ResponseConversionCompatible bool      `json:"response_conversion_compatible"`
	ResponseConversionIssueCount int       `json:"response_conversion_issue_count"`
	StreamOutcome                string    `gorm:"size:16" json:"stream_outcome,omitempty"`
	StreamReason                 string    `gorm:"size:48" json:"stream_reason,omitempty"`
	StreamTerminalObserved       bool      `json:"stream_terminal_observed"`
	StreamDeliveryStarted        bool      `json:"stream_delivery_started"`
	StreamTerminalWritten        bool      `json:"stream_terminal_written"`
	StreamTailLimited            bool      `json:"stream_tail_limited"`
	StreamWriteFailed            bool      `json:"stream_write_failed"`
	CreatedAt                    time.Time `gorm:"index" json:"created_at"`
}

// ── 失败归因词表（Log.ErrKind 取值），前端与排障按此过滤 ──────────────────
// 只持有取值；错误 → 类别的判定归 gateway.failSpec（失败处置模块，唯一权威）。

const (
	ErrKindResourceLimit        = "resource_limit"
	ErrKindChannelCapacity      = "channel_capacity"
	ErrKindAttemptBudget        = "attempt_budget_exhausted"
	ErrKindFirstContentDeadline = "first_content_deadline"
	ErrKindNonStreamDeadline    = "non_stream_deadline"
	ErrKindClientWrite          = "client_write"        // 客户端写入或 Flush 失败，不计渠道故障
	ErrKindClientCancel         = "client_cancel"       // 客户端主动断开或其自身期限到达
	ErrKindUpstream             = "upstream_error"      // 上游返回非 2xx
	ErrKindWatchdog             = "watchdog_timeout"    // 上游流静默超时被看门狗掐断
	ErrKindStreamInterrupt      = "stream_interrupted"  // 流已提交后中途断掉，无法 failover
	ErrKindConvert              = "convert_error"       // 协议转换 / 请求改写失败
	ErrKindNetwork              = "network"             // 连接 / 超时等网络层失败
	ErrKindInternal             = "internal"            // 其余未归类失败
	ErrKindCircuitOpen          = "circuit_open"        // 分组全部目标熔断开路，未打上游直接 503
	ErrKindRateLimitCooldown    = "rate_limit_cooldown" // 本地限流冷却跳过，未发送HTTP
)
