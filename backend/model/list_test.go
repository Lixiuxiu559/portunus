package model

import (
	"fmt"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func setupListTest(t *testing.T) (*channel.Channel, *channel.Channel) {
	setupSyncTest(t)

	c1 := channel.Channel{Name: "c1", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	c2 := channel.Channel{Name: "c2", Type: protocol.ProviderOpenAI, BaseURL: "http://y", Key: "k"}
	if err := shared.DB.Create(&c1).Error; err != nil {
		t.Fatalf("建渠道 c1 失败: %v", err)
	}
	if err := shared.DB.Create(&c2).Error; err != nil {
		t.Fatalf("建渠道 c2 失败: %v", err)
	}

	// c1 下 25 个模型，c2 下 3 个
	for i := 1; i <= 25; i++ {
		m := Model{ChannelID: c1.ID, Name: fmt.Sprintf("model-%02d", i)}
		if err := shared.DB.Create(&m).Error; err != nil {
			t.Fatalf("建模型失败: %v", err)
		}
	}
	for _, name := range []string{"gpt-4o", "gpt-4o-mini", "claude-sonnet"} {
		m := Model{ChannelID: c2.ID, Name: name}
		if err := shared.DB.Create(&m).Error; err != nil {
			t.Fatalf("建模型失败: %v", err)
		}
	}
	return &c1, &c2
}

func TestListPagination(t *testing.T) {
	c1, _ := setupListTest(t)

	// 第 1 页 20 条，total 28
	ms, total, err := List(0, "", 1, 20)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if total != 28 {
		t.Fatalf("total = %d, want 28", total)
	}
	if len(ms) != 20 {
		t.Fatalf("第 1 页条数 = %d, want 20", len(ms))
	}

	// 第 2 页 8 条
	ms2, total2, err := List(0, "", 2, 20)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if total2 != 28 || len(ms2) != 8 {
		t.Fatalf("第 2 页 total=%d len=%d, want 28/8", total2, len(ms2))
	}

	// 按渠道过滤 + 分页
	ms3, total3, err := List(c1.ID, "", 1, 10)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if total3 != 25 || len(ms3) != 10 {
		t.Fatalf("渠道过滤 total=%d len=%d, want 25/10", total3, len(ms3))
	}
	ms4, _, err := List(c1.ID, "", 3, 10)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(ms4) != 5 {
		t.Fatalf("渠道过滤第 3 页 len = %d, want 5", len(ms4))
	}

	// 名称过滤 + 分页
	_, total5, err := List(0, "gpt", 1, 10)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if total5 != 2 {
		t.Fatalf("名称过滤 total = %d, want 2", total5)
	}
}
