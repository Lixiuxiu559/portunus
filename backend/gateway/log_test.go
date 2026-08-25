package gateway

import (
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

func TestComputeCost(t *testing.T) {
	m := model.Model{InputPrice: 2.5, OutputPrice: 10, CacheReadPrice: 1.25, CacheWritePrice: 2.5}

	// 1M 输入 + 1M 输出 → (1e6*2.5 + 1e6*10)/1e6 = 12.5
	u := &protocol.Usage{PromptTokens: 1000000, CompletionTokens: 1000000}
	if cost := computeCost(u, m); cost != 12.5 {
		t.Errorf("computeCost = %f, want 12.5", cost)
	}

	// 加上缓存读/写 → 12.5 + (1e6*1.25 + 1e6*2.5)/1e6 = 16.25
	u = &protocol.Usage{PromptTokens: 1000000, CompletionTokens: 1000000, CacheReadTokens: 1000000, CacheWriteTokens: 1000000}
	if cost := computeCost(u, m); cost != 16.25 {
		t.Errorf("computeCost with cache = %f, want 16.25", cost)
	}

	// 空用量 → 0
	if cost := computeCost(&protocol.Usage{}, m); cost != 0 {
		t.Errorf("空用量应为 0, got %f", cost)
	}
}
