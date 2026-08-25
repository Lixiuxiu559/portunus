package gateway

import (
	"github.com/Lixiuxiu559/portunus/backend/group"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// logCall 写一条调用日志。
// 暂为同步写入（单条 SQLite insert 开销极小）；后续若成为瓶颈可改为队列异步落库。
func logCall(apiKeyID int64, g *group.Group, t target, status int, success bool, usage *protocol.Usage, durationMs int64) {
	entry := shared.Log{
		APIKeyID:   apiKeyID,
		GroupName:  g.Name,
		ChannelID:  t.channel.ID,
		ModelName:  t.model.Name,
		Status:     status,
		Success:    success,
		DurationMs: durationMs,
	}
	if usage != nil {
		entry.InputToken = int64(usage.PromptTokens)
		entry.OutputToken = int64(usage.CompletionTokens)
		entry.Cost = computeCost(entry.InputToken, entry.OutputToken, t.model.InputPrice, t.model.OutputPrice)
	}
	shared.DB.Create(&entry)
}

// computeCost 按模型价格计算一次调用费用（价格单位：每 1M token）。
func computeCost(inputToken, outputToken int64, inputPrice, outputPrice float64) float64 {
	return (float64(inputToken)*inputPrice + float64(outputToken)*outputPrice) / 1e6
}
