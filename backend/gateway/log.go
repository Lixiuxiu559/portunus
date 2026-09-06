package gateway

import (
	"unicode/utf8"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// logCall 写一条调用日志。callErr 非 nil 时把失败归因（类别 + 截断原文）一并落库，
// 让 status=0 / success=0 的日志能区分「用户取消」与「真故障」。
// 暂为同步写入（单条 SQLite insert 开销极小）；后续若成为瓶颈可改为队列异步落库。
// 写入动作由 Deps.LogWrite 注入（生产 LogDB.Create，测试内存收集）。
func (s *relayServer) logCall(apiKeyID int64, g *group.Group, t router.Target, status int, success, stream bool, usage *protocol.Usage, durationMs, firstTokenMs int64, requestID string, callErr error) {
	entry := shared.Log{
		APIKeyID:     apiKeyID,
		GroupName:    g.Name,
		ChannelID:    t.Channel.ID,
		ModelName:    t.Model.Name,
		Status:       status,
		Success:      success,
		Stream:       stream,
		RequestID:    requestID,
		DurationMs:   durationMs,
		FirstTokenMs: firstTokenMs,
	}
	if callErr != nil {
		entry.ErrKind = failSpec(callErr).ErrKind
		entry.ErrMsg = truncateErr(callErr.Error(), 256)
	}
	if usage != nil {
		entry.InputToken = int64(usage.PromptTokens)
		entry.OutputToken = int64(usage.CompletionTokens)
		entry.CacheReadToken = int64(usage.CacheReadTokens)
		entry.CacheWriteToken = int64(usage.CacheWriteTokens)
		entry.Cost = computeCost(usage, t.Model)
	}
	s.deps.LogWrite(&entry)
}

// truncateErr 把错误信息截断到不超过 max 字节（含 3 字节省略号，末尾不落在
// 多字节字符中间），短于上限的原文原样保留。只保证边界完整，不要求整串
// 合法 UTF-8——中部坏字节原样保留（garbage in, garbage out），不从首个坏
// 字节起整段丢弃。
func truncateErr(s string, max int) string {
	const ellipsis = "…"
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	withEllipsis := false
	if max > len(ellipsis) {
		cut = s[:max-len(ellipsis)] // 预留省略号空间，保证总长不超 max
		withEllipsis = true
	}
	// 末尾回退到完整 rune 边界（多字节序列最长 4 字节，最多回退 3 次）
	for i := 0; i < 3 && len(cut) > 0; i++ {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || size > 1 {
			break // 末尾已是完整 rune（含合法的 U+FFFD）
		}
		cut = cut[:len(cut)-1]
	}
	if len(cut) == 0 {
		return ""
	}
	if withEllipsis {
		return cut + ellipsis
	}
	return cut
}

// computeCost 按四维价格计算一次调用费用（价格单位：每 1M token）。
func computeCost(usage *protocol.Usage, m model.Model) float64 {
	return (float64(usage.PromptTokens)*m.InputPrice +
		float64(usage.CompletionTokens)*m.OutputPrice +
		float64(usage.CacheReadTokens)*m.CacheReadPrice +
		float64(usage.CacheWriteTokens)*m.CacheWritePrice) / 1e6
}
