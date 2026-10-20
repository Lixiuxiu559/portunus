package model

import (
	"encoding/json"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func setupCreateTest(t *testing.T) *channel.Channel {
	t.Helper()
	setupSyncTest(t)
	c := channel.Channel{Name: "c", Type: protocol.ProviderOpenAI, BaseURL: "http://x", Key: "k"}
	if err := shared.DB.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestCreateDoesNotApplyRetiredPrices(t *testing.T) {
	c := setupCreateTest(t)
	for _, name := range []string{"gpt-4o", "deepseek-v4-pro", "custom"} {
		m, err := Create(CreateRequest{ChannelID: c.ID, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if m.InputPrice != 0 || m.OutputPrice != 0 || m.CacheReadPrice != 0 || m.CacheWritePrice != 0 {
			t.Fatalf("新模型不应设置默认价格: %+v", m)
		}
		// 仅保持旧数据库列定义，USD 不再有计价语义。
		if m.Currency != CurrencyUSD {
			t.Fatalf("历史列定义发生变化: %+v", m)
		}
	}
}

func TestUpdatePreservesRetiredPrices(t *testing.T) {
	c := setupCreateTest(t)
	old := Model{ChannelID: c.ID, Name: "legacy", Currency: CurrencyCNY, InputPrice: 1.25, OutputPrice: 9.5, CacheReadPrice: .4, CacheWritePrice: 2.3}
	if err := shared.DB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.AutoMigrate(&Model{}); err != nil {
		t.Fatal(err)
	}
	other := channel.Channel{Name: "other", Type: protocol.ProviderOpenAI, BaseURL: "http://y", Key: "k"}
	if err := shared.DB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	var req UpdateRequest
	// 旧客户端可继续发额外字段，但不能修改已退役字段，类型也不再参与校验。
	if err := json.Unmarshal([]byte(`{"name":"renamed","currency":"USD","input_price":0,"output_price":999,"cache_read_price":null,"cache_write_price":"obsolete"}`), &req); err != nil {
		t.Fatal(err)
	}
	req.ChannelID = &other.ID
	updated, err := Update(old.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed" || updated.ChannelID != other.ID {
		t.Fatalf("名称/渠道未更新: %+v", updated)
	}
	var stored Model
	if err := shared.DB.First(&stored, old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Currency != old.Currency || stored.InputPrice != old.InputPrice || stored.OutputPrice != old.OutputPrice || stored.CacheReadPrice != old.CacheReadPrice || stored.CacheWritePrice != old.CacheWritePrice {
		t.Fatalf("存量价格被改写: %+v", stored)
	}
	for _, value := range []any{stored, stored.ToResponse()} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"currency", "input_price", "output_price", "cache_read_price", "cache_write_price"} {
			if _, exists := fields[field]; exists {
				t.Fatalf("模型响应暴露历史字段 %s", field)
			}
		}
	}
}
