package model

import (
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
		t.Fatalf("建渠道失败: %v", err)
	}
	return &c
}

func TestCreateCurrency(t *testing.T) {
	c := setupCreateTest(t)

	// 缺省货币 → USD
	m1, err := Create(CreateRequest{ChannelID: c.ID, Name: "m-default"})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if m1.Currency != CurrencyUSD {
		t.Errorf("缺省货币 = %q, want USD", m1.Currency)
	}

	// 显式 CNY
	m2, err := Create(CreateRequest{ChannelID: c.ID, Name: "m-cny", Currency: CurrencyCNY})
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if m2.Currency != CurrencyCNY {
		t.Errorf("货币 = %q, want CNY", m2.Currency)
	}

	// 非法货币拒绝
	if _, err := Create(CreateRequest{ChannelID: c.ID, Name: "m-bad", Currency: "EUR"}); err != ErrInvalid {
		t.Errorf("非法货币应返回 ErrInvalid, got %v", err)
	}

	// Update 改货币
	cny := CurrencyCNY
	m3, err := Update(m1.ID, UpdateRequest{Currency: &cny})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if m3.Currency != CurrencyCNY {
		t.Errorf("更新后货币 = %q, want CNY", m3.Currency)
	}

	// Update 非法货币拒绝
	bad := Currency("JPY")
	if _, err := Update(m1.ID, UpdateRequest{Currency: &bad}); err != ErrInvalid {
		t.Errorf("更新非法货币应返回 ErrInvalid, got %v", err)
	}
}
