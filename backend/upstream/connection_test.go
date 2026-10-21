package upstream

import (
	"context"
	"strings"
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
)

func TestResolveDefaultsAndIndependentPresets(t *testing.T) {
	for _, preset := range Presets() {
		for _, profile := range preset.Profiles {
			cfg := Config{ProviderID: preset.ID, ProfileID: profile.ID}
			if preset.ID == "generic" {
				cfg.BaseURL = " http://localhost:1234/proxy/v2/// "
			}
			got, err := Resolve(cfg)
			if err != nil {
				t.Fatalf("%s/%s: %v", preset.ID, profile.ID, err)
			}
			if got.Type != profile.Type || got.AdapterID != "generic" || got.Options.SchemaVersion != 1 || got.Options.ModelDiscovery != "api" {
				t.Fatalf("incorrect defaults: %+v", got)
			}
			if preset.ID == "generic" && got.BaseURL != "http://localhost:1234/proxy/v2" {
				t.Fatal(got.BaseURL)
			}
		}
	}
	presets := Presets()
	presets[0].Profiles[0].BaseURL = "changed"
	if Presets()[0].Profiles[0].BaseURL != "" {
		t.Fatal("shared preset state")
	}
}

func TestResolveRejectsUnsafeOrOperationRoots(t *testing.T) {
	for _, base := range []string{"", "ftp://example.org", "https://user:super-secret@example.org", "https://example.org?key=super-secret", "https://example.org?", "https://example.org#super-secret", "https://example.org#", "https://example.org/v1/chat/completions/", "https://example.org/v1/responses", "https://example.org/v1/messages", "https://example.org/v1/models", "https://example.org/v1/models/m:generateContent", "https://example.org/%zz"} {
		_, err := Resolve(Config{Type: protocol.ProviderOpenAI, BaseURL: base})
		if err == nil {
			t.Errorf("accepted %q", base)
		} else if strings.Contains(err.Error(), "super-secret") {
			t.Errorf("unsafe error: %v", err)
		}
	}
}

func TestResolveRejectsUnknownSettings(t *testing.T) {
	for _, cfg := range []Config{
		{ProviderID: "missing"},
		{ProfileID: "missing"},
		{ProviderID: "anthropic", Type: protocol.ProviderOpenAI},
		{ProfileID: "chat", Type: protocol.ProviderGemini},
		{AdapterID: "missing"},
		{Options: Options{SchemaVersion: 2}},
		{Options: Options{ModelDiscovery: "other"}},
	} {
		cfg.BaseURL = "http://localhost/v1"
		if _, err := Resolve(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
}

func TestOpenPreservesHistoricalEndpointsAndHeaders(t *testing.T) {
	for _, typ := range []protocol.Provider{protocol.ProviderOpenAI, protocol.ProviderOpenAIResponses, protocol.ProviderAnthropic, protocol.ProviderGemini} {
		// 即使历史地址不能通过新校验，升级也不能悄悄更改请求目标。
		cfg := Config{ProviderID: "no-longer-listed", ProfileID: "old", Type: typ, BaseURL: "http://localhost/custom?legacy=value", Key: "test-key"}
		old, err := protocol.NewUpstream(typ, cfg.BaseURL, cfg.Key)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := Open(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if conn.ChatURL("model", true) != old.ChatURL("model", true) {
			t.Fatal("historical endpoint changed")
		}
		if conn.ChatHeaders().Get("Authorization") != old.ChatHeaders().Get("Authorization") || conn.ChatHeaders().Get("X-Api-Key") != old.ChatHeaders().Get("X-Api-Key") || conn.ChatHeaders().Get("X-Goog-Api-Key") != old.ChatHeaders().Get("X-Goog-Api-Key") {
			t.Fatal("historical authentication changed")
		}
	}
	cfg, err := Resolve(Config{ProviderID: "openai", BaseURL: "http://localhost/custom/v9", Options: Options{ModelDiscovery: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	// Open 不读取厂商目录，因此不会重新套用默认地址。
	cfg.ProfileID = "retired-profile"
	conn, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if conn.ChatURL("", false) != "http://localhost/custom/v9/chat/completions" {
		t.Fatal("preset reapplied")
	}
	if _, err = conn.FetchModels(context.Background()); err == nil {
		t.Fatal("manual mode must reject discovery")
	}
	cfg.AdapterID = "missing"
	if _, err = Open(cfg); err == nil {
		t.Fatal("unknown adapter silently accepted")
	}
}
