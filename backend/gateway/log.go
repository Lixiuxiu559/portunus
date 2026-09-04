package gateway

import (
	"context"
	"errors"
	"net"
	"unicode/utf8"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// 失败归因的固定类别（shared.Log.ErrKind 取值），前端与排障按此过滤。
const (
	errKindClientCancel    = "client_cancel"      // 客户端主动断开（Esc / 关窗口）
	errKindUpstream        = "upstream_error"     // 上游返回非 2xx
	errKindWatchdog        = "watchdog_timeout"   // 上游流静默超时被看门狗掐断
	errKindStreamInterrupt = "stream_interrupted" // 流已提交后中途断掉，无法 failover
	errKindConvert         = "convert_error"      // 协议转换 / 请求改写失败
	errKindNetwork         = "network"            // 连接 / 超时等网络层失败
	errKindInternal        = "internal"           // 其余未归类失败
)

// logCall 写一条调用日志。callErr 非 nil 时把失败归因（类别 + 截断原文）一并落库，
// 让 status=0 / success=0 的日志能区分「用户取消」与「真故障」。
// 暂为同步写入（单条 SQLite insert 开销极小）；后续若成为瓶颈可改为队列异步落库。
func logCall(apiKeyID int64, g *group.Group, t router.Target, status int, success bool, usage *protocol.Usage, durationMs, firstTokenMs int64, callErr error) {
	entry := shared.Log{
		APIKeyID:     apiKeyID,
		GroupName:    g.Name,
		ChannelID:    t.Channel.ID,
		ModelName:    t.Model.Name,
		Status:       status,
		Success:      success,
		DurationMs:   durationMs,
		FirstTokenMs: firstTokenMs,
	}
	if callErr != nil {
		entry.ErrKind = classifyErr(callErr)
		entry.ErrMsg = truncateErr(callErr.Error(), 256)
	}
	if usage != nil {
		entry.InputToken = int64(usage.PromptTokens)
		entry.OutputToken = int64(usage.CompletionTokens)
		entry.CacheReadToken = int64(usage.CacheReadTokens)
		entry.CacheWriteToken = int64(usage.CacheWriteTokens)
		entry.Cost = computeCost(usage, t.Model)
	}
	shared.LogDB.Create(&entry)
}

// classifyErr 把失败原因归为固定类别。判定顺序有讲究：客户端取消最先
// （committed 包装的 Canceled 也是用户行为，不是渠道故障）；之后按失败阶段
// 从流中断到网络逐层判定，兜底 internal。
func classifyErr(err error) string {
	if err == nil {
		return ""
	}
	var (
		committed *streamCommittedError
		stall     *streamStallError
		statusErr *upstreamStatusError
		convErr   *convertError
		netErr    net.Error
	)
	switch {
	case errors.Is(err, context.Canceled):
		return errKindClientCancel
	case errors.As(err, &committed):
		return errKindStreamInterrupt
	case errors.As(err, &stall):
		return errKindWatchdog
	case errors.As(err, &statusErr):
		return errKindUpstream
	case errors.As(err, &convErr):
		return errKindConvert
	case errors.As(err, &netErr):
		return errKindNetwork
	default:
		return errKindInternal
	}
}

// truncateErr 把错误信息截断到不超过 max 字节（含 3 字节省略号，多字节字符
// 不被切半），短于上限的原文原样保留。
func truncateErr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "…"
	cut := s[:max-len(ellipsis)] // 预留省略号空间，保证总长不超 max
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1] // 尾部切在多字节字符中间，回退到合法边界
	}
	if len(cut) == 0 {
		return ""
	}
	return cut + ellipsis
}

// computeCost 按四维价格计算一次调用费用（价格单位：每 1M token）。
func computeCost(usage *protocol.Usage, m model.Model) float64 {
	return (float64(usage.PromptTokens)*m.InputPrice +
		float64(usage.CompletionTokens)*m.OutputPrice +
		float64(usage.CacheReadTokens)*m.CacheReadPrice +
		float64(usage.CacheWriteTokens)*m.CacheWritePrice) / 1e6
}
