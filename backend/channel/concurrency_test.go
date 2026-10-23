package channel

import (
	"errors"
	"strconv"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestChannelConcurrencySafeIntegerBoundary(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("安全整数边界超出本机 int 范围，JSON binding 会先拒绝")
	}
	setupExistsTest(t)
	maxValue := MaxConcurrentRequestsLimit
	max := int(maxValue)
	c, err := Create(CreateRequest{Name: "maximum", ProviderID: "openai", Key: "secret", MaxConcurrentRequests: max})
	if err != nil || c.MaxConcurrentRequests != max || !c.Valid() {
		t.Fatalf("maximum rejected: %+v %v", c, err)
	}
	tooLarge := max + 1
	if _, err := Create(CreateRequest{Name: "too-large", ProviderID: "openai", Key: "secret", MaxConcurrentRequests: tooLarge}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe integer create: %v", err)
	}
	if _, err := Update(c.ID, UpdateRequest{MaxConcurrentRequests: &tooLarge}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsafe integer update: %v", err)
	}
	saved, err := Get(c.ID)
	if err != nil || saved.MaxConcurrentRequests != max {
		t.Fatalf("rejected update persisted: %+v %v", saved, err)
	}
	saved.MaxConcurrentRequests = tooLarge
	if saved.Valid() || ValidMaxConcurrentRequests(tooLarge) {
		t.Fatal("unsafe integer passes validation")
	}
	zero := 0
	if _, err := Update(c.ID, UpdateRequest{MaxConcurrentRequests: &zero}); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(c.ID, UpdateRequest{MaxConcurrentRequests: &max})
	if err != nil || updated.MaxConcurrentRequests != max {
		t.Fatalf("maximum update rejected: %+v %v", updated, err)
	}
}

func TestChannelConcurrencyConfiguration(t *testing.T) {
	setupExistsTest(t)
	c, err := Create(CreateRequest{Name: "limited", ProviderID: "openai", Key: "secret", MaxConcurrentRequests: 3})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		saved, err := Get(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.MaxConcurrentRequests != want || saved.ToResponse().MaxConcurrentRequests != want {
			t.Fatalf("limit=%d response=%d want=%d", saved.MaxConcurrentRequests, saved.ToResponse().MaxConcurrentRequests, want)
		}
	}
	check(3)
	name, baseURL := "renamed", "https://other.invalid/v1"
	for _, req := range []UpdateRequest{{Name: &name}, {BaseURL: &baseURL}} {
		if _, err := Update(c.ID, req); err != nil {
			t.Fatal(err)
		}
		check(3)
	}
	negative := -1
	if _, err := Update(c.ID, UpdateRequest{MaxConcurrentRequests: &negative}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative update: %v", err)
	}
	check(3)
	zero := 0
	if _, err := Update(c.ID, UpdateRequest{MaxConcurrentRequests: &zero}); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := Create(CreateRequest{Name: "invalid", ProviderID: "openai", Key: "secret", MaxConcurrentRequests: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative create: %v", err)
	}
	var count int64
	if err := shared.DB.Model(&Channel{}).Where("name = ?", "invalid").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("negative create persisted: count=%d err=%v", count, err)
	}
	defaulted, err := Create(CreateRequest{Name: "default", ProviderID: "openai", Key: "secret"})
	if err != nil || defaulted.MaxConcurrentRequests != 0 {
		t.Fatalf("default: %+v %v", defaulted, err)
	}
	legacy := Channel{Name: "legacy", Type: protocol.ProviderOpenAI, BaseURL: "https://legacy.invalid"}
	if !legacy.Valid() {
		t.Fatal("legacy zero limit rejected")
	}
	legacy.MaxConcurrentRequests = -1
	if legacy.Valid() {
		t.Fatal("negative limit passed Valid")
	}
}

func TestChannelConcurrencyLegacyMigration(t *testing.T) {
	setupExistsTest(t)
	if err := shared.DB.Migrator().DropTable(&Channel{}); err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Exec(`CREATE TABLE channels (id integer PRIMARY KEY, name text NOT NULL, base_url text, key text, type text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Exec(`INSERT INTO channels (id, name, base_url, key, type) VALUES (1, 'legacy', 'https://legacy.invalid', 'secret', 'openai')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.AutoMigrate(&Channel{}); err != nil {
		t.Fatal(err)
	}
	c, err := Get(1)
	if err != nil || c.MaxConcurrentRequests != 0 || c.ToResponse().MaxConcurrentRequests != 0 {
		t.Fatalf("legacy migration: %+v %v", c, err)
	}
}
