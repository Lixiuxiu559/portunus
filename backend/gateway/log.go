package gateway

import (
	"unicode/utf8"

	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// logCall 写一条调用日志。callErr 非 nil 时把失败归因（类别 + 截断原文）一并落库，
// 让 status=0 / success=0 的日志能区分「用户取消」与「真故障」。
// clientProto 与 stats 是诊断遥测：前者区分 DSH(openai) 与 Claude Code(anthropic)
// 的流量，后者记录本次装配的桥接命中数与垫片改写数（见 protocol.ComposeStats）。
// 暂为同步写入（单条 SQLite insert 开销极小）；后续若成为瓶颈可改为队列异步落库。
// 写入动作由 Deps.LogWrite 注入（生产 LogDB.Create，测试内存收集）。
func (s *relayServer) logCall(apiKeyID int64, g *group.Group, t router.Target, clientProto protocol.Provider, stats protocol.ComposeStats, status int, success, stream bool, usage *protocol.Usage, durationMs, firstTokenMs int64, requestID string, callErr error, reports ...*streamDeliveryReport) {
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
		// 诊断遥测（见 shared.Log 同名字段注释）
		ClientProtocol:       string(clientProto),
		BridgeDecoded:        stats.BridgeDecoded,
		ShimRewrites:         stats.ShimRewrites,
		HostedToolsStripped:  stats.HostedToolsStripped,
		ConversionChecked:    stats.Compatibility.Checked,
		ConversionScope:      stats.Compatibility.Scope,
		ConversionCompatible: stats.Compatibility.Compatible,
		ConversionIssueCount: len(stats.Compatibility.Issues),
	}
	if len(reports) > 0 && reports[0] != nil {
		report := reports[0].ResponseCompatibility
		entry.ResponseConversionChecked = report.Checked
		entry.ResponseConversionScope = report.Scope
		entry.ResponseConversionCompatible = report.Compatible
		entry.ResponseConversionIssueCount = len(report.Issues)
	}
	if stream && len(reports) > 0 && reports[0] != nil {
		r := reports[0]
		entry.StreamTailLimited = r.TailLimited
		entry.StreamOutcome, entry.StreamReason = r.Outcome.Status, r.Outcome.Reason
		entry.StreamTerminalObserved = r.Outcome.TerminalObserved
		entry.StreamDeliveryStarted, entry.StreamTerminalWritten, entry.StreamWriteFailed = r.Started, r.TerminalWritten, r.WriteFailed
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
