package gateway

import "testing"

func TestComputeCost(t *testing.T) {
	// 1M 输入 + 1M 输出，价格 2.5 / 10 → (1e6*2.5 + 1e6*10)/1e6 = 12.5
	if cost := computeCost(1000000, 1000000, 2.5, 10); cost != 12.5 {
		t.Errorf("computeCost = %f, want 12.5", cost)
	}
	if cost := computeCost(0, 0, 2.5, 10); cost != 0 {
		t.Errorf("空用量应为 0, got %f", cost)
	}
}
