package gateway

import (
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/router"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// logCall 写一条调用日志。
// 暂为同步写入（单条 SQLite insert 开销极小）；后续若成为瓶颈可改为队列异步落库。
func logCall(apiKeyID int64, g *group.Group, t router.Target, status int, success bool, usage *protocol.Usage, durationMs, firstTokenMs int64) {
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
	if usage != nil {
		entry.InputToken = int64(usage.PromptTokens)
		entry.OutputToken = int64(usage.CompletionTokens)
		entry.CacheReadToken = int64(usage.CacheReadTokens)
		entry.CacheWriteToken = int64(usage.CacheWriteTokens)
		entry.Cost = computeCost(usage, t.Model)
	}
	shared.LogDB.Create(&entry)
}

// computeCost 按四维价格计算一次调用费用（价格单位：每 1M token）。
func computeCost(usage *protocol.Usage, m model.Model) float64 {
	return (float64(usage.PromptTokens)*m.InputPrice +
		float64(usage.CompletionTokens)*m.OutputPrice +
		float64(usage.CacheReadTokens)*m.CacheReadPrice +
		float64(usage.CacheWriteTokens)*m.CacheWritePrice) / 1e6
}
