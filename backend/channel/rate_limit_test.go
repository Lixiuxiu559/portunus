package channel

import (
	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"testing"
)

func TestRateLimitScopeCreateUpdateAndLegacy(t *testing.T) {
	setupExistsTest(t)
	for _, scope := range []string{"", "model", "channel"} {
		c, err := Create(CreateRequest{Name: "scope-" + scope, ProviderID: "openai", Key: "k", RateLimitScope: scope})
		if err != nil {
			t.Fatal(err)
		}
		want := NormalizeRateLimitScope(scope)
		saved, err := Get(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.RateLimitScope != want || saved.ToResponse().RateLimitScope != want {
			t.Fatalf("scope=%q response=%q want=%q", saved.RateLimitScope, saved.ToResponse().RateLimitScope, want)
		}
		name := "renamed-" + scope
		saved, err = Update(c.ID, UpdateRequest{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
		if saved.RateLimitScope != want {
			t.Fatal("omitted scope changed")
		}
	}
	c, err := Create(CreateRequest{Name: "invalid-update", ProviderID: "openai", Key: "k", RateLimitScope: "channel"})
	if err != nil {
		t.Fatal(err)
	}
	invalid, name := "all", "must-not-save"
	if _, err = Update(c.ID, UpdateRequest{Name: &name, RateLimitScope: &invalid}); err == nil {
		t.Fatal("invalid scope accepted")
	}
	saved, err := Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != c.Name || saved.RateLimitScope != "channel" {
		t.Fatalf("invalid update mutated record: %+v", saved)
	}
	empty := ""
	saved, err = Update(c.ID, UpdateRequest{RateLimitScope: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if saved.RateLimitScope != "model" {
		t.Fatal("explicit empty should reset to model")
	}
	if _, err = Create(CreateRequest{Name: "invalid-create", ProviderID: "openai", Key: "k", RateLimitScope: invalid}); err == nil {
		t.Fatal("invalid create accepted")
	}
	var count int64
	shared.DB.Model(&Channel{}).Where("name = ?", "invalid-create").Count(&count)
	if count != 0 {
		t.Fatal("invalid create persisted")
	}
	legacy := Channel{Name: "legacy", Type: protocol.ProviderOpenAI, BaseURL: "https://legacy.invalid", RateLimitScope: ""}
	if !legacy.Valid() || legacy.ToResponse().RateLimitScope != "model" {
		t.Fatal("legacy empty scope lost compatibility")
	}
	legacy.RateLimitScope = "invalid"
	if legacy.Valid() {
		t.Fatal("invalid scope passes Valid")
	}
}
