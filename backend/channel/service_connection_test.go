package channel

import (
	"testing"

	"github.com/Lixiuxiu559/portunus/backend/protocol"
	"github.com/Lixiuxiu559/portunus/backend/shared"
	"github.com/Lixiuxiu559/portunus/backend/upstream"
)

func TestCreatePresetAndExplicitFalse(t *testing.T) {
	setupExistsTest(t)
	off := false
	c, err := Create(CreateRequest{Name: "preset", ProviderID: "openai", Key: "secret", AutoSync: &off, ThinkingCompat: &off})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AutoSync || saved.ThinkingCompat || saved.BaseURL != "https://api.openai.com/v1" || saved.Type != protocol.ProviderOpenAI || saved.ProfileID != "chat" || saved.ConnectionOptions.SchemaVersion != 1 {
		t.Fatalf("defaults or explicit false lost: %+v", saved)
	}
	if _, err := Create(CreateRequest{Name: "bad", ProviderID: "openai", Key: "k", AdapterID: "unknown"}); err == nil {
		t.Fatal("unknown adapter accepted")
	}
	if _, err := Create(CreateRequest{Name: "no-key", ProviderID: "openai"}); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestUnrelatedUpdateKeepsLegacyConnection(t *testing.T) {
	setupExistsTest(t)
	old := Channel{Name: "legacy", Type: protocol.ProviderOpenAI, BaseURL: "https://legacy.invalid/models?compat=1", Key: "secret"}
	if err := shared.DB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	c, err := Update(old.ID, UpdateRequest{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != old.BaseURL || c.ConnectionOptions != old.ConnectionOptions {
		t.Fatalf("unrelated update changed config: %+v", c)
	}
	key := "new-secret"
	if _, err := Update(old.ID, UpdateRequest{Key: &key}); err == nil {
		t.Fatal("changed invalid connection accepted")
	}
	empty := ""
	if _, err := Update(old.ID, UpdateRequest{Key: &empty}); err != nil {
		t.Fatalf("empty key should preserve existing: %v", err)
	}
}

func TestManualModeDisablesAutoSync(t *testing.T) {
	setupExistsTest(t)
	on := true
	c, err := Create(CreateRequest{Name: "manual", ProviderID: "openai", Key: "k", AutoSync: &on, ConnectionOptions: upstream.Options{ModelDiscovery: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AutoSync || saved.ConnectionOptions.ModelDiscovery != "manual" {
		t.Fatalf("manual state not preserved: %+v", saved)
	}
}

func TestLegacyChannelSchemaMigration(t *testing.T) {
	setupExistsTest(t)
	if err := shared.DB.Migrator().DropTable(&Channel{}); err != nil {
		t.Fatal(err)
	}
	ddl := `CREATE TABLE channels (id integer PRIMARY KEY, name text NOT NULL UNIQUE, type text, base_url text, key text, auto_sync numeric DEFAULT true, thinking_compat numeric DEFAULT true, created_at datetime, updated_at datetime)`
	if err := shared.DB.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.DB.Exec(`INSERT INTO channels (id,name,type,base_url,key,auto_sync,thinking_compat) VALUES (41,'old','openai','https://old.invalid/v1','k',false,false)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := shared.AutoMigrate(&Channel{}); err != nil {
		t.Fatal(err)
	}
	c, err := Get(41)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProviderID != "generic" || c.AdapterID != "generic" || c.RateLimitScope != "model" || c.Icon != "" || c.AutoSync || c.ThinkingCompat || c.BaseURL != "https://old.invalid/v1" {
		t.Fatalf("migration changed legacy fields: %+v", c)
	}
	if _, err := upstream.Open(c.ConnectionConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyProtocolUpdateReselectsProfile(t *testing.T) {
	setupExistsTest(t)
	c, err := Create(CreateRequest{Name: "switch", Type: protocol.ProviderOpenAI, BaseURL: "https://x.invalid/v1", Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	typ := protocol.ProviderAnthropic
	c, err = Update(c.ID, UpdateRequest{Type: &typ})
	if err != nil {
		t.Fatal(err)
	}
	if c.ProfileID != "messages" || c.Type != typ || c.BaseURL != "https://x.invalid/v1" {
		t.Fatalf("protocol change did not rematch profile while preserving URL: %+v", c)
	}
}

func TestEquivalentLegacyOptionsDoNotRevalidateConnection(t *testing.T) {
	setupExistsTest(t)
	old := Channel{Name: "legacy-options", Type: protocol.ProviderOpenAI, BaseURL: "https://old.invalid/v1?legacy=preserved", Key: "k", AutoSync: true}
	if err := shared.DB.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	options := upstream.Options{SchemaVersion: 1, ModelDiscovery: "api"}
	got, err := Update(old.ID, UpdateRequest{Name: &name, ConnectionOptions: &options})
	if err != nil || got.BaseURL != old.BaseURL || !got.AutoSync || got.ConnectionOptions != old.ConnectionOptions {
		t.Fatalf("equivalent options changed legacy connection: %+v %v", got, err)
	}
}
