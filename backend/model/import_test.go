package model

import (
	"context"
	"errors"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/channel"
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
	"gorm.io/gorm"
)

func TestImportNamesIdempotentAndAtomic(t *testing.T) {
	setupSyncTest(t)
	ch := channel.Channel{Name: "import", Type: protocol.ProviderOpenAI, BaseURL: "https://x.invalid", Key: "k"}
	if err := shared.DB.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	added, err := ImportNamesTx(shared.DB, ch.ID, []string{" a ", "a", "b"})
	if err != nil || added != 2 {
		t.Fatalf("import added=%d err=%v", added, err)
	}
	var original Model
	if err := shared.DB.Where("channel_id = ? AND name = ?", ch.ID, "a").First(&original).Error; err != nil {
		t.Fatal(err)
	}
	added, err = ImportNamesTx(shared.DB, ch.ID, []string{"a", "b"})
	if err != nil || added != 0 {
		t.Fatalf("repeat added=%d err=%v", added, err)
	}
	var preserved Model
	if err := shared.DB.First(&preserved, original.ID).Error; err != nil || preserved.Name != "a" {
		t.Fatalf("id not preserved: %+v %v", preserved, err)
	}
	if _, err := ImportNamesTx(shared.DB, ch.ID, []string{"c", " "}); err == nil {
		t.Fatal("empty name accepted")
	}
	var count int64
	shared.DB.Model(&Model{}).Count(&count)
	if count != 2 {
		t.Fatal("invalid batch partially imported")
	}
	rollback := errors.New("rollback")
	err = shared.DB.Transaction(func(tx *gorm.DB) error {
		if _, err := ImportNamesTx(tx, ch.ID, []string{"c"}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	shared.DB.Model(&Model{}).Count(&count)
	if count != 2 {
		t.Fatal("transaction did not roll back")
	}
}

func TestManualSyncDoesNotCallUpstream(t *testing.T) {
	setupSyncTest(t)
	ch := channel.Channel{Name: "manual", Type: protocol.ProviderOpenAI, BaseURL: "invalid", ConnectionOptions: upstream.Options{ModelDiscovery: "manual"}}
	if _, err := SyncFromChannelContext(context.Background(), &ch); err != ErrManualDiscovery {
		t.Fatalf("manual sync error=%v", err)
	}
}
